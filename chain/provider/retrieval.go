package provider

import (
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// Retrieval serves stored pieces over HTTP. It does not pin through Kubo. A caller that passes a positive
// per-second limit, burst, and IP cap gets one token bucket per client
// address. When the address table is full, a new address is refused.
type Retrieval struct {
	store     *Store
	limit     rate.Limit
	burst     int
	maxIPs    int
	mu        sync.Mutex
	buckets   map[string]*rate.Limiter
	uploadMax int64
	assigned  func(string) bool
}

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
		buckets: make(map[string]*rate.Limiter),
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

// ServeHTTP handles GET and HEAD /pieces/<cid>, including Range.
// POST stores a piece only after AcceptUploads, and only for an assigned CID.
func (rt *Retrieval) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	root, err := hex.DecodeString(r.Header.Get("X-Piece-Root"))
	if err != nil || len(root) != 32 {
		http.Error(w, "bad piece root", http.StatusBadRequest)
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
	rt.mu.Lock()
	defer rt.mu.Unlock()
	lim, ok := rt.buckets[ip]
	if !ok {
		if len(rt.buckets) >= rt.maxIPs {
			return false
		}
		lim = rate.NewLimiter(rt.limit, rt.burst)
		rt.buckets[ip] = lim
	}
	return lim.Allow()
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
