// Package txgate is the HTTP gate between a validator's onion service and its
// chain REST API. The onion service is reachable by anyone on the Orama Tor
// network, so the gate serves the three calls a wallet needs to submit one
// transaction and nothing else: read the signer's account, broadcast the
// transaction, look the transaction up. Every other path of the chain API (the
// query endpoints, the transaction search, the node and consensus services)
// answers 404 here.
//
// Requests all arrive from the local Tor process, so the gate cannot tell
// callers apart and keeps no record of them: no request is logged, and the
// limits are on the whole gate (a request rate and the number of requests in
// flight). A caller that is refused for load gets 429 and tries another
// validator's onion service.
package txgate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	pathAccountPrefix = "/cosmos/auth/v1beta1/accounts/"
	pathTxPrefix      = "/cosmos/tx/v1beta1/txs/"
	pathBroadcast     = "/cosmos/tx/v1beta1/txs"

	// MaxBody bounds a broadcast request. A transaction with a shielded proof
	// is the largest the chain accepts.
	MaxBody = 512 << 10
	// maxResponse bounds what the gate copies back from the chain API.
	maxResponse = 1 << 20

	contentJSON = "application/json"

	// DefaultRate is the requests per second the gate forwards in total.
	DefaultRate = 20
	// DefaultBurst is how many requests may arrive at once before the rate applies.
	DefaultBurst = 40
	// DefaultInFlight is the most requests the chain API is asked at once.
	DefaultInFlight = 16
	// DefaultUpstreamTimeout bounds one call to the chain API.
	DefaultUpstreamTimeout = 20 * time.Second
)

// A bech32 address uses the alphabet without 1, b, i and o after the "1" separator.
var (
	accountPattern = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{20,100}$`)
	txHashPattern  = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)
)

// Config is a gate.
type Config struct {
	// Upstream is the chain REST API, "http://host:port".
	Upstream string
	// Rate and Burst limit requests per second for the whole gate.
	Rate  float64
	Burst int
	// InFlight caps the requests being answered at once.
	InFlight int
	// UpstreamTimeout bounds one call to the chain API.
	UpstreamTimeout time.Duration
	// Client calls the chain API; nil means a client with UpstreamTimeout.
	Client *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Gate is the HTTP handler.
type Gate struct {
	upstream *url.URL
	client   *http.Client
	sem      chan struct{}
	bucket   *bucket
}

// New builds a gate. The upstream must be a plain http URL with a host and no
// path: the gate adds the path of each call itself.
func New(c Config) (*Gate, error) {
	u, err := url.Parse(c.Upstream)
	if err != nil || u.Scheme != "http" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("txgate upstream %q must be http://host:port", c.Upstream)
	}
	if c.Rate <= 0 || c.Burst < 1 || c.InFlight < 1 {
		return nil, fmt.Errorf("txgate limits must be positive: rate %v burst %d in-flight %d", c.Rate, c.Burst, c.InFlight)
	}
	timeout := c.UpstreamTimeout
	if timeout <= 0 {
		return nil, errors.New("txgate upstream timeout must be positive")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Gate{
		upstream: &url.URL{Scheme: u.Scheme, Host: u.Host},
		client:   client,
		sem:      make(chan struct{}, c.InFlight),
		bucket:   newBucket(c.Rate, c.Burst, now),
	}, nil
}

// ServeHTTP implements http.Handler.
func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, ok := route(r)
	if !ok {
		reply(w, http.StatusNotFound, `{"error":"not served over the onion service"}`)
		return
	}
	if r.Method != target.method {
		w.Header().Set("Allow", target.method)
		reply(w, http.StatusMethodNotAllowed, `{"error":"method not allowed"}`)
		return
	}
	var body []byte
	if target.method == http.MethodPost {
		var err error
		if body, err = readBody(w, r); err != nil {
			reply(w, http.StatusRequestEntityTooLarge, `{"error":"transaction body too large or unreadable"}`)
			return
		}
		if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != contentJSON {
			reply(w, http.StatusUnsupportedMediaType, `{"error":"content type must be application/json"}`)
			return
		}
	}
	if !g.bucket.take() {
		w.Header().Set("Retry-After", "5")
		reply(w, http.StatusTooManyRequests, `{"error":"busy, try another validator"}`)
		return
	}
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	default:
		w.Header().Set("Retry-After", "5")
		reply(w, http.StatusTooManyRequests, `{"error":"busy, try another validator"}`)
		return
	}
	g.forward(w, r.Context(), target, body)
}

type call struct {
	method string
	path   string
}

// route is the one upstream call a request may become, and whether it is allowed.
func route(r *http.Request) (call, bool) {
	p := r.URL.Path
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		return call{}, false
	}
	switch {
	case p == pathBroadcast:
		return call{http.MethodPost, pathBroadcast}, true
	case strings.HasPrefix(p, pathAccountPrefix) && accountPattern.MatchString(p[len(pathAccountPrefix):]):
		return call{http.MethodGet, p}, true
	case strings.HasPrefix(p, pathTxPrefix) && txHashPattern.MatchString(p[len(pathTxPrefix):]):
		return call{http.MethodGet, p}, true
	}
	return call{}, false
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
}

func (g *Gate) forward(w http.ResponseWriter, ctx context.Context, c call, body []byte) {
	u := *g.upstream
	u.Path = c.path
	req, err := http.NewRequestWithContext(ctx, c.method, u.String(), bytes.NewReader(body))
	if err != nil {
		reply(w, http.StatusBadGateway, `{"error":"the chain API did not answer"}`)
		return
	}
	// Only the content type crosses: no cookie, forwarding header or user agent
	// of the caller reaches the chain API, and none is logged here.
	if c.method == http.MethodPost {
		req.Header.Set("Content-Type", contentJSON)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		reply(w, http.StatusBadGateway, `{"error":"the chain API did not answer"}`)
		return
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(out) > maxResponse {
		reply(w, http.StatusBadGateway, `{"error":"the chain API answer was unreadable or too large"}`)
		return
	}
	w.Header().Set("Content-Type", contentJSON)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", contentJSON)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}

// bucket is a token bucket with a clock the tests control.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newBucket(rate float64, burst int, now func() time.Time) *bucket {
	return &bucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now(), now: now}
}

func (b *bucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.tokens += t.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
