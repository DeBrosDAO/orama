package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A pre-push hook runs `go test` with GIT_DIR naming the repository being
// pushed. A git command a test or the tool runs in another directory must act on
// that directory, not rewrite the pushing repository's index.
func TestGitCommand_aHookEnvironmentDoesNotReachAnotherRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	pushing := t.TempDir()
	if out, err := gitCommand(pushing, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(pushing, "kept.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := gitCommand(pushing, "add", "kept.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	t.Setenv("GIT_DIR", filepath.Join(pushing, ".git"))

	other := t.TempDir()
	if out, err := gitCommand(other, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init in the other directory: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(other, "intruder.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := gitCommand(other, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add in the other directory: %v: %s", err, out)
	}

	pushed, err := gitCommand(pushing, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	if got := strings.TrimSpace(string(pushed)); got != "kept.txt" {
		t.Fatalf("the pushing repository's index changed: %q", got)
	}
	files, err := trackedFiles(other)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "intruder.txt" {
		t.Fatalf("trackedFiles listed %v, want the other directory's own index", files)
	}
}

func TestWithoutGitLocation_keepsEverythingElse(t *testing.T) {
	got := withoutGitLocation([]string{"GIT_DIR=/x", "PATH=/bin", "GIT_INDEX_FILE=/i", "GIT_AUTHOR_NAME=a", "HOME=/h"})
	want := []string{"PATH=/bin", "GIT_AUTHOR_NAME=a", "HOME=/h"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(withoutGitLocation(nil)) != 0 {
		t.Fatal("an empty environment must stay empty")
	}
}
