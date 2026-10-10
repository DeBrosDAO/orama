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
	if err := addRoot(AddRootOptions{File: file}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &out); err != nil {
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
	if err := addRoot(AddRootOptions{File: file}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &out); err != nil || !strings.Contains(out.String(), "already adopted") {
		t.Fatalf("the same root again: %q, %v", out.String(), err)
	}
}

func TestAddRoot_aDifferentRootNeedsReplace(t *testing.T) {
	first, firstDigest := writeRoot(t, 24*time.Hour)
	second, secondDigest := writeRoot(t, 24*time.Hour)
	adopted := filepath.Join(t.TempDir(), "release-root.json")
	if err := addRoot(AddRootOptions{File: first}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err := addRoot(AddRootOptions{File: second}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &bytes.Buffer{})
	var usage *clierr.Error
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), firstDigest) || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(adopted); releaseverify.RootDigest(got) != firstDigest {
		t.Fatal("the adopted root changed without --replace")
	}
	if err := addRoot(AddRootOptions{File: second, Replace: true}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &bytes.Buffer{}); err != nil {
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
		err := addRoot(AddRootOptions{File: file}, adopted, filepath.Join(t.TempDir(), "seen.json"), now, &bytes.Buffer{})
		var usage *clierr.Error
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
		if _, statErr := os.Stat(adopted); statErr == nil {
			t.Errorf("%s: a root was adopted", name)
		}
	}
}

// chainOfRoots makes root version 1 and its successor under new keys, and writes both as files.
func chainOfRoots(t *testing.T) (v1, v2 string) {
	t.Helper()
	k1, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root1, err := releaserepo.NewRoot(k1, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	root2, err := releaserepo.NextRoot(root1, k1, k2, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	v1, v2 = filepath.Join(dir, "1.root.json"), filepath.Join(dir, "2.root.json")
	if err := os.WriteFile(v1, root1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v2, root2, 0o644); err != nil {
		t.Fatal(err)
	}
	return v1, v2
}

func TestAddRoot_rotateFollowsTheNextVersionWithoutReplace(t *testing.T) {
	v1, v2 := chainOfRoots(t)
	dir := t.TempDir()
	adopted, seen := filepath.Join(dir, "release-root.json"), filepath.Join(dir, "seen.json")
	if err := addRoot(AddRootOptions{File: v1}, adopted, seen, now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// Without --rotate a different root is refused: the node does not take a pushed root on the pusher's word.
	if err := addRoot(AddRootOptions{File: v2}, adopted, seen, now, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("a different root without --rotate: %v", err)
	}
	var out bytes.Buffer
	if err := addRoot(AddRootOptions{File: v2, Rotate: true}, adopted, seen, now, &out); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(v2)
	got, _ := os.ReadFile(adopted)
	if !bytes.Equal(got, want) || !strings.Contains(out.String(), "Rotated the release root") {
		t.Errorf("adopted %d bytes, want version 2; output %q", len(got), out.String())
	}
	out.Reset()
	if err := addRoot(AddRootOptions{File: v2, Rotate: true}, adopted, seen, now, &out); err != nil || !strings.Contains(out.String(), "already adopted") {
		t.Errorf("rotating to the adopted root: %v, %q", err, out.String())
	}
}

func TestAddRoot_rotateRefusesARootThatIsNotTheNextVersionOfTheAdoptedOne(t *testing.T) {
	v1, _ := chainOfRoots(t)
	_, strangerV2 := chainOfRoots(t)
	dir := t.TempDir()
	adopted, seen := filepath.Join(dir, "release-root.json"), filepath.Join(dir, "seen.json")
	if err := addRoot(AddRootOptions{File: v1}, adopted, seen, now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err := addRoot(AddRootOptions{File: strangerV2, Rotate: true}, adopted, seen, now, &bytes.Buffer{})
	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "not the next version") {
		t.Fatalf("err = %v", err)
	}
	want, _ := os.ReadFile(v1)
	if got, _ := os.ReadFile(adopted); !bytes.Equal(got, want) {
		t.Error("a root of another chain replaced the adopted one")
	}
}

func TestAddRoot_rotateWithoutAnAdoptedRootSaysToAdoptOneFirst(t *testing.T) {
	_, v2 := chainOfRoots(t)
	dir := t.TempDir()
	err := addRoot(AddRootOptions{File: v2, Rotate: true}, filepath.Join(dir, "none.json"), filepath.Join(dir, "seen.json"), now, &bytes.Buffer{})
	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "add-root") {
		t.Fatalf("err = %v", err)
	}
}
