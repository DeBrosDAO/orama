package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

// HTTPClient is a Bus that talks to the node's pubsub HTTP API over its unix
// socket (DefaultSocketPath).
type HTTPClient struct {
	baseURL   string
	namespace string
	transport http.RoundTripper
	http      *http.Client
	logger    *zap.Logger

	mu      sync.Mutex
	streams map[string]*stream // namespace.topic -> the one upstream stream
}

var _ Bus = (*HTTPClient)(nil)

// socketBaseURL is the URL every request is made against. The host names
// nothing: the transport dials the socket whatever the URL says.
const socketBaseURL = "http://pubsub"

// requestTimeout bounds a publish; a subscription is a stream and has none.
const requestTimeout = 10 * time.Second

// NewHTTPClient returns a Bus that reaches the @index pubsub API on the unix
// socket at socketPath.
func NewHTTPClient(socketPath, namespace string, logger *zap.Logger) *HTTPClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &HTTPClient{
		baseURL:   socketBaseURL,
		namespace: namespace,
		transport: transport,
		http:      &http.Client{Timeout: requestTimeout, Transport: transport},
		logger:    logger.Named("pubsub-http"),
		streams:   make(map[string]*stream),
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

// ListTopics returns the topics of the client's namespace (or the namespace
// override in ctx) that this node's pubsub service has a subscription on.
func (c *HTTPClient) ListTopics(ctx context.Context) ([]string, error) {
	ns := c.ns(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/topics?namespace="+url.QueryEscape(ns), nil)
	if err != nil {
		return nil, fmt.Errorf("pubsub list topics: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pubsub list topics for namespace %q: %w", ns, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("pubsub list topics for namespace %q: %s %s", ns, resp.Status, slurp)
	}
	var out topicsBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("pubsub list topics for namespace %q: decode reply: %w", ns, err)
	}
	return out.Topics, nil
}

// Close ends every upstream stream. Handlers stop receiving; their stop
// functions become no-ops.
func (c *HTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, st := range c.streams {
		st.cancel()
		delete(c.streams, k)
	}
	return nil
}

// meshCall makes one request to the mesh API and decodes the reply into out.
func (c *HTTPClient) meshCall(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, slurp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decode reply: %w", method, path, err)
	}
	return nil
}
