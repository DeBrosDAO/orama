package report

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeVaultStatus_answeringVaultIsResponsive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"guardians":3,"healthy":3,"threshold":2,"write_quorum":3}`))
	}))
	defer srv.Close()
	r := &VaultReport{}
	probeVaultStatus(context.Background(), srv.URL, r)
	if !r.Responsive || r.Guardians != 3 || r.Healthy != 3 || r.Threshold != 2 || r.WriteQuorum != 3 {
		t.Fatalf("report %+v, want responsive with 3/3 guardians", r)
	}
}

func TestProbeVaultStatus_unansweredOrGarbageIsNotResponsive(t *testing.T) {
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("not json")) }))
	defer garbage.Close()
	for name, url := range map[string]string{"garbage": garbage.URL, "nothing listening": "http://127.0.0.1:1"} {
		r := &VaultReport{}
		probeVaultStatus(context.Background(), url, r)
		if r.Responsive {
			t.Errorf("%s: reported responsive", name)
		}
	}
}

// TestCollectVault_statusHasItsOwnBudget: the status probe shared one budget
// with four shell commands run before it; on a loaded node they spent it and a
// vault that answered read as unresponsive.
func TestCollectVault_statusHasItsOwnBudget(t *testing.T) {
	if vaultStatusTimeout <= 0 || vaultDetailsTimeout <= 0 {
		t.Fatal("each part of the vault report needs a budget of its own")
	}
	ctx, cancel := context.WithTimeout(context.Background(), vaultStatusTimeout)
	defer cancel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(`{"guardians":3,"healthy":3}`))
	}))
	defer slow.Close()
	r := &VaultReport{}
	probeVaultStatus(ctx, slow.URL, r)
	if !r.Responsive {
		t.Fatal("a vault answering inside the status budget read as unresponsive")
	}
}
