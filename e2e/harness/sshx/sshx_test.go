package sshx

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun_happyPath(t *testing.T) {
	s := startServer(t)
	out, errOut, exit, err := Run(context.Background(), s.target, "echo hello")
	if err != nil || exit != 0 {
		t.Fatalf("Run: exit %d err %v stderr %q", exit, err, errOut)
	}
	if out != "hello\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRun_nonZeroExitIsNotAnError(t *testing.T) {
	s := startServer(t)
	_, errOut, exit, err := Run(context.Background(), s.target, "echo bad >&2; exit 3")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit != 3 || errOut != "bad\n" {
		t.Fatalf("exit %d stderr %q, want 3 and bad", exit, errOut)
	}
}

func TestRun_refusesUnpinnedHostKey(t *testing.T) {
	s := startServer(t)
	other, _ := newSigner(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	if err := AppendKnownHost(kh, s.addr, other.PublicKey()); err != nil {
		t.Fatal(err)
	}
	target := s.target
	target.KnownHostsFile = kh
	_, _, exit, err := Run(context.Background(), target, "true")
	if err == nil || exit != exitUnknown {
		t.Fatalf("Run against a mismatched host key: exit %d err %v, want refusal", exit, err)
	}
}

func TestRun_refusesHostMissingFromKnownHosts(t *testing.T) {
	s := startServer(t)
	target := s.target
	target.KnownHostsFile = filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(target.KnownHostsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Run(context.Background(), target, "true"); err == nil {
		t.Fatal("Run against an unknown host succeeded")
	}
}

func TestRun_incompleteTarget(t *testing.T) {
	_, _, exit, err := Run(context.Background(), Target{Host: "127.0.0.1"}, "true")
	if err == nil || exit != exitUnknown {
		t.Fatalf("exit %d err %v, want an error", exit, err)
	}
}

func TestRun_contextCancelStopsCommand(t *testing.T) {
	s := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err := Run(ctx, s.target, "sleep 10")
	if err == nil {
		t.Fatal("Run outlived its context")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Run took %s after its context ended", time.Since(start))
	}
}

func TestPut_writesFileWithMode(t *testing.T) {
	s := startServer(t)
	dest := filepath.Join(t.TempDir(), "f.txt")
	if err := Put(context.Background(), s.target, dest, []byte("data\n"), 0o640); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "data\n" {
		t.Fatalf("content %q err %v", got, err)
	}
	fi, _ := os.Stat(dest)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %o, want 640", fi.Mode().Perm())
	}
}

func TestPut_emptyData(t *testing.T) {
	s := startServer(t)
	dest := filepath.Join(t.TempDir(), "empty")
	if err := Put(context.Background(), s.target, dest, nil, 0o600); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() != 0 {
		t.Fatalf("stat %v size %d", err, fi.Size())
	}
}

func TestPut_refusesUnsafePaths(t *testing.T) {
	for _, p := range []string{"relative/x", "/tmp/a b", "/tmp/x;rm -rf /", "/tmp/../etc/passwd", "/tmp/dir/", "/tmp/$(id)"} {
		if err := Put(context.Background(), Target{}, p, nil, 0o600); err == nil {
			t.Errorf("Put(%q) accepted an unsafe path", p)
		}
	}
}

func TestGet_readsFile(t *testing.T) {
	s := startServer(t)
	src := filepath.Join(t.TempDir(), "g")
	want := bytes.Repeat([]byte("x"), 100000)
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Get(context.Background(), s.target, src)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Get: %d bytes err %v", len(got), err)
	}
}

func TestGet_missingFile(t *testing.T) {
	s := startServer(t)
	_, err := Get(context.Background(), s.target, filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("Get of a missing file: %v", err)
	}
}

func TestScanHostKey_returnsServerKey(t *testing.T) {
	s := startServer(t)
	key, err := ScanHostKey(context.Background(), s.addr)
	if err != nil {
		t.Fatalf("ScanHostKey: %v", err)
	}
	if Fingerprint(key) != Fingerprint(s.hostKey.PublicKey()) {
		t.Fatal("scanned key differs from the server's")
	}
}

func TestConfirmHostKey_matchingKey(t *testing.T) {
	s := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ConfirmHostKey(ctx, s.addr, s.hostKey.PublicKey(), 50*time.Millisecond); err != nil {
		t.Fatalf("ConfirmHostKey with the server's own key: %v", err)
	}
}

func TestConfirmHostKey_otherKeyIsNeverAccepted(t *testing.T) {
	s := startServer(t)
	other, _ := newSigner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := ConfirmHostKey(ctx, s.addr, other.PublicKey(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), Fingerprint(s.hostKey.PublicKey())) {
		t.Fatalf("ConfirmHostKey with a key the server does not hold: %v", err)
	}
}

func TestConfirmHostKey_deadlineWithNoServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	want, _ := newSigner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := ConfirmHostKey(ctx, addr, want.PublicKey(), 50*time.Millisecond); err == nil {
		t.Fatal("ConfirmHostKey succeeded with nothing listening")
	}
}

func TestKnownHostsWrites_refuseSymlinks(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kh := filepath.Join(dir, "kh")
	if err := os.Symlink(victim, kh); err != nil {
		t.Fatal(err)
	}
	key, _ := newSigner(t)
	if err := AppendKnownHost(kh, "10.1.1.1", key.PublicKey()); err == nil {
		t.Fatal("AppendKnownHost followed a symlink")
	}
	if err := RemoveKnownHost(kh, "10.1.1.1"); err == nil {
		t.Fatal("RemoveKnownHost followed a symlink")
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep\n" {
		t.Fatalf("the symlink's target changed: %q", raw)
	}
}

func TestRemoveKnownHost_keepsOtherHosts(t *testing.T) {
	a, _ := newSigner(t)
	b, _ := newSigner(t)
	kh := filepath.Join(t.TempDir(), "kh")
	if err := AppendKnownHost(kh, "10.1.1.1", a.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if err := AppendKnownHost(kh, "10.1.1.2", b.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveKnownHost(kh, "10.1.1.1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(kh)
	if strings.Contains(string(raw), "10.1.1.1 ") || !strings.Contains(string(raw), "10.1.1.2 ") {
		t.Fatalf("known_hosts after removal: %q", raw)
	}
}

func TestRemoveKnownHost_missingFile(t *testing.T) {
	if err := RemoveKnownHost(filepath.Join(t.TempDir(), "none"), "10.1.1.1"); err != nil {
		t.Fatalf("RemoveKnownHost on a missing file: %v", err)
	}
}

func TestLimitedBuffer_dropsOverflow(t *testing.T) {
	b := limitedBuffer{limit: 4}
	n, err := b.Write([]byte("abcdef"))
	if err != nil || n != 6 || b.String() != "abcd" || !b.overflow {
		t.Fatalf("n %d err %v content %q overflow %v", n, err, b.String(), b.overflow)
	}
}
