package provider

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

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
