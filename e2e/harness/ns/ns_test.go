package ns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/nsledger"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

func TestUniqueName_validAndUnique(t *testing.T) {
	a, b := UniqueName("TestX/sub"), UniqueName("TestX/sub")
	if a == b {
		t.Fatalf("two calls gave %s", a)
	}
	for _, n := range []string{a, b, UniqueName(strings.Repeat("long", 100))} {
		if err := ValidName(n); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(a, "e2e-") || a[:12] != b[:12] {
		t.Fatalf("names %s %s do not share the test hash", a, b)
	}
}

// TestUniqueName_recordsTheNameInThePackageLedger: every namespace a test
// names is tracked before it exists, so the runner can remove it whatever
// happens to the test's own cleanup.
func TestUniqueName_recordsTheNameInThePackageLedger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvEvidenceDir, dir)
	name := UniqueName("TestY")
	got, err := nsledger.Pending(filepath.Join(dir, nsledger.FileName))
	if err != nil || len(got) != 1 || got[0].Namespace != name {
		t.Fatalf("ledger %+v %v, want %s", got, err, name)
	}
}

func TestUniqueName_withoutAnEvidenceDirWritesNothing(t *testing.T) {
	t.Setenv(config.EnvEvidenceDir, "")
	UniqueName("TestZ")
}

func TestValidName_cases(t *testing.T) {
	for _, bad := range []string{"", "a", "-ab", "ab-", "Ab", "a_b", "ünï", strings.Repeat("a", 41)} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"ab", "e2e-1", strings.Repeat("a", 40)} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func TestCreationMode_parse(t *testing.T) {
	m, err := creationMode("namespace-creation: allowlist\nmax-namespaces-per-wallet: 10\n")
	if err != nil || m != "allowlist" {
		t.Fatalf("m=%q err=%v", m, err)
	}
	if _, err := creationMode("something else"); err == nil {
		t.Fatal("missing line accepted")
	}
}

// fakeNS serves the routes the namespace factory uses.
type fakeNS struct {
	mu       sync.Mutex
	statuses []string // returned in order, last one repeated
	queryOK  bool
	deleted  bool
	healthOK bool
	// failDeletes is how many deletes are refused with a retryable 503
	// first; deleteTakesEffect makes those refused deletes take effect anyway.
	failDeletes       int
	deleteTakesEffect bool
	deleteCalls       int
}

