package pubsub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// HTTPClient is a Bus that talks to the localhost pubsub HTTP API.
type HTTPClient struct {
	baseURL   string
	namespace string
	http      *http.Client
	logger    *zap.Logger

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

var _ Bus = (*HTTPClient)(nil)

// NewHTTPClient returns a Bus pointed at the @index pubsub HTTP API.
func NewHTTPClient(baseURL, namespace string, logger *zap.Logger) *HTTPClient {
	if baseURL == "" {
		baseURL = "http://" + DefaultListenAddr
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &HTTPClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		namespace: namespace,
		http:      &http.Client{Timeout: 10 * time.Second},
		logger:    logger.Named("pubsub-http"),
		cancels:   make(map[string]context.CancelFunc),
	}
}

func (c *HTTPClient) ns(ctx context.Context) string {
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return c.namespace
}

func (c *HTTPClient) Publish(ctx context.Context, topic string, data []byte) error {
	body, err := json.Marshal(publishBody{
		Namespace: c.ns(ctx),
		Topic:     topic,
		DataB64:   base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("pubsub publish: %s %s", resp.Status, slurp)
	}
	return nil
}

func (c *HTTPClient) PublishBatch(ctx context.Context, msgs []TopicMessage, opts PublishBatchOptions) error {
	entries := make([]publishBody, len(msgs))
	for i, m := range msgs {
		entries[i] = publishBody{Topic: m.Topic, DataB64: base64.StdEncoding.EncodeToString(m.Data)}
	}
	body, err := json.Marshal(publishBatchBody{
		Namespace:  c.ns(ctx),
		Messages:   entries,
		BestEffort: opts.BestEffort,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/publish-batch", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("pubsub publish-batch: %s %s", resp.Status, slurp)
	}
	return nil
}

func (c *HTTPClient) PublishSame(ctx context.Context, topics []string, data []byte, opts PublishBatchOptions) error {
	msgs := make([]TopicMessage, len(topics))
	for i, t := range topics {
		msgs[i] = TopicMessage{Topic: t, Data: data}
	}
	return c.PublishBatch(ctx, msgs, opts)
}

func (c *HTTPClient) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	ns := c.ns(ctx)
	key := ns + "." + topic
	subCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if prev, ok := c.cancels[key]; ok {
		prev()
	}
	c.cancels[key] = cancel
	c.mu.Unlock()

	url := fmt.Sprintf("%s/subscribe?namespace=%s&topic=%s", c.baseURL, url.QueryEscape(ns), url.QueryEscape(topic))
	req, err := http.NewRequestWithContext(subCtx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return err
	}
	go func() {
		client := &http.Client{Timeout: 0}
		resp, err := client.Do(req)
		if err != nil {
			c.logger.Warn("subscribe request failed", zap.Error(err))
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "data: "))
			if err != nil {
				continue
			}
			_ = handler(topic, raw)
		}
	}()
	return nil
}

func (c *HTTPClient) Unsubscribe(_ context.Context, topic string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.namespace + "." + topic
	if cancel, ok := c.cancels[key]; ok {
		cancel()
		delete(c.cancels, key)
	}
	return nil
}

func (c *HTTPClient) ListTopics(context.Context) ([]string, error) {
	return nil, nil
}

func (c *HTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, cancel := range c.cancels {
		cancel()
		delete(c.cancels, k)
	}
	return nil
}
