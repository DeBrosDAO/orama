package provider

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func TestUploadStoresOnlyAnAssignedPiece(t *testing.T) {
	dir := t.TempDir()
	data := []byte("upload-bytes")
	committed, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, []string{"banned"}, func() (uint64, error) { return 1000, nil })
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
	assigned := map[string]bool{"piece-a": true}
	if err := h.AcceptUploads(100, func(cid string) bool { return assigned[cid] }); err != nil {
		t.Fatal(err)
	}

	if code := postPiece(h, "other", data, committed.Root); code != http.StatusForbidden {
		t.Fatalf("unassigned %d", code)
	}
	if s.Has("other") {
		t.Fatal("unassigned piece was stored")
	}

	wrong := bytes.Repeat([]byte{1}, 32)
	if code := postPiece(h, "piece-a", data, wrong); code != http.StatusConflict {
		t.Fatalf("bad root %d", code)
	}
	if s.Has("piece-a") {
		t.Fatal("mismatched piece was stored")
	}

	if code := postPiece(h, "piece-a", data, committed.Root); code != http.StatusNoContent {
		t.Fatalf("accept %d", code)
	}
	if !s.Has("piece-a") {
		t.Fatal("assigned piece was not stored")
	}

	bannedData := []byte("nope")
	banned, err := piece.Commit(bannedData)
	if err != nil {
		t.Fatal(err)
	}
	assigned["banned"] = true
	if code := postPiece(h, "banned", bannedData, banned.Root); code != http.StatusConflict {
		t.Fatalf("denylist %d", code)
	}
	if s.Has("banned") {
		t.Fatal("denylisted piece was stored")
	}

	if err := h.AcceptUploads(4, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if code := postPiece(h, "big", data, committed.Root); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big %d", code)
	}
	if s.Has("big") {
		t.Fatal("oversized piece was stored")
	}
}

func TestUploadDeclinesAFullDisk(t *testing.T) {
	dir := t.TempDir()
	data := []byte("0123456789")
	committed, err := piece.Commit(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, nil, func() (uint64, error) { return 1, nil })
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
	if code := postPiece(h, "piece-a", data, committed.Root); code != http.StatusConflict {
		t.Fatalf("disk %d", code)
	}
	if s.Has("piece-a") {
		t.Fatal("full disk stored a piece")
	}
}

func postPiece(h *Retrieval, cid string, data, root []byte) int {
	req := httptest.NewRequest(http.MethodPost, "/pieces/"+cid, bytes.NewReader(data))
	req.RemoteAddr = "192.0.2.20:9"
	req.Header.Set("X-Piece-Root", hex.EncodeToString(root))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}