func (f *fakeNS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case PathStatus:
		if f.deleted {
			http.Error(w, `{"error":"cluster not found"}`, http.StatusNotFound)
			return
		}
		st := f.statuses[0]
		if len(f.statuses) > 1 {
			f.statuses = f.statuses[1:]
		}
		_ = json.NewEncoder(w).Encode(ClusterStatus{ClusterID: "c1", Status: st})
	case gw.PathChallenge:
		now := time.Now().UTC().Truncate(time.Second)
		m := &siw.Message{Chain: siw.Ethereum, Domain: "x.test", Address: "0x0000000000000000000000000000000000000001",
			URI: "https://x.test", ChainID: "1", Nonce: "abcdefgh1234", IssuedAt: now}
		text, _ := m.Render()
		_ = json.NewEncoder(w).Encode(map[string]string{"message": text})
	case gw.PathVerify:
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok", "refresh_token": "ref"})
	case PathQuery:
		if !f.queryOK {
			http.Error(w, `{"error":"no upstream"}`, http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"rows":[[1]]}`))
	case PathDelete:
		f.deleteCalls++
		if f.failDeletes > 0 {
			f.failDeletes--
			f.deleted, f.healthOK = f.deleted || f.deleteTakesEffect, f.healthOK && !f.deleteTakesEffect
			http.Error(w, `{"error":"retry shortly","retryable":true}`, http.StatusServiceUnavailable)
			return
		}
		f.deleted, f.healthOK = true, false
		_, _ = w.Write([]byte(`{"status":"deleted"}`))
	case PathHealth:
		if !f.healthOK {
			http.Error(w, "gone", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}
}

func fakeNamespace(t *testing.T, f *fakeNS) *Namespace {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := gw.NewWithTLS(srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := wallet.NewEVM()
	return &Namespace{Name: "e2e-x", ClusterID: "c1", URL: srv.URL, Client: c,
		Owner: &gw.User{Wallet: w, Client: c, Namespace: "e2e-x"}}
}

func poll(t *testing.T, fn func() (bool, error), attempts int) (bool, error) {
	t.Helper()
	var done bool
	var err error
	for i := 0; i < attempts && !done; i++ {
		done, err = fn()
	}
	return done, err
}

func TestReadyProbe_needsStatusAndRealRequest(t *testing.T) {
	f := &fakeNS{statuses: []string{"provisioning", "ready"}}
	n := fakeNamespace(t, f)
	probe := n.readyProbe(context.Background())
	done, err := probe()
	if done || err == nil || !strings.Contains(err.Error(), "status=provisioning") {
		t.Fatalf("provisioning: done=%v err=%v", done, err)
	}
	done, err = probe()
	if done || err == nil || !strings.Contains(err.Error(), "does not serve") {
		t.Fatalf("ready flag without a serving gateway counted as ready: done=%v err=%v", done, err)
	}
	f.mu.Lock()
	f.queryOK = true
	f.mu.Unlock()
	if done, err := poll(t, probe, 1); !done || err != nil {
		t.Fatalf("serving namespace not ready: %v", err)
	}
	if n.Owner.Session == nil || n.Owner.Token() != "tok" {
		t.Fatalf("owner session %+v", n.Owner.Session)
	}
}

func TestReadyProbe_failedStops(t *testing.T) {
	n := fakeNamespace(t, &fakeNS{statuses: []string{"failed"}})
	_, err := n.readyProbe(context.Background())()
	if err == nil || !strings.Contains(err.Error(), "provisioning failed") {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteViaUser_verifiesTeardown(t *testing.T) {
	f := &fakeNS{statuses: []string{"ready"}, healthOK: true}
	n := fakeNamespace(t, f)
	n.deleteViaUser(t)
	if !f.deleted {
		t.Fatal("namespace not deleted")
	}
}

// fastTeardown shortens the wait between teardown attempts for the test.
func fastTeardown(t *testing.T) {
	t.Helper()
	old := teardownInterval
	teardownInterval = time.Millisecond
	t.Cleanup(func() { teardownInterval = old })
}

// TestDeleteViaUser_retriesARefusedDelete: a delete the gateway refuses with
// a retryable answer is tried again; one attempt left the namespace on the
// cluster (stagenet 2026-10-01: "retry shortly", "retry the delete").
func TestDeleteViaUser_retriesARefusedDelete(t *testing.T) {
	fastTeardown(t)
	f := &fakeNS{statuses: []string{"ready"}, healthOK: true, failDeletes: 2}
	n := fakeNamespace(t, f)
	n.deleteViaUser(t)
	if !f.deleted || f.deleteCalls != 3 {
		t.Fatalf("deleted=%v after %d delete calls, want deleted after 3", f.deleted, f.deleteCalls)
	}
}

// TestDeleteViaUser_aDeleteThatTookEffectIsNotRepeated: the delete answered
// 503 but the cluster went away; the teardown notices and finishes.
func TestDeleteViaUser_aDeleteThatTookEffectIsNotRepeated(t *testing.T) {
	fastTeardown(t)
	f := &fakeNS{statuses: []string{"ready"}, healthOK: true, failDeletes: 5, deleteTakesEffect: true}
	n := fakeNamespace(t, f)
	n.deleteViaUser(t)
	if f.deleteCalls != 1 {
		t.Fatalf("%d delete calls, want 1: the namespace was already gone after the first", f.deleteCalls)
	}
}

func TestAllowCreator_modes(t *testing.T) {
	cases := map[string]string{"open": "", "allowlist": "creators add"}
	for mode, wantCall := range cases {
		t.Run(mode, func(t *testing.T) {
			cli, log := fakeCLI(t, mode)
			allowCreator(t, cli, "0xabc")
			if wantCall != "" && !strings.Contains(readFile(t, log), wantCall) {
				t.Fatalf("CLI calls %q lack %q", readFile(t, log), wantCall)
			}
		})
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// fakeCLI is an orama stand-in that prints the given creation mode and logs
// its arguments.
func fakeCLI(t *testing.T, mode string) (*oramacli.Runner, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\necho 'namespace-creation: " + mode + "'\n"
	bin := filepath.Join(dir, "orama")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &oramacli.Runner{Bin: bin, Home: dir, AgentSock: filepath.Join(dir, "a.sock"),
		RealHome: func() (string, error) { return "/nonexistent/home", nil }}, log
}
