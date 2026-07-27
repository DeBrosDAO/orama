package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

const (
	// internalGatewayPort is the per-node internal gateway HTTP port on the
	// WireGuard mesh, where /v1/internal/* endpoints (including storage evict)
	// are served. Matches the port used by deployment replica coordination.
	internalGatewayPort = 6001

	// storageInternalAuthMarker is the X-Orama-Internal-Auth value for
	// storage-coordination internal calls. The real security is the WireGuard
	// source-IP check (auth.IsWireGuardPeer); the marker is a defence-in-depth
	// discriminator, matching the deployment/namespace coordination pattern.
	storageInternalAuthMarker = "storage-coordination"

	// evictFanoutTimeout bounds a single node's evict call during fan-out.
	evictFanoutTimeout = 30 * time.Second
)

// remainingPinsForCID returns how many namespaces still hold a live pin on this
// CID (bugboard #153). A CID uploaded by multiple namespaces shares ONE cluster
// pin, so immediate eviction must only proceed when no namespace still
// references it — otherwise a shared blob would be destroyed for the others.
func (h *Handlers) remainingPinsForCID(ctx context.Context, cid string) (int, error) {
	if h.db == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result []map[string]interface{}
	query := `SELECT COUNT(*) as count FROM ipfs_content_ownership WHERE cid = ? AND is_pinned = 1`
	if err := h.db.Query(ctx, &result, query, cid); err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return countFromRow(result[0]["count"]), nil
}

// cidPinnedByOtherNamespace reports whether any namespace OTHER than the given
// one still holds a live pin on this CID (bugboard #156). IPFS-Cluster dedups to
// one pin per CID, so the caller's unpin must NOT remove the shared cluster pin
// while another namespace still references the content — doing so orphans that
// namespace's data at the next GC. Used to gate the cluster-pin removal.
func (h *Handlers) cidPinnedByOtherNamespace(ctx context.Context, cid, namespace string) (bool, error) {
	if h.db == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result []map[string]interface{}
	query := `SELECT COUNT(*) as count FROM ipfs_content_ownership WHERE cid = ? AND namespace != ? AND is_pinned = 1`
	if err := h.db.Query(ctx, &result, query, cid, namespace); err != nil {
		return false, err
	}
	if len(result) == 0 {
		return false, nil
	}
	return countFromRow(result[0]["count"]) > 0, nil
}

// countFromRow coerces a COUNT(*) cell (rqlite returns float64 or int64) to int.
func countFromRow(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// activeNodeInternalIPs returns the internal (WireGuard) IPs of all active
// cluster nodes — the fan-out target set for immediate eviction. A node that
// does not hold the block simply no-ops its local block rm, so targeting all
// active nodes is safe (and the cluster's RF replicas are a subset).
func (h *Handlers) activeNodeInternalIPs(ctx context.Context) ([]string, error) {
	if h.db == nil {
		return nil, nil
	}
	// Only nodes with a real WireGuard internal IP: the fan-out is an
	// internal-auth call that must travel the WG mesh (the receiver rejects any
	// non-10.0.0.x source), so never fall back to a public ip_address.
	var result []map[string]interface{}
	query := `SELECT internal_ip as ip FROM dns_nodes WHERE status = 'active' AND internal_ip IS NOT NULL AND internal_ip != ''`
	if err := h.db.Query(ctx, &result, query); err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(result))
	for _, row := range result {
		if ip, ok := row["ip"].(string); ok && ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

// evictBlobEverywhere fans out an immediate local eviction of a CID to every
// active cluster node (bugboard #153). Best-effort: per-node failures are
// logged but do not fail the caller — the pin is already removed cluster-wide,
// so eviction is disk-reclaim + privacy hardening layered on top. Returns true
// only when every targeted node reported success.
func (h *Handlers) evictBlobEverywhere(ctx context.Context, cid string) bool {
	ips, err := h.activeNodeInternalIPs(ctx)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "immediate evict: failed to list nodes (skipping fan-out)",
			zap.String("cid", cid), zap.Error(err))
		return false
	}
	if len(ips) == 0 {
		return false
	}

	payload, err := json.Marshal(map[string]string{"cid": cid})
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "immediate evict: failed to marshal payload",
			zap.String("cid", cid), zap.Error(err))
		return false
	}

	// Fan out concurrently: eviction is best-effort disk reclaim on top of an
	// already-completed cluster unpin, so the whole fan-out must be bounded by a
	// single node timeout, not N × timeout on the unpin response path.
	var wg sync.WaitGroup
	var mu sync.Mutex
	allOK := true
	markFailed := func() {
		mu.Lock()
		allOK = false
		mu.Unlock()
	}
	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			url := fmt.Sprintf("http://%s:%d/v1/internal/storage/evict", ip, internalGatewayPort)
			reqCtx, cancel := context.WithTimeout(ctx, evictFanoutTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(payload))
			if err != nil {
				markFailed()
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Orama-Internal-Auth", storageInternalAuthMarker)

			resp, err := (&http.Client{Timeout: evictFanoutTimeout}).Do(req)
			if err != nil {
				h.logger.ComponentWarn(logging.ComponentGeneral, "immediate evict: node call failed (best-effort)",
					zap.String("cid", cid), zap.String("node_ip", ip), zap.Error(err))
				markFailed()
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				h.logger.ComponentWarn(logging.ComponentGeneral, "immediate evict: node returned non-200 (best-effort)",
					zap.String("cid", cid), zap.String("node_ip", ip), zap.Int("status", resp.StatusCode))
				markFailed()
			}
		}(ip)
	}
	wg.Wait()
	return allOK
}

// EvictHandler serves POST /v1/internal/storage/evict — the per-node side of
// immediate eviction (bugboard #153). It removes the CID's blocks from THIS
// node's local kubo blockstore. Internal-only: WireGuard source + marker header.
func (h *Handlers) EvictHandler(w http.ResponseWriter, r *http.Request) {
	if !httputil.CheckMethod(w, r, http.MethodPost) {
		return
	}
	if !h.isInternalStorageRequest(r) {
		httputil.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req struct {
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "cid required")
		return
	}

	removed, err := h.ipfsClient.EvictLocal(r.Context(), req.CID)
	if err != nil {
		// Partial/failed eviction is reported but is not a hard failure — the
		// caller treats eviction as best-effort disk reclaim on top of the
		// already-completed cluster unpin.
		h.logger.ComponentWarn(logging.ComponentGeneral, "local evict incomplete",
			zap.String("cid", req.CID), zap.Int("removed", removed), zap.Error(err))
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "partial", "cid": req.CID, "removed": removed})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": req.CID, "removed": removed})
}

// isInternalStorageRequest authorizes an internal storage call: the caller must
// present the storage-coordination marker AND originate from the WireGuard mesh.
func (h *Handlers) isInternalStorageRequest(r *http.Request) bool {
	if r.Header.Get("X-Orama-Internal-Auth") != storageInternalAuthMarker {
		return false
	}
	return auth.IsWireGuardPeer(r.RemoteAddr)
}
