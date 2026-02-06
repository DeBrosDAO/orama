package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// requestLogEntry holds a single request log to be batched.
type requestLogEntry struct {
	method     string
	path       string
	statusCode int
	bytesOut   int
	durationMs int64
	ip         string
	apiKey     string // raw API key (resolved to ID at flush time in batch)
}

// requestLogBatcher aggregates request logs and flushes them to RQLite in bulk
// instead of issuing 3 DB writes per request (INSERT log + SELECT api_key_id + UPDATE last_used).
type requestLogBatcher struct {
	gw        *Gateway
	entries   []requestLogEntry
	mu        sync.Mutex
	interval  time.Duration
	maxBatch  int
	stopCh    chan struct{}
}

func newRequestLogBatcher(gw *Gateway, interval time.Duration, maxBatch int) *requestLogBatcher {
	b := &requestLogBatcher{
		gw:       gw,
		entries:  make([]requestLogEntry, 0, maxBatch),
		interval: interval,
		maxBatch: maxBatch,
		stopCh:   make(chan struct{}),
	}
	go b.run()
	return b
}

// Add enqueues a log entry. If the buffer is full, it triggers an early flush.
func (b *requestLogBatcher) Add(entry requestLogEntry) {
	b.mu.Lock()
	b.entries = append(b.entries, entry)
	needsFlush := len(b.entries) >= b.maxBatch
	b.mu.Unlock()

	if needsFlush {
		go b.flush()
	}
}

// run is the background loop that flushes logs periodically.
func (b *requestLogBatcher) run() {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.flush()
		case <-b.stopCh:
			b.flush() // final flush on stop
			return
		}
	}
}

// flush writes all buffered log entries to RQLite in a single batch.
func (b *requestLogBatcher) flush() {
	b.mu.Lock()
	if len(b.entries) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.entries
	b.entries = make([]requestLogEntry, 0, b.maxBatch)
	b.mu.Unlock()

	if b.gw.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := b.gw.client.Database()

	// Collect unique API keys that need ID resolution
	apiKeySet := make(map[string]struct{})
	for _, e := range batch {
		if e.apiKey != "" {
			apiKeySet[e.apiKey] = struct{}{}
		}
	}

	// Batch-resolve API key IDs in a single query
	apiKeyIDs := make(map[string]int64)
	if len(apiKeySet) > 0 {
		keys := make([]string, 0, len(apiKeySet))
		for k := range apiKeySet {
			keys = append(keys, k)
		}

		placeholders := make([]string, len(keys))
		args := make([]interface{}, len(keys))
		for i, k := range keys {
			placeholders[i] = "?"
			args[i] = k
		}

		q := fmt.Sprintf("SELECT id, key FROM api_keys WHERE key IN (%s)", strings.Join(placeholders, ","))
		res, err := db.Query(client.WithInternalAuth(ctx), q, args...)
		if err == nil && res != nil {
			for _, row := range res.Rows {
				if len(row) >= 2 {
					var id int64
					switch v := row[0].(type) {
					case float64:
						id = int64(v)
					case int64:
						id = v
					}
					if key, ok := row[1].(string); ok && id > 0 {
						apiKeyIDs[key] = id
					}
				}
			}
		}
	}

	// Build batch INSERT for request_logs
	if len(batch) > 0 {
		var sb strings.Builder
		sb.WriteString("INSERT INTO request_logs (method, path, status_code, bytes_out, duration_ms, ip, api_key_id) VALUES ")

		args := make([]interface{}, 0, len(batch)*7)
		for i, e := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?)")

			var apiKeyID interface{} = nil
			if e.apiKey != "" {
				if id, ok := apiKeyIDs[e.apiKey]; ok {
					apiKeyID = id
				}
			}
			args = append(args, e.method, e.path, e.statusCode, e.bytesOut, e.durationMs, e.ip, apiKeyID)
		}

		_, _ = db.Query(client.WithInternalAuth(ctx), sb.String(), args...)
	}

	// Batch UPDATE last_used_at for all API keys seen in this batch
	if len(apiKeyIDs) > 0 {
		ids := make([]string, 0, len(apiKeyIDs))
		args := make([]interface{}, 0, len(apiKeyIDs))
		for _, id := range apiKeyIDs {
			ids = append(ids, "?")
			args = append(args, id)
		}

		q := fmt.Sprintf("UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id IN (%s)", strings.Join(ids, ","))
		_, _ = db.Query(client.WithInternalAuth(ctx), q, args...)
	}

	if b.gw.logger != nil {
		b.gw.logger.ComponentDebug(logging.ComponentGeneral, "request logs flushed",
			zap.Int("count", len(batch)),
			zap.Int("api_keys", len(apiKeyIDs)),
		)
	}
}

// Stop signals the batcher to stop and flush remaining entries.
func (b *requestLogBatcher) Stop() {
	close(b.stopCh)
}
