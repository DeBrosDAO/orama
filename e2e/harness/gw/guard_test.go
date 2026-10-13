package gw

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

func TestNewWithTLS_fleetModeRequiresHTTPS(t *testing.T) {
	t.Setenv(config.EnvState, "/run/state.json")
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if _, err := NewWithTLS("http://gw.example", cfg, nil); err == nil {
		t.Fatal("clear-text gateway accepted in fleet mode")
	}
	if _, err := NewWithTLS("https://gw.example", nil, nil); err == nil {
		t.Fatal("system-root client accepted in fleet mode")
	}
	if _, err := NewWithTLS("https://gw.example", cfg, nil); err != nil {
		t.Fatalf("pinned https refused: %v", err)
	}
}

func TestNewWithTLS_httpOutsideFleetMode(t *testing.T) {
	t.Setenv(config.EnvState, "")
	if _, err := NewWithTLS("http://127.0.0.1:1", nil, nil); err != nil {
		t.Fatalf("unit-test client refused: %v", err)
	}
}

func TestRaw_nilTLSIsAnErrorNotAPanic(t *testing.T) {
	c, err := NewWithTLS("https://127.0.0.1:1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Raw(context.Background(), []byte("GET / HTTP/1.1\r\n\r\n")); err == nil || !strings.Contains(err.Error(), "TLS config") {
		t.Fatalf("err %v", err)
	}
}

// TestDo_recordErrorKeepsRequestError: when evidence cannot be written, the
// request's own failure still reaches the caller.
func TestDo_recordErrorKeepsRequestError(t *testing.T) {
	dir := t.TempDir()
	rec, err := evidence.New(filepath.Join(dir, "ev"), "gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "ev")); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := NewWithTLS("http://"+addr, nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Send(context.Background(), Req{Path: "/health"})
	var opErr *net.OpError
	if !errors.As(err, &opErr) || !strings.Contains(err.Error(), "record") {
		t.Fatalf("err %v", err)
	}
}

// TestSignIn_unregisteredTokenIsAnError: a minted token that cannot reach the
// run's registry would leak from the runner's view, so sign-in fails.
func TestSignIn_unregisteredTokenIsAnError(t *testing.T) {
	c, _, _ := startGateway(t)
	c.Recorder().Redactor().PersistTo(filepath.Join(t.TempDir(), "missing-dir", "redact-tokens"))
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SignIn(context.Background(), w, "e2e", nil); err == nil || !strings.Contains(err.Error(), "redaction") {
		t.Fatalf("err %v", err)
	}
}
