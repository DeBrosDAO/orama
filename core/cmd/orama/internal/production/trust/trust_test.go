package trust

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func writeRoot(t *testing.T, validFor time.Duration) (path string, digest string) {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, now.Add(validFor))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(path, root, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, releaseverify.RootDigest(root)
}

func TestAddRoot_adoptsAValidRootOnce(t *testing.T) {
	file, digest := writeRoot(t, 24*time.Hour)
	adopted := filepath.Join(t.TempDir(), "release-root.json")
	var out bytes.Buffer
	if err := addRoot(AddRootOptions{File: file}, adopted, now, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Adopted release root "+digest) {
		t.Errorf("output %q", out.String())
	}
	got, err := os.ReadFile(adopted)
	if err != nil || releaseverify.RootDigest(got) != digest {
		t.Fatalf("adopted file: %v", err)
	}
	out.Reset()
	if err := addRoot(AddRootOptions{File: file}, adopted, now, &out); err != nil || !strings.Contains(out.String(), "already adopted") {
		t.Fatalf("the same root again: %q, %v", out.String(), err)
	}
}

func TestAddRoot_aDifferentRootNeedsReplace(t *testing.T) {
	first, firstDigest := writeRoot(t, 24*time.Hour)
	second, secondDigest := writeRoot(t, 24*time.Hour)
	adopted := filepath.Join(t.TempDir(), "release-root.json")
	if err := addRoot(AddRootOptions{File: first}, adopted, now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err := addRoot(AddRootOptions{File: second}, adopted, now, &bytes.Buffer{})
	var usage *clierr.Error
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), firstDigest) || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(adopted); releaseverify.RootDigest(got) != firstDigest {
		t.Fatal("the adopted root changed without --replace")
	}
	if err := addRoot(AddRootOptions{File: second, Replace: true}, adopted, now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(adopted); releaseverify.RootDigest(got) != secondDigest {
		t.Fatal("--replace did not replace")
	}
}

func TestAddRoot_refusals(t *testing.T) {
	adopted := filepath.Join(t.TempDir(), "release-root.json")
	expired, _ := writeRoot(t, -time.Hour)
	garbage := filepath.Join(t.TempDir(), "garbage.json")
	if err := os.WriteFile(garbage, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"expired": expired, "garbage": garbage, "missing": filepath.Join(t.TempDir(), "none.json")} {
		err := addRoot(AddRootOptions{File: file}, adopted, now, &bytes.Buffer{})
		var usage *clierr.Error
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
		if _, statErr := os.Stat(adopted); statErr == nil {
			t.Errorf("%s: a root was adopted", name)
		}
	}
}
