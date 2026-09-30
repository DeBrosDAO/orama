// Package gw is the gateway client feature tests use: HTTP/1.1 through the real
// public path, trusting only the run's pinned CA bundle, with every exchange
// captured as evidence. It also implements the sign-in flows with real
// signatures and a WebSocket dialer.
package gw

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// Budgets and bounds.
const (
	// RequestBudget bounds one request when the caller's context has no deadline.
	RequestBudget = 30 * time.Second
	// MaxResponseBytes bounds a response body read into memory.
	MaxResponseBytes = 32 << 20
	// Connection reuse bounds: a package builds many clients (one per
	// namespace, per user), so idle connections are closed instead of
	// being kept, each with its file descriptor, until the process exits.
	idleConnTimeout     = 30 * time.Second
	maxIdleConnsPerHost = 4
	tlsHandshakeTimeout = 15 * time.Second
)

// Client talks to one gateway base URL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	TLS     *tls.Config
	rec     *evidence.Recorder
	test    string
	// pacer paces credential requests (nil: not paced); unpaced opts out.
	pacer   *pace.Pacer
	unpaced bool
	// pinIP is the node PinTo pinned the client to; pinErr a PinTo failure.
	pinIP  string
	pinErr error
}

// LoadCAPool reads a PEM bundle into a pool that holds nothing else: the
// system roots are deliberately absent, so a certificate that only a public CA
// would accept fails the handshake.
func LoadCAPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the run's CA bundle %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA bundle %s holds no PEM certificate", caFile)
	}
	return pool, nil
}

// New returns a client for baseURL pinned to caFile's roots. HTTP/2 is off:
// clients reach the gateway over HTTP/1.1 through Caddy, and the tests must
// exercise that path.
func New(baseURL, caFile string, rec *evidence.Recorder) (*Client, error) {
	pool, err := LoadCAPool(caFile)
	if err != nil {
		return nil, err
	}
	return NewWithTLS(baseURL, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, rec)
}

// NewWithTLS returns a client with an explicit TLS config (unit tests, and
// negative tests that must present a different trust store). In fleet mode
// (E2E_FLEET_STATE set) the URL must be https and tlsCfg set: a fleet test
// never talks to the gateway in clear text or on the system roots by accident,
// and credential requests are paced by the run's pacer (pace.FromEnv).
func NewWithTLS(baseURL string, tlsCfg *tls.Config, rec *evidence.Recorder) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("gateway URL %q must be an absolute http(s) URL", baseURL)
	}
	if inFleetMode() && (u.Scheme != "https" || tlsCfg == nil) {
		return nil, fmt.Errorf("gateway URL %q: in fleet mode a client must use https with an explicit TLS config", baseURL)
	}
	pacer, err := pace.FromEnv(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to set up credential pacing for %s: %w", baseURL, err)
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		Proxy:               nil,
		IdleConnTimeout:     idleConnTimeout,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Transport: transport, CheckRedirect: noRedirects},
		TLS:     tlsCfg,
		rec:     rec,
		pacer:   pacer,
	}, nil
}

// noRedirects returns redirects to the test instead of following them: a
// redirect is a response worth asserting on.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// For returns a copy of c whose evidence is attributed to t.
func (c *Client) For(t testing.TB) *Client {
	cp := *c
	cp.test = t.Name()
	return &cp
}

// WithBase returns a copy of c aimed at another base URL on the same trust
// (a namespace gateway, https://ns-<name>.<base>).
func (c *Client) WithBase(baseURL string) *Client {
	cp := *c
	cp.BaseURL = strings.TrimRight(baseURL, "/")
	return &cp
}

// Recorder returns the evidence recorder (nil outside a run).
func (c *Client) Recorder() *evidence.Recorder { return c.rec }

// Response is a fully read response.
type Response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
}

// Do sends req, reads the whole body and records the exchange. A request to a
// credential route first waits on the run's pacer, and a 429 to it is a
// *PacingError (see Unpaced).
func (c *Client) Do(req *http.Request) (*Response, error) {
	if c.pinErr != nil {
		return nil, c.pinErr
	}
	reqBody := snapshotBody(req)
	paced, err := c.paceRequest(req.Context(), req, reqBody)
	if err != nil {
		return nil, err
	}
	ctx := req.Context()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, RequestBudget)
		defer cancel()
		req = req.WithContext(ctx)
	}
	start := time.Now()
	httpResp, err := c.HTTP.Do(req)
	resp := &Response{Duration: time.Since(start)}
	if err == nil {
		resp.Status, resp.Header = httpResp.StatusCode, httpResp.Header
		resp.Body, err = io.ReadAll(io.LimitReader(httpResp.Body, MaxResponseBytes))
		closeErr := httpResp.Body.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close response body: %w", closeErr)
		}
	}
	if err != nil {
		err = fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	if err == nil && paced {
		err = pacingErr(req, resp)
	}
	if recErr := c.record(req, reqBody, resp, err); recErr != nil {
		return resp, errors.Join(err, recErr)
	}
	return resp, err
}

// inFleetMode reports whether this process runs against a fleet.
func inFleetMode() bool {
	mode, err := config.FromEnv(os.LookupEnv)
	return err != nil || mode.StatePath != ""
}

// snapshotBody reads a replayable copy of the request body for evidence.
func snapshotBody(req *http.Request) []byte {
	if req.GetBody == nil {
		return nil
	}
	rc, err := req.GetBody()
	if err != nil {
		return []byte(fmt.Sprintf("[body unavailable: %v]", err))
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, evidence.MaxFieldBytes+1))
	if err != nil {
		return []byte(fmt.Sprintf("[body unreadable: %v]", err))
	}
	return b
}

func (c *Client) record(req *http.Request, body []byte, resp *Response, err error) error {
	rec := evidence.Record{
		Kind: evidence.KindHTTP, Test: c.test, Summary: req.Method + " " + req.URL.String() + c.pinNote(),
		Status: resp.Status, DurationMS: resp.Duration.Milliseconds(),
		Input:  dumpHeaders(req.Header) + "\n" + string(body),
		Output: dumpHeaders(resp.Header) + "\n" + string(resp.Body),
	}
	if err != nil {
		rec.Error = err.Error()
	}
	if recErr := c.rec.Add(rec); recErr != nil {
		return fmt.Errorf("failed to record %s %s: %w", req.Method, req.URL.Path, recErr)
	}
	return nil
}

func dumpHeaders(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}
