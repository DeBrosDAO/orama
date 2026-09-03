package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"
)

// DefaultListenAddr is the localhost HTTP API for orama-namespace-pubsub@index.
const DefaultListenAddr = "127.0.0.1:10105"

type publishBody struct {
	Namespace string `json:"namespace"`
	Topic     string `json:"topic"`
	DataB64   string `json:"data"`
}

type publishBatchBody struct {
	Namespace  string        `json:"namespace"`
	Messages   []publishBody `json:"messages"`
	BestEffort bool          `json:"best_effort"`
}

// Handler serves the localhost pubsub HTTP API on top of a Manager.
func Handler(mgr *Manager, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/publish", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body publishBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Topic == "" || body.Namespace == "" {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		data, err := base64.StdEncoding.DecodeString(body.DataB64)
		if err != nil {
			http.Error(w, "invalid base64", http.StatusBadRequest)
			return
		}
		ctx := WithNamespace(r.Context(), body.Namespace)
		if err := mgr.Publish(ctx, body.Topic, data); err != nil {
			logger.Warn("publish failed", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/publish-batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body publishBatchBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Namespace == "" {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		msgs := make([]TopicMessage, 0, len(body.Messages))
		for _, m := range body.Messages {
			data, err := base64.StdEncoding.DecodeString(m.DataB64)
			if err != nil {
				http.Error(w, "invalid base64", http.StatusBadRequest)
				return
			}
			msgs = append(msgs, TopicMessage{Topic: m.Topic, Data: data})
		}
		ctx := WithNamespace(r.Context(), body.Namespace)
		if err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{BestEffort: body.BestEffort}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ns := r.URL.Query().Get("namespace")
		topic := r.URL.Query().Get("topic")
		if ns == "" || topic == "" {
			http.Error(w, "namespace and topic required", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch := make(chan []byte, 32)
		ctx := WithNamespace(r.Context(), ns)
		err := mgr.Subscribe(ctx, topic, func(_ string, data []byte) error {
			select {
			case ch <- data:
			default:
			}
			return nil
		})
		if err != nil {
			logger.Warn("subscribe failed", zap.Error(err))
			return
		}
		defer func() { _ = mgr.Unsubscribe(ctx, topic) }()

		for {
			select {
			case <-r.Context().Done():
				return
			case data := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString(data))
				flusher.Flush()
			}
		}
	})
	return mux
}
