package deployments

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDetermineEntryPoint(t *testing.T) {
	h := &NodeJSHandler{logger: zap.NewNop()}
	for manifest, want := range map[string]string{
		`{"scripts":{"start":"node server.js"}}`: "server.js",
		`{"scripts":{"start":"next start"}}`:     "npm:start",
		`{"main":"dist/app.js"}`:                 "dist/app.js",
		`{}`:                                     "index.js",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := h.determineEntryPoint(dir)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", manifest, got, err, want)
		}
	}
}

// package.json is the tenant's. A symlink must not have the gateway read what
// it points at, and a FIFO must not hang the request.
func TestDetermineEntryPoint_readsOnlyARegularFile(t *testing.T) {
	h := &NodeJSHandler{logger: zap.NewNop()}

	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(outside, []byte(`{"main":"leaked.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "package.json")); err != nil {
		t.Fatal(err)
	}
	if got, err := h.determineEntryPoint(dir); err == nil {
		t.Errorf("a symlinked package.json was followed: %q", got)
	}

	dir = t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "package.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.determineEntryPoint(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO package.json was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO package.json blocked")
	}
}
