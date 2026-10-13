package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

func TestSSHFor_recordsTransfersAttributedToTest(t *testing.T) {
	sh := &fakeShell{files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	dir := t.TempDir()
	rec, err := evidence.New(dir, "fleet", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := NewWithDialer(testState(), rec, func(State, Node) Shell { return sh })
	shell := f.SSHFor(t, f.Node(t, "node-1"))
	if err := shell.Put(t.Context(), "/tmp/a", []byte(`{"password":"pw-12345678"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := shell.Get(t.Context(), "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	recs, err := evidence.Load(dir)
	if err != nil || len(recs) != 2 {
		t.Fatalf("recs %+v err %v", recs, err)
	}
	for _, r := range recs {
		if r.Test != t.Name() || strings.Contains(r.Input+r.Output, "pw-12345678") {
			t.Fatalf("record %+v", r)
		}
	}
	if !strings.Contains(recs[0].Summary, "put /tmp/a") || !strings.Contains(recs[1].Summary, "get /tmp/a") {
		t.Fatalf("summaries %q %q", recs[0].Summary, recs[1].Summary)
	}
}

// A sealed key backup matches no redaction pattern, so a transfer records the file's digest and
// never its bytes.
func TestSSHFor_recordsADigestNotTheContentsOfATransferredFile(t *testing.T) {
	sh := &fakeShell{files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	dir := t.TempDir()
	rec, err := evidence.New(dir, "fleet", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := NewWithDialer(testState(), rec, func(State, Node) Shell { return sh })
	shell := f.SSHFor(t, f.Node(t, "node-1"))
	sealed := []byte("ciphertext-of-a-validator-key-3f9a")
	if err := shell.Put(t.Context(), "/tmp/e2e-keybackup-1", sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := shell.Get(t.Context(), "/tmp/e2e-keybackup-1"); err != nil {
		t.Fatal(err)
	}
	recs, err := evidence.Load(dir)
	if err != nil || len(recs) != 2 {
		t.Fatalf("recs %+v err %v", recs, err)
	}
	for _, r := range recs {
		if strings.Contains(r.Input+r.Output, string(sealed)) {
			t.Fatalf("the file's bytes reached the evidence: %+v", r)
		}
		if got := r.Input + r.Output; got != fileDigest(sealed) {
			t.Fatalf("recorded %q, want the digest %q", got, fileDigest(sealed))
		}
	}
}

// TestRecordedShell_recordErrorKeepsOperationError: when evidence cannot be
// written, the command's own failure must still reach the caller.
func TestRecordedShell_recordErrorKeepsOperationError(t *testing.T) {
	dir := t.TempDir()
	rec, err := evidence.New(filepath.Join(dir, "ev"), "fleet", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "ev")); err != nil {
		t.Fatal(err)
	}
	opErr := errors.New("connection refused")
	sh := &fakeShell{answer: func(string) (Output, error) { return Output{}, opErr }, putErrs: opErr}
	f := NewWithDialer(testState(), rec, func(State, Node) Shell { return sh })
	shell := f.SSH(t.Context(), f.Node(t, "node-1"))
	if _, err := shell.Run(t.Context(), "true"); !errors.Is(err, opErr) || !strings.Contains(err.Error(), "record") {
		t.Fatalf("run err %v", err)
	}
	if err := shell.Put(t.Context(), "/x", nil, 0o600); !errors.Is(err, opErr) || !strings.Contains(err.Error(), "record") {
		t.Fatalf("put err %v", err)
	}
}
