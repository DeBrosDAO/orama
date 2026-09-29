package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Retrieval serves stored pieces over HTTP. It does not pin through Kubo
// itself: the Runner pins public deals, and POST /pins/<root> only fetches a
// piece by CID into the store (AcceptPins). A caller that passes a positive
// per-second limit, burst, and IP cap gets one token bucket per client
// address. When the address table is full, a new address is refused.
type Retrieval struct {
	store     *Store
	limit     rate.Limit
	burst     int
	maxIPs    int
	mu        sync.Mutex
	buckets   map[string]*bucket
	uploadMax int64
	assigned  func(string) bool
	catter    Catter
	pinnable  func(string) bool
	uploads   chan struct{}
	pins      chan struct{}
	pinning   map[string]struct{}
	now       func() time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

const (
	// bucketIdle is how long an address's bucket is kept after its last request.
	// A full table drops idle buckets before it refuses a new address.
	bucketIdle = 10 * time.Minute
	// MaxConcurrentUploads bounds the upload bodies held in memory at once.
	MaxConcurrentUploads = 2
	// MaxConcurrentPins bounds the fetches by CID in flight. They have their
	// own slots so a request naming a CID nobody serves cannot starve uploads.
	MaxConcurrentPins = 2
	// PinFetchTimeout bounds one fetch by CID. A piece a client just pinned on
	// its own node is found by Kubo well inside it.
	PinFetchTimeout = 30 * time.Second
	// ipv6BucketBits groups IPv6 clients by /64, the usual per-host allocation.
	ipv6BucketBits = 64
)

// NewRetrieval builds the piece HTTP handler. perSecond and burst are the
// per-address limit. maxIPs is how many addresses the table holds.
func NewRetrieval(store *Store, perSecond float64, burst, maxIPs int) (*Retrieval, error) {
	if store == nil {
		return nil, errors.New("retrieval store is nil")
	}
	if perSecond <= 0 || burst < 1 || maxIPs < 1 {
		return nil, errors.New("retrieval limit must be positive")
	}
	return &Retrieval{
		store:   store,
		limit:   rate.Limit(perSecond),
		burst:   burst,
		maxIPs:  maxIPs,
		buckets: make(map[string]*bucket),
		uploads: make(chan struct{}, MaxConcurrentUploads),
		pins:    make(chan struct{}, MaxConcurrentPins),
		pinning: make(map[string]struct{}),
		now:     time.Now,
	}, nil
}

// AcceptUploads lets POST /pieces/<cid> store a body. assigned reports
// whether this node was given that CID. maxBody is the most bytes read.
// Until this is called, POST is refused.
func (rt *Retrieval) AcceptUploads(maxBody int64, assigned func(string) bool) error {
	if maxBody < 1 || assigned == nil {
		return errors.New("upload limit must be positive")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.uploadMax = maxBody
	rt.assigned = assigned
	return nil
}

// Catter reads a CID's content from IPFS, at most max bytes.
type Catter interface {
	Cat(ctx context.Context, cid string, max int64) ([]byte, error)
}

// AcceptPins lets POST /pins/<hex piece root> with X-Piece-CID fetch the
// piece by that CID through cat (the public Kubo), check it against the
// assigned root, and store it. pinnable reports whether this node waits for
// that root in a slot of a public deal: a PRIVATE deal's piece is never
// fetched through the public Kubo. It needs AcceptUploads first, whose size
// limit it shares.
func (rt *Retrieval) AcceptPins(cat Catter, pinnable func(string) bool) error {
	if cat == nil || pinnable == nil {
		return errors.New("pin fetcher and filter are required")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.assigned == nil {
		return errors.New("AcceptPins needs AcceptUploads first")
	}
	rt.catter, rt.pinnable = cat, pinnable
	return nil
}

// ServeHTTP handles GET and HEAD /pieces/<cid>, including Range.
// POST stores a piece only after AcceptUploads, and only for an assigned CID.
func (rt *Retrieval) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, pinsPrefix) {
		rt.servePin(w, r)
		return
	}
	if r.Method == http.MethodPost {
		rt.serveUpload(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", rt.allowHeader())
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cid, err := pieceCID(r.URL.Path)
	if err != nil {
		http.Error(w, "bad piece path", http.StatusBadRequest)
		return
	}
	ip, err := clientIP(r.RemoteAddr)
	if err != nil {
		http.Error(w, "missing client address", http.StatusBadRequest)
		return
	}
	if !rt.allow(ip) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	f, err := os.Open(rt.store.piecePath(cid))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "stat failed", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, cid, info.ModTime(), f)
}

func (rt *Retrieval) serveUpload(w http.ResponseWriter, r *http.Request) {
	maxBody, assigned := rt.uploadConfig()
	if assigned == nil {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cid, err := pieceCID(r.URL.Path)
	if err != nil {
		http.Error(w, "bad piece path", http.StatusBadRequest)
		return
	}
	ip, err := clientIP(r.RemoteAddr)
	if err != nil {
		http.Error(w, "missing client address", http.StatusBadRequest)
		return
	}
	if !rt.allow(ip) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if !assigned(cid) {
		http.Error(w, "not assigned", http.StatusForbidden)
		return
	}
	select {
	case rt.uploads <- struct{}{}:
		defer func() { <-rt.uploads }()
	default:
		http.Error(w, "too many uploads in progress", http.StatusServiceUnavailable)
		return
	}
	root, err := hex.DecodeString(r.Header.Get("X-Piece-Root"))
	if err != nil || len(root) != 32 || hex.EncodeToString(root) != cid {
		// The name is the assigned root. A different claimed root would let
		// anyone store unrelated bytes under a pending slot's name.
		http.Error(w, "piece root must be the name it is uploaded under", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "piece too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	decision, err := rt.store.Ingest(cid, data, root)
	if err != nil {
		http.Error(w, "ingest failed", http.StatusInternalServerError)
		return
	}
	if !decision.Accept {
		http.Error(w, decision.Reason, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pinsPrefix is the path POST /pins/<hex piece root> is served under.
const pinsPrefix = "/pins/"

// servePin fetches an assigned piece by the CID the client names. The bytes
// are read through the public Kubo (bounded by the upload limit), must hash to
// the assigned root, and are then stored like an upload. The CID is recorded
// so a public deal pins the piece under it.
func (rt *Retrieval) servePin(w http.ResponseWriter, r *http.Request) {
	maxBody, _ := rt.uploadConfig()
	rt.mu.Lock()
	cat, pinnable := rt.catter, rt.pinnable
	rt.mu.Unlock()
	if cat == nil {
		w.Header().Set("Allow", rt.allowHeader())
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, pinsPrefix)
	root, err := hex.DecodeString(name)
	if err != nil || len(root) != 32 || hex.EncodeToString(root) != name {
		http.Error(w, "bad pin path", http.StatusBadRequest)
		return
	}
	ip, err := clientIP(r.RemoteAddr)
	if err != nil {
		http.Error(w, "missing client address", http.StatusBadRequest)
		return
	}
	if !rt.allow(ip) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	if !pinnable(name) {
		http.Error(w, "not assigned to a public deal", http.StatusForbidden)
		return
	}
	cid := r.Header.Get("X-Piece-CID")
	if !ValidIPFSCID(cid) {
		http.Error(w, "X-Piece-CID must be a CIDv0 or a base32 CIDv1", http.StatusBadRequest)
		return
	}
	if rt.store.Denied(cid) {
		http.Error(w, ReasonDenylist, http.StatusConflict)
		return
	}
	if pins, err := rt.store.IPFSPins(name); err == nil && slices.ContainsFunc(pins, func(p ipfsPin) bool { return p.CID == cid }) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	select {
	case rt.pins <- struct{}{}:
		defer func() { <-rt.pins }()
	default:
		http.Error(w, "too many fetches in progress", http.StatusServiceUnavailable)
		return
	}
	if !rt.startPin(name) {
		http.Error(w, "a fetch of this piece is already in progress", http.StatusTooManyRequests)
		return
	}
	defer rt.endPin(name)
	ctx, cancel := context.WithTimeout(r.Context(), PinFetchTimeout)
	defer cancel()
	data, err := cat.Cat(ctx, cid, maxBody)
	if err != nil {
		http.Error(w, "fetch by CID failed", http.StatusBadGateway)
		return
	}
	decision, err := rt.store.Ingest(name, data, root)
	if err != nil {
		http.Error(w, "ingest failed", http.StatusInternalServerError)
		return
	}
	if !decision.Accept {
		http.Error(w, decision.Reason, http.StatusConflict)
		return
	}
	if err := rt.store.AddIPFS(name, cid); err != nil {
		http.Error(w, "record CID failed", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startPin marks a fetch of name in flight. It reports false when one already is.
func (rt *Retrieval) startPin(name string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, busy := rt.pinning[name]; busy {
		return false
	}
	rt.pinning[name] = struct{}{}
	return true
}

func (rt *Retrieval) endPin(name string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.pinning, name)
}

func (rt *Retrieval) uploadConfig() (int64, func(string) bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.uploadMax, rt.assigned
}

func (rt *Retrieval) allowHeader() string {
	if _, assigned := rt.uploadConfig(); assigned != nil {
		return "GET, HEAD, POST"
	}
	return "GET, HEAD"
}

func (rt *Retrieval) allow(ip string) bool {
	key := bucketKey(ip)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	now := rt.now()
	b, ok := rt.buckets[key]
	if !ok {
		if len(rt.buckets) >= rt.maxIPs {
			rt.dropIdle(now)
		}
		if len(rt.buckets) >= rt.maxIPs {
			return false
		}
		b = &bucket{lim: rate.NewLimiter(rt.limit, rt.burst)}
		rt.buckets[key] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// dropIdle removes buckets unused for bucketIdle. The caller holds rt.mu.
func (rt *Retrieval) dropIdle(now time.Time) {
	for key, b := range rt.buckets {
		if now.Sub(b.seen) >= bucketIdle {
			delete(rt.buckets, key)
		}
	}
}

// bucketKey is the address itself for IPv4 and its /64 for IPv6, so one
// host cannot fill the table by rotating through its own prefix.
func bucketKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(ipv6BucketBits, 128)).String() + "/64"
}

func pieceCID(urlPath string) (string, error) {
	const prefix = "/pieces/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", errors.New("path")
	}
	cid := strings.TrimPrefix(urlPath, prefix)
	if cid == "" || strings.Contains(cid, "/") {
		return "", errors.New("path")
	}
	if err := validCID(cid); err != nil {
		return "", err
	}
	return cid, nil
}

func clientIP(remote string) (string, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if net.ParseIP(host) == nil {
		return "", errors.New("address")
	}
	return host, nil
}
