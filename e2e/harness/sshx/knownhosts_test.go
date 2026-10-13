package sshx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestAppendKnownHost_concurrentWritersLoseNothing: pins and unpins from
// many writers at once (the provisioner, the broker child, features) keep
// every pin that was not removed, and the file is never torn.
func TestAppendKnownHost_concurrentWritersLoseNothing(t *testing.T) {
	key, _ := newSigner(t)
	kh := filepath.Join(t.TempDir(), "kh")
	const writers = 24
	if err := AppendKnownHost(kh, "10.9.9.9", key.PublicKey()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers+1)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- AppendKnownHost(kh, fmt.Sprintf("10.1.1.%d", i+1), key.PublicKey())
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- RemoveKnownHost(kh, "10.9.9.9")
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(kh)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != writers || strings.Contains(string(raw), "10.9.9.9") {
		t.Fatalf("%d lines (want %d), removed host present: %v", len(lines), writers, strings.Contains(string(raw), "10.9.9.9"))
	}
	if info, err := os.Stat(kh); err != nil || info.Mode().Perm() != knownHostsMode {
		t.Fatalf("mode %v err %v", info.Mode(), err)
	}
}
