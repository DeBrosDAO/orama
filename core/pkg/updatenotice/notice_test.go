package updatenotice

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotice_writeReadClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-notice.json")
	if n, err := Read(path); n != nil || err != nil {
		t.Fatalf("no file: %v, %v", n, err)
	}
	want := Notice{State: StateAvailable, Mode: "notify", Channel: "stable", Current: "0.3.0", Candidate: "0.3.1", CheckedAt: time.Unix(1_700_000_000, 0).UTC()}
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got == nil || *got != want {
		t.Fatalf("read back %+v, %v", got, err)
	}
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("clearing nothing: %v", err)
	}
	if n, _ := Read(path); n != nil {
		t.Fatal("a cleared notice is still there")
	}
}

func TestNotice_refusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.json")
	if err := Write(path, Notice{State: "weird"}); err == nil {
		t.Error("an unknown state was written")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("a corrupt notice read as none")
	}
}
