package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistTo_otherProcessRedacts(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	feature := FromEnv(lookupFrom(nil))
	feature.PersistTo(RegistryPath(state))
	if err := feature.Add("minted-token-123456", "short"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(RegistryPath(state))
	if err != nil || info.Mode().Perm() != registryMode {
		t.Fatalf("registry %v mode %v", err, info)
	}
	runner, err := ForRun(lookupFrom(map[string]string{"HCLOUD_TOKEN": "hcloud-value-1234"}), state)
	if err != nil {
		t.Fatal(err)
	}
	got := runner.Redact("a minted-token-123456 b hcloud-value-1234 c short")
	if strings.Contains(got, "minted-token-123456") || strings.Contains(got, "hcloud-value-1234") || !strings.Contains(got, "short") {
		t.Fatalf("got %q", got)
	}
}

func TestLoadRegistry_missingIsEmpty(t *testing.T) {
	vals, err := LoadRegistry(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(vals) != 0 {
		t.Fatalf("vals %v err %v", vals, err)
	}
}

func TestAdd_sinkFailureReported(t *testing.T) {
	r := NewRedactor()
	r.PersistTo(filepath.Join(t.TempDir(), "no-such-dir", RegistryFileName))
	if err := r.Add("value-that-must-persist"); err == nil {
		t.Fatal("a registry that cannot be written was not reported")
	}
	if !strings.Contains(r.Redact("x value-that-must-persist"), Mask) {
		t.Fatal("the value was not registered locally")
	}
}

func TestAppendRegistry_refusesMultiline(t *testing.T) {
	err := AppendRegistry(filepath.Join(t.TempDir(), RegistryFileName), []string{"a\nb"})
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err %v", err)
	}
}

// TestLoadRegistry_overLongLineIsCutNotFatal: one oversized credential used
// to fail the whole load (and with it every later redaction); it is now
// registered cut, and the others still load.
func TestLoadRegistry_overLongLineIsCutNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), RegistryFileName)
	long := strings.Repeat("L", maxRegistryLine+100)
	if err := os.WriteFile(path, []byte("first-12345678\n"+long+"\nlast-12345678"), 0o600); err != nil {
		t.Fatal(err)
	}
	vals, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || vals[0] != "first-12345678" || len(vals[1]) != maxRegistryLine || vals[2] != "last-12345678" {
		t.Fatalf("loaded %d values", len(vals))
	}
}

// TestSealed_splitSealAndLookup: secret variables leave the environment and
// stay readable through LookupEnv; ReadSealed refuses a name that is not a
// secret.
func TestSealed_splitSealAndLookup(t *testing.T) {
	clean, secret := SplitSecretEnv([]string{"PATH=/bin", "HCLOUD_TOKEN=hc-1", "INFISICAL_X=y", "E2E_X=1"})
	if len(clean) != 2 || secret["HCLOUD_TOKEN"] != "hc-1" || secret["INFISICAL_X"] != "y" {
		t.Fatalf("clean %v secret %v", clean, secret)
	}
	t.Setenv("CF_API_TOKEN", "cf-in-env")
	if err := Seal(map[string]string{"CF_API_TOKEN": "cf-sealed"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Unseal("CF_API_TOKEN") })
	if _, inEnv := os.LookupEnv("CF_API_TOKEN"); inEnv || Getenv("CF_API_TOKEN") != "cf-sealed" {
		t.Fatal("the sealed value is still in the environment or lost")
	}
	if _, err := ReadSealed(strings.NewReader(`{"PATH":"/evil"}`)); err == nil {
		t.Fatal("a non-secret name was accepted from a sealing pipe")
	}
}

// TestRedactingWriter_linesAndTail: whole lines are redacted, a value split
// across writes is still masked, and Close flushes the tail.
func TestRedactingWriter_linesAndTail(t *testing.T) {
	var out strings.Builder
	w := NewRedactingWriter(&out, NewRedactor("minted-12345678").Redact)
	for _, part := range []string{"a minted-12", "345678 b\nc minted-1234", "5678"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "a "+Mask+" b\nc "+Mask {
		t.Fatalf("got %q", got)
	}
}
