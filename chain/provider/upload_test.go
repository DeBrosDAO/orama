package provider

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func rootHex(t *testing.T, data []byte) (string, []byte) {
	t.Helper()
	c, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(c.Root), c.Root
}

func TestUploadStoresOnlyAnAssignedPiece(t *testing.T) {
	data := []byte("upload-bytes")
	name, root := rootHex(t, data)
	bannedData := []byte("nope")
	bannedName, bannedRoot := rootHex(t, bannedData)
	s, err := Open(t.TempDir(), []string{bannedName}, func() (uint64, error) { return 1000, nil })
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 100, 20, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AcceptUploads(0, func(string) bool { return true }); err == nil {
		t.Fatal("zero max body was accepted")
	}
	assigned := map[string]bool{name: true}
	if err := h.AcceptUploads(100, func(cid string) bool { return assigned[cid] }); err != nil {
		t.Fatal(err)
	}

	otherName, otherRoot := rootHex(t, []byte("other"))
	if code := postPiece(h, otherName, []byte("other"), otherRoot); code != http.StatusForbidden {
		t.Fatalf("unassigned %d", code)
	}
	if code := postPiece(h, name, data, bytes.Repeat([]byte{1}, 32)); code != http.StatusBadRequest {
		t.Fatalf("a root that is not the name %d", code)
	}
	if code := postPiece(h, name, []byte("garbage"), mustRoot(t, []byte("garbage"))); code != http.StatusBadRequest {
		t.Fatalf("garbage under a pending name %d", code)
	}
	if s.Has(name) {
		t.Fatal("a mismatched piece was stored")
	}
	if code := postPiece(h, name, data, root); code != http.StatusNoContent {
		t.Fatalf("accept %d", code)
	}
	if code := postPiece(h, name, data, root); code != http.StatusNoContent {
		t.Fatalf("the same piece again %d", code)
	}

	assigned[bannedName] = true
	if code := postPiece(h, bannedName, bannedData, bannedRoot); code != http.StatusConflict {
		t.Fatalf("denylist %d", code)
	}
	if s.Has(bannedName) {
		t.Fatal("denylisted piece was stored")
	}

	if err := h.AcceptUploads(4, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	bigName, bigRoot := rootHex(t, []byte("0123456789"))
	if code := postPiece(h, bigName, []byte("0123456789"), bigRoot); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big %d", code)
	}
	if s.Has(bigName) {
		t.Fatal("oversized piece was stored")
	}
}

func TestIngest_neverReplacesAStoredPiece(t *testing.T) {
	s, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := []byte("first"), []byte("second")
	dec, err := s.Ingest("name", a, mustRoot(t, a))
	if err != nil || !dec.Accept {
		t.Fatalf("first: %+v %v", dec, err)
	}
	dec, err = s.Ingest("name", b, mustRoot(t, b))
	if err != nil || dec.Reason != ReasonExists {
		t.Fatalf("second: %+v %v", dec, err)
	}
	got, err := s.ReadRoot("name")
	if err != nil || !bytes.Equal(got, mustRoot(t, a)) {
		t.Fatal("the stored piece changed")
	}
}

func TestUploadDeclinesAFullDisk(t *testing.T) {
	data := []byte("0123456789")
	name, root := rootHex(t, data)
	s, err := Open(t.TempDir(), nil, func() (uint64, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewRetrieval(s, 100, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AcceptUploads(100, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if code := postPiece(h, name, data, root); code != http.StatusConflict {
		t.Fatalf("disk %d", code)
	}
	if s.Has(name) {
		t.Fatal("full disk stored a piece")
	}
}

func mustRoot(t *testing.T, data []byte) []byte {
	t.Helper()
	_, root := rootHex(t, data)
	return root
}

func postPiece(h *Retrieval, cid string, data, root []byte) int {
	req := httptest.NewRequest(http.MethodPost, "/pieces/"+cid, bytes.NewReader(data))
	req.RemoteAddr = "192.0.2.20:9"
	req.Header.Set("X-Piece-Root", hex.EncodeToString(root))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}
