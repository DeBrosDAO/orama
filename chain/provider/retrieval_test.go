package provider

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func TestRetrievalServesARangeAndRefusesTheRest(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte{3}, 64)
	c, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest("piece-a", data, c.Root); err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 100, 8, 4)
	if err != nil {
		t.Fatal(err)
	}

	full := httptest.NewRequest(http.MethodGet, "/pieces/piece-a", nil)
	full.RemoteAddr = "192.0.2.1:9"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, full)
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), data) {
		t.Fatalf("full %d %q", rr.Code, rr.Body.Bytes())
	}

	head := httptest.NewRequest(http.MethodHead, "/pieces/piece-a", nil)
	head.RemoteAddr = "192.0.2.6:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, head)
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Fatalf("head %d body %d", rr.Code, rr.Body.Len())
	}

	part := httptest.NewRequest(http.MethodGet, "/pieces/piece-a", nil)
	part.RemoteAddr = "192.0.2.2:9"
	part.Header.Set("Range", "bytes=0-3")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, part)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "\x03\x03\x03\x03" {
		t.Fatalf("range %d %q", rr.Code, rr.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/pieces/missing", nil)
	missing.RemoteAddr = "192.0.2.3:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, missing)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing %d", rr.Code)
	}

	bad := httptest.NewRequest(http.MethodGet, "/pieces/../outside", nil)
	bad.RemoteAddr = "192.0.2.4:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, bad)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("escape %d", rr.Code)
	}

	post := httptest.NewRequest(http.MethodPost, "/pieces/piece-a", nil)
	post.RemoteAddr = "192.0.2.5:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, post)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("post %d allow %q", rr.Code, rr.Header().Get("Allow"))
	}
}

func TestRetrievalLimitsPerAddressAndRefusesAFullTable(t *testing.T) {
	dir := t.TempDir()
	data := []byte("abcdefghij")
	c, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest("piece-a", data, c.Root); err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 0.001, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRequest(http.MethodGet, "/pieces/piece-a", nil)
	first.RemoteAddr = "192.0.2.8:9"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, first)
	if rr.Code != http.StatusOK {
		t.Fatalf("first %d", rr.Code)
	}
	again := httptest.NewRequest(http.MethodGet, "/pieces/piece-a", nil)
	again.RemoteAddr = "192.0.2.8:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, again)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("burst %d", rr.Code)
	}
	other := httptest.NewRequest(http.MethodGet, "/pieces/piece-a", nil)
	other.RemoteAddr = "192.0.2.9:9"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, other)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("table %d", rr.Code)
	}
	if _, err := NewRetrieval(nil, 1, 1, 1); err == nil {
		t.Fatal("nil store was accepted")
	}
	if _, err := NewRetrieval(s, 0, 1, 1); err == nil {
		t.Fatal("zero rate was accepted")
	}
}

func TestRetrieval_dropsIdleBucketsAndGroupsIPv6By64(t *testing.T) {
	s, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_700_000_000, 0)
	h.now = func() time.Time { return clock }
	if !h.allow("192.0.2.1") {
		t.Fatal("first address refused")
	}
	if h.allow("192.0.2.2") {
		t.Fatal("a full table let a second address in while the first is active")
	}
	clock = clock.Add(bucketIdle)
	if !h.allow("192.0.2.2") {
		t.Fatal("an idle bucket was not dropped for a new address")
	}

	h6, err := NewRetrieval(s, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !h6.allow("2001:db8::1") {
		t.Fatal("first v6 address refused")
	}
	if h6.allow("2001:db8::2") {
		t.Fatal("the same /64 got a second token")
	}
	if len(h6.buckets) != 1 {
		t.Fatalf("one /64 took %d buckets", len(h6.buckets))
	}
}

func TestUpload_refusesPastTheConcurrencyCap(t *testing.T) {
	s, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 100, 100, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AcceptUploads(100, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxConcurrentUploads; i++ {
		h.uploads <- struct{}{}
	}
	data := []byte("x")
	name := hex.EncodeToString(mustRoot(t, data))
	if code := postPiece(h, name, data, mustRoot(t, data)); code != http.StatusServiceUnavailable {
		t.Fatalf("busy %d", code)
	}
	if s.Has(name) {
		t.Fatal("a refused upload stored a piece")
	}
}
