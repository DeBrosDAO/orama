package orama

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// fakeStore is the gateway's /v1/internal/tls-store as far as the module can
// tell: it checks the stamp and keeps values and locks in memory.
type fakeStore struct {
	t      *testing.T
	macKey []byte

	mu       sync.Mutex
	values   map[string]string
	locks    map[string]string
	status   int // when non-zero, every call is answered with it
	failNext int // this many calls are answered 503 first
}

func (f *fakeStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	stamp, sig, _ := strings.Cut(r.Header.Get(macV2Header), ".")
	ts, _ := strconv.ParseInt(stamp, 10, 64)
	want := coordinationV2MAC(f.macKey, r.Method, storeAudience, r.URL.Path, r.URL.RawQuery, body, r.Header.Get(nonceHeader), ts)
	if sig != want {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	if f.failNext > 0 {
		f.failNext--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var req storeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Errorf("undecodable request: %v", err)
	}
	yes, no := true, false
	var resp storeResponse
	switch req.Op {
	case "store":
		if !strings.HasPrefix(req.Value, sealedPrefix) {
			f.t.Errorf("value for %s sent unsealed", req.Key)
		}
		f.values[req.Key] = req.Value
	case "load":
		v, ok := f.values[req.Key]
		resp.Exists, resp.Value = &no, ""
		if ok {
			resp.Exists, resp.Value = &yes, v
		}
	case "stat":
		resp.Exists = &no
		if v, ok := f.values[req.Key]; ok {
			resp.Exists, resp.Terminal, resp.Size = &yes, true, int64(len(v))
		}
	case "delete":
		delete(f.values, req.Key)
	case "list":
		for k := range f.values {
			if strings.HasPrefix(k, req.Key+"/") {
				resp.Keys = append(resp.Keys, k)
			}
		}
		sort.Strings(resp.Keys)
	case "lock":
		if _, held := f.locks[req.Key]; !held {
			f.locks[req.Key] = req.Holder
			resp.Acquired = true
		}
	case "renew", "unlock":
		if f.locks[req.Key] != req.Holder {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if req.Op == "unlock" {
			delete(f.locks, req.Key)
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func newTestStorage(t *testing.T) (*Storage, *fakeStore) {
	t.Helper()
	mac, _ := vectorStoreKeys(t)
	f := &fakeStore{t: t, macKey: mac, values: map[string]string{}, locks: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	keyFile := filepath.Join(t.TempDir(), "orama-tls-store.key")
	if err := os.WriteFile(keyFile, []byte(vectorMasterHex+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Storage{Endpoint: srv.URL + "/v1/internal/tls-store", KeyFile: keyFile}
	if err := s.setup(); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestStorage_storeLoadDelete(t *testing.T) {
	s, f := newTestStorage(t)
	ctx := context.Background()
	if err := s.Store(ctx, "certificates/ca/a/a.key", []byte("PRIVATE KEY")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.values["certificates/ca/a/a.key"], "PRIVATE") {
		t.Fatal("the gateway received the key in the clear")
	}
	got, err := s.Load(ctx, "certificates/ca/a/a.key")
	if err != nil || string(got) != "PRIVATE KEY" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if !s.Exists(ctx, "certificates/ca/a/a.key") {
		t.Error("Exists is false for a stored key")
	}
	if err := s.Delete(ctx, "certificates/ca/a/a.key"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "certificates/ca/a/a.key"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load after Delete: %v, want fs.ErrNotExist", err)
	}
}

// A refused or failed call is an error, never "not there": CertMagic answers
// "not there" by obtaining a certificate, which spends the CA's rate limit.
func TestStorage_aRefusalIsNeverAbsence(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusBadRequest} {
		s, f := newTestStorage(t)
		f.status = status
		_, err := s.Load(context.Background(), "certificates/ca/a/a.crt")
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("status %d: Load = %v, want an error that is not fs.ErrNotExist", status, err)
		}
		if _, err := s.Stat(context.Background(), "certificates/ca/a/a.crt"); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("status %d: Stat = %v, want an error that is not fs.ErrNotExist", status, err)
		}
	}
}

func TestStorage_aWrongKeyIsRefused(t *testing.T) {
	s, _ := newTestStorage(t)
	s.macKey = make([]byte, keyLen)
	_, err := s.Load(context.Background(), "a")
	if err == nil || errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("Load with the wrong key: %v", err)
	}
}

func TestStorage_listAndStat(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()
	if _, err := s.List(ctx, "certificates", true); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("List of an empty store: %v, want fs.ErrNotExist", err)
	}
	for _, k := range []string{"certificates/ca/b.crt", "certificates/ca/a.crt"} {
		if err := s.Store(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.List(ctx, "certificates", true)
	if err != nil || strings.Join(keys, ",") != "certificates/ca/a.crt,certificates/ca/b.crt" {
		t.Fatalf("List = %v, %v", keys, err)
	}
	info, err := s.Stat(ctx, "certificates/ca/a.crt")
	if err != nil || !info.IsTerminal || info.Key != "certificates/ca/a.crt" {
		t.Fatalf("Stat = %+v, %v", info, err)
	}
	if _, err := s.Stat(ctx, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat of a missing key: %v", err)
	}
}

// Two nodes' Caddy share one store: while one holds a lock the other waits.
func TestStorage_lockIsExclusiveAcrossInstances(t *testing.T) {
	a, f := newTestStorage(t)
	b := &Storage{Endpoint: a.Endpoint, KeyFile: a.KeyFile}
	if err := b.setup(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Lock(ctx, "issue_cert_x"); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := b.Lock(waitCtx, "issue_cert_x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second instance took a held lock: %v", err)
	}
	if err := a.RenewLockLease(ctx, "issue_cert_x", time.Hour); err != nil {
		t.Fatalf("RenewLockLease: %v", err)
	}
	if err := a.Unlock(ctx, "issue_cert_x"); err != nil {
		t.Fatal(err)
	}
	if err := b.Lock(ctx, "issue_cert_x"); err != nil {
		t.Fatalf("the lock was not free after Unlock: %v", err)
	}
	if holder := f.locks["issue_cert_x"]; len(holder) != 32 {
		t.Errorf("holder id %q is not 32 hex characters", holder)
	}
	if _, err := hex.DecodeString(f.locks["issue_cert_x"]); err != nil {
		t.Errorf("holder id is not hex: %v", err)
	}
}

func TestStorage_unlockOfALockNotHeld(t *testing.T) {
	s, _ := newTestStorage(t)
	if err := s.Unlock(context.Background(), "never"); !errors.Is(err, errLockNotHeld) {
		t.Fatalf("Unlock of a lock never taken: %v", err)
	}
	if err := s.RenewLockLease(context.Background(), "never", time.Minute); !errors.Is(err, errLockNotHeld) {
		t.Fatalf("RenewLockLease of a lock never taken: %v", err)
	}
}

func TestStorage_setupRefusesAnIncompleteConfiguration(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("not hex"), 0o600)
	short := filepath.Join(dir, "short.key")
	os.WriteFile(short, []byte("0011"), 0o600)
	for name, s := range map[string]*Storage{
		"no endpoint":    {KeyFile: short},
		"no key file":    {Endpoint: "http://localhost/x"},
		"missing file":   {Endpoint: "http://localhost/x", KeyFile: filepath.Join(dir, "none")},
		"not hex":        {Endpoint: "http://localhost/x", KeyFile: bad},
		"wrong key size": {Endpoint: "http://localhost/x", KeyFile: short},
	} {
		if err := s.setup(); err == nil {
			t.Errorf("%s: setup accepted it", name)
		}
	}
}

func TestStorage_unmarshalCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`orama {
		endpoint http://localhost:6001/v1/internal/tls-store
		key_file /etc/caddy/orama-tls-store.key
	}`)
	var s Storage
	if err := s.UnmarshalCaddyfile(d); err != nil {
		t.Fatal(err)
	}
	if s.Endpoint != "http://localhost:6001/v1/internal/tls-store" || s.KeyFile != "/etc/caddy/orama-tls-store.key" {
		t.Fatalf("parsed %+v", s)
	}
	if err := new(Storage).UnmarshalCaddyfile(caddyfile.NewTestDispenser("orama {\n\tbogus x\n}")); err == nil {
		t.Error("an unknown option was accepted")
	}
	if err := new(Storage).UnmarshalCaddyfile(caddyfile.NewTestDispenser("orama {\n\tendpoint\n}")); err == nil {
		t.Error("an option without a value was accepted")
	}
}

// One failed call does not make Exists answer "absent".
func TestStorage_existsRidesOutOneFailure(t *testing.T) {
	s, f := newTestStorage(t)
	ctx := context.Background()
	if err := s.Store(ctx, "certificates/ca/a.crt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	f.failNext = 1
	if !s.Exists(ctx, "certificates/ca/a.crt") {
		t.Fatal("Exists answered false after one failed call")
	}
}

// Provisioning waits while the store is not open yet (503, the gateway still
// importing) and returns once it answers.
func TestStorage_waitsForTheStoreToOpen(t *testing.T) {
	s, f := newTestStorage(t)
	f.failNext = 3
	if err := s.waitForStore(context.Background(), 5*time.Second, 10*time.Millisecond); err != nil {
		t.Fatalf("waitForStore: %v", err)
	}
	if f.failNext != 0 {
		t.Errorf("returned before the store answered (%d refusals left)", f.failNext)
	}
}

// A store that never opens fails provisioning, so Caddy exits and restarts
// rather than run without its certificates.
func TestStorage_waitForStoreGivesUp(t *testing.T) {
	s, f := newTestStorage(t)
	f.status = http.StatusServiceUnavailable
	if err := s.waitForStore(context.Background(), 50*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("waitForStore succeeded against a store that never opened")
	}
}

// A refusal of this node's key is final: no waiting.
func TestStorage_waitForStoreStopsOnARefusal(t *testing.T) {
	s, f := newTestStorage(t)
	f.status = http.StatusNotFound
	start := time.Now()
	err := s.waitForStore(context.Background(), 5*time.Second, 10*time.Millisecond)
	if !errors.Is(err, errRefused) {
		t.Fatalf("waitForStore = %v, want a refusal", err)
	}
	if time.Since(start) > time.Second {
		t.Error("waited on a refusal")
	}
}

// Nothing listening (the gateway still starting) is waited on, not refused.
func TestStorage_waitsWhileNothingListens(t *testing.T) {
	s, _ := newTestStorage(t)
	s.Endpoint = "http://127.0.0.1:1/v1/internal/tls-store"
	start := time.Now()
	err := s.waitForStore(context.Background(), 100*time.Millisecond, 20*time.Millisecond)
	if err == nil || errors.Is(err, errRefused) {
		t.Fatalf("waitForStore = %v, want a timeout", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("gave up before the wait ran out")
	}
}

// A listener that answers 200 but is not the store does not count as open.
func TestStorage_waitForStoreWantsAStatAnswer(t *testing.T) {
	s, _ := newTestStorage(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	defer srv.Close()
	s.Endpoint = srv.URL
	if err := s.waitForStore(context.Background(), 50*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("a 200 with no stat answer was taken for the store")
	}
}

// A load that fails once is tried again: CertMagic never retries the first
// load of a name, so one blip would leave it unmanaged.
func TestStorage_loadRidesOutATransientFailure(t *testing.T) {
	s, f := newTestStorage(t)
	ctx := context.Background()
	if err := s.Store(ctx, "certificates/ca/a.crt", []byte("CERT")); err != nil {
		t.Fatal(err)
	}
	f.failNext = 1
	got, err := s.Load(ctx, "certificates/ca/a.crt")
	if err != nil || string(got) != "CERT" {
		t.Fatalf("Load after one failure = %q, %v", got, err)
	}
}
