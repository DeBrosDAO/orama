package storageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

var (
	testSeed   = bytes.Repeat([]byte{7}, 32)
	testRepair = bytes.Repeat([]byte{9}, 32)
	testNonce  = bytes.Repeat([]byte{3}, 32)
)

// fakeProvider refuses a root until ready, then stores and serves it.
type fakeProvider struct {
	mu       sync.Mutex
	refusals int
	pieces   map[string][]byte
	corrupt  bool
	down     bool
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name := strings.TrimPrefix(r.URL.Path, "/pieces/")
	if p.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodPost:
		if p.refusals > 0 {
			p.refusals--
			http.Error(w, "not assigned", http.StatusForbidden)
			return
		}
		if r.Header.Get(rootHeader) != name {
			http.Error(w, "bad root", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		p.pieces[name] = body
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		body, ok := p.pieces[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if p.corrupt {
			body = append([]byte{0xff}, body[1:]...)
		}
		_, _ = w.Write(body)
	}
}

type world struct {
	slots     []storagefile.Slot
	providers []*fakeProvider
	servers   []*httptest.Server
	accepted  bool
	// live marks slots a provider has accepted: active and accepted.
	live  map[uint64]bool
	chain *httptest.Server
}

func newWorld(t *testing.T, plain []byte) *world {
	t.Helper()
	slots, err := storagefile.Prepare(testSeed, testRepair, testNonce, 3, plain)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{slots: slots}
	for range slots {
		p := &fakeProvider{refusals: 1, pieces: map[string][]byte{}}
		w.providers = append(w.providers, p)
		srv := httptest.NewServer(p)
		t.Cleanup(srv.Close)
		w.servers = append(w.servers, srv)
	}
	w.chain = httptest.NewServer(http.HandlerFunc(w.answer))
	t.Cleanup(w.chain.Close)
	return w
}

func field(num protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, num, protowire.BytesType), b)
}

func (w *world) answer(rw http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Query().Get("path"), `"`)
	data, _ := hex.DecodeString(strings.TrimPrefix(r.URL.Query().Get("data"), "0x"))
	var body []byte
	switch path {
	case queryDeal:
		deal := append(encodeUintField(dealIDField, 5), field(dealNonceField, testNonce)...)
		body = field(respBodyField, append(deal, encodeUintField(dealReplicasField, 3)...))
	case querySlot:
		var idx uint64
		_ = eachField(data, func(num protowire.Number, _ protowire.Type, v uint64, _ []byte) error {
			if num == 2 {
				idx = v
			}
			return nil
		})
		s := w.slots[idx]
		slot := append(encodeUintField(slotDealField, 5), encodeUintField(slotIndexField, idx)...)
		slot = append(slot, field(slotNodeField, []byte("n"+string(rune('0'+idx))))...)
		slot = append(slot, field(slotRootField, s.Root)...)
		status := uint64(slotAssigned)
		if w.live[idx] {
			status = slotActive
		}
		slot = append(slot, encodeUintField(slotStatusField, status)...)
		if w.accepted || w.live[idx] {
			slot = append(slot, encodeUintField(slotAcceptedField, 1)...)
		}
		body = field(respBodyField, slot)
	case queryNode:
		var id string
		_ = eachField(data, func(_ protowire.Number, _ protowire.Type, _ uint64, b []byte) error {
			id = string(b)
			return nil
		})
		n := int(id[1] - '0')
		node := append(field(nodeEndpointsField, []byte("/dns4/x/tcp/1")), field(nodeEndpointsField, []byte(w.servers[n].URL+"/"))...)
		body = field(respBodyField, node)
	}
	_ = json.NewEncoder(rw).Encode(map[string]any{"result": map[string]any{"response": map[string]any{
		"code": 0, "value": base64.StdEncoding.EncodeToString(body),
	}}})
}

func (w *world) client(t *testing.T) *Client {
	t.Helper()
	chain, err := NewChain(strings.Replace(w.chain.URL, "http://", "tcp://", 1))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(chain, http.DefaultClient, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.poll = 10 * time.Millisecond
	return c
}

func (w *world) slotBytes() [][]byte {
	var out [][]byte
	for _, s := range w.slots {
		out = append(out, s.Bytes)
	}
	return out
}

func TestPutAndGet_roundTripAcrossProviders(t *testing.T) {
	plain := bytes.Repeat([]byte("orama "), 700)
	w := newWorld(t, plain)
	c := w.client(t)
	if err := c.Put(context.Background(), 5, w.slotBytes()); err != nil {
		t.Fatal(err)
	}
	for i, p := range w.providers {
		if got := p.pieces[hex.EncodeToString(w.slots[i].Root)]; !bytes.Equal(got, w.slots[i].Bytes) {
			t.Fatalf("provider %d holds the wrong bytes", i)
		}
	}
	w.accepted = true
	w.providers[0].down = true
	got, err := c.Get(context.Background(), 5, testSeed, testRepair)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round trip changed the file")
	}
}

func TestPut_wrongFileUploadsNothing(t *testing.T) {
	w := newWorld(t, []byte("abc"))
	c := w.client(t)
	files := w.slotBytes()
	files[2] = append([]byte{}, files[1]...)
	err := c.Put(context.Background(), 5, files)
	if !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("got %v, want ErrRootMismatch", err)
	}
	for i, p := range w.providers {
		if len(p.pieces) != 0 {
			t.Fatalf("provider %d received a piece", i)
		}
	}
	if err := c.Put(context.Background(), 5, files[:2]); err == nil {
		t.Fatal("a short slot list was accepted")
	}
}

func TestGet_wrongSeedFailsClosed(t *testing.T) {
	w := newWorld(t, nil)
	c := w.client(t)
	if err := c.Put(context.Background(), 5, w.slotBytes()); err != nil {
		t.Fatal(err)
	}
	w.accepted = true
	_, err := c.Get(context.Background(), 5, bytes.Repeat([]byte{1}, 32), testRepair)
	if !errors.Is(err, storagefile.ErrNotForKey) {
		t.Fatalf("got %v, want ErrNotForKey", err)
	}
	got, err := c.Get(context.Background(), 5, testSeed, testRepair)
	if err != nil || len(got) != 0 {
		t.Fatalf("zero-length file: %v %d", err, len(got))
	}
}

func TestGet_corruptReplicaIsRefused(t *testing.T) {
	w := newWorld(t, []byte("payload"))
	c := w.client(t)
	if err := c.Put(context.Background(), 5, w.slotBytes()); err != nil {
		t.Fatal(err)
	}
	w.accepted = true
	for _, p := range w.providers {
		p.corrupt = true
	}
	_, err := c.Get(context.Background(), 5, testSeed, testRepair)
	if !errors.Is(err, ErrNoReplica) || !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestNewChain_refusesANonHTTPAddress(t *testing.T) {
	for _, addr := range []string{"", "ftp://x", "127.0.0.1:31001"} {
		if _, err := NewChain(addr); err == nil {
			t.Fatalf("%q was accepted", addr)
		}
	}
}

func TestParseABCIAnswer_mapsNotFound(t *testing.T) {
	_, err := parseABCIAnswer("/q", []byte(`{"result":{"response":{"codespace":"sdk","code":22,"log":"rpc error: code = NotFound"}}}`))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := parseABCIAnswer("/q", []byte(`{"error":{"message":"bad","data":"x"}}`)); err == nil {
		t.Fatal("an RPC error was accepted")
	}
	if _, err := parseABCIAnswer("/q", []byte(`nope`)); err == nil {
		t.Fatal("non-JSON was accepted")
	}
}
