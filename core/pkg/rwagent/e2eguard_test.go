package rwagent

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withRealHome points the real-home lookup at home for one test.
func withRealHome(t *testing.T, home string) {
	t.Helper()
	prev := realHomeDir
	realHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { realHomeDir = prev })
}

func TestE2EGuard_emptySocketRefusedUnderE2E(t *testing.T) {
	t.Setenv(E2EEnvVar, "1")
	withRealHome(t, t.TempDir())
	_, err := New("").Status(context.Background())
	if !errors.Is(err, ErrE2EDefaultSocket) {
		t.Fatalf("Status with the default socket under %s=1: %v", E2EEnvVar, err)
	}
}

func TestE2EGuard_socketInsideRealWalletRefused(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, ".rootwallet")
	if err := os.MkdirAll(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(E2EEnvVar, "1")
	withRealHome(t, home)
	_, err := New(filepath.Join(wallet, "agent.sock")).Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "real RootWallet directory") {
		t.Fatalf("Status through the real wallet's socket: %v", err)
	}
}

func TestE2EGuard_symlinkIntoRealWalletRefused(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, ".rootwallet")
	if err := os.MkdirAll(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "looks-isolated")
	if err := os.Symlink(wallet, link); err != nil {
		t.Fatal(err)
	}
	if err := checkE2ESocket(filepath.Join(link, "agent.sock"), home); err == nil {
		t.Fatal("a socket reached through a symlink into the real wallet was allowed")
	}
}

func TestE2EGuard_isolatedSocketAllowed(t *testing.T) {
	home := t.TempDir()
	if err := checkE2ESocket(filepath.Join(t.TempDir(), "a.sock"), home); err != nil {
		t.Fatalf("an isolated socket: %v", err)
	}
	// A sibling whose name only starts like the wallet dir is not inside it.
	if err := checkE2ESocket(filepath.Join(home, ".rootwallet-e2e", "a.sock"), home); err != nil {
		t.Fatalf("a sibling of the wallet dir: %v", err)
	}
}

func TestE2EGuard_explicitSock(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", jsonHandler(200, apiResponse[StatusResponse]{OK: true, Data: StatusResponse{Version: "e2e"}}))
	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()
	t.Setenv(E2EEnvVar, "1")
	withRealHome(t, t.TempDir())
	st, err := New(sock).Status(context.Background())
	if err != nil || st.Version != "e2e" {
		t.Fatalf("Status through the test agent: %v", err)
	}
}

func TestE2EGuard_offWithoutTheFlag(t *testing.T) {
	t.Setenv(E2EEnvVar, "")
	if err := e2eGuard(""); err != nil {
		t.Fatalf("guard outside an e2e run: %v", err)
	}
	t.Setenv(E2EEnvVar, "0")
	if err := e2eGuard(""); err != nil {
		t.Fatalf("guard with %s=0: %v", E2EEnvVar, err)
	}
}

func TestE2EGuard_realHomeLookupFailure(t *testing.T) {
	t.Setenv(E2EEnvVar, "1")
	prev := realHomeDir
	realHomeDir = func() (string, error) { return "", errors.New("no passwd entry") }
	defer func() { realHomeDir = prev }()
	if err := e2eGuard("/tmp/x.sock"); err == nil || !strings.Contains(err.Error(), "no passwd entry") {
		t.Fatalf("guard with no real home: %v", err)
	}
}

func TestCheckE2ESocket_caseChangedPathRefused(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, walletDirName)
	if err := os.MkdirAll(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(home, strings.ToUpper(walletDirName))
	if _, err := os.Stat(upper); err != nil {
		t.Skipf("the temp file system is case-sensitive (%v): a case-changed path names another directory", err)
	}
	if err := checkE2ESocket(filepath.Join(upper, "agent.sock"), home); err == nil {
		t.Fatal("a case-changed path into the real wallet was allowed")
	}
}

func TestCheckE2ESocket_hardLinkToRealSocketRefused(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, walletDirName)
	if err := os.MkdirAll(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(wallet, DefaultSocketName)
	if err := os.WriteFile(real, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "a.sock")
	if err := os.Link(real, link); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	err := checkE2ESocket(link, home)
	if err == nil || !strings.Contains(err.Error(), "real RootWallet agent socket") {
		t.Fatalf("a hard link to the real socket: %v", err)
	}
}

func TestCheckE2ESocket_noRealWalletAllowsAnySocket(t *testing.T) {
	if err := checkE2ESocket(filepath.Join(t.TempDir(), "a.sock"), filepath.Join(t.TempDir(), "absent-home")); err != nil {
		t.Fatalf("no wallet directory at all: %v", err)
	}
}

func TestRealHomeDir_ignoresHOME(t *testing.T) {
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	home, err := realHomeDir()
	if err != nil {
		t.Skipf("this account has no user database entry: %v", err)
	}
	if home == fake {
		t.Fatalf("the real home came from $HOME (%s)", fake)
	}
}

func TestE2EGuard_everyRefusalWrapsErrE2EGuard(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, walletDirName)
	if err := os.MkdirAll(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ErrE2EDefaultSocket, ErrE2EGuard) {
		t.Fatal("ErrE2EDefaultSocket does not wrap ErrE2EGuard")
	}
	if err := checkE2ESocket(filepath.Join(wallet, "agent.sock"), home); !errors.Is(err, ErrE2EGuard) {
		t.Fatalf("a socket inside the real wallet: %v", err)
	}
}

// TestE2EGuard_rerunAtDial: a socket path that was safe when the client was
// made, then swapped for a link into the real wallet, is refused at the
// dial: the guard is not only checked once in New.
func TestE2EGuard_rerunAtDial(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", jsonHandler(200, apiResponse[StatusResponse]{OK: true, Data: StatusResponse{Version: "real"}}))
	realSock, cleanup := startMockAgent(t, mux)
	defer cleanup()
	home := t.TempDir()
	if err := os.Symlink(filepath.Dir(realSock), filepath.Join(home, ".rootwallet")); err != nil {
		t.Fatal(err)
	}
	safe, err := os.MkdirTemp(shortSocketBase, "rws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(safe) })
	t.Setenv(E2EEnvVar, "1")
	withRealHome(t, home)
	c := New(filepath.Join(safe, filepath.Base(realSock)))
	if err := os.Remove(safe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(realSock), safe); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrE2EGuard) {
		t.Fatalf("a swapped socket path reached the real wallet's agent: %v", err)
	}
}
