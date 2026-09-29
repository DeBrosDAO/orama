package gotest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// TestRedactFile_overLongLineIsCutNotLeft: a line over the bound used to
// fail the scan and leave the whole file unredacted; now it is cut and every
// secret, before and after it, is masked.
func TestRedactFile_overLongLineIsCutNotLeft(t *testing.T) {
	old := lineBound
	lineBound = 4096
	t.Cleanup(func() { lineBound = old })
	path := filepath.Join(t.TempDir(), "out.json")
	long := "x minted-77777777 " + strings.Repeat("y", lineBound+10) + " minted-77777777"
	in := "first minted-77777777\n" + long + "\nlast minted-77777777\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RedactFile(path, secrets.NewRedactor("minted-77777777").Redact); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	if strings.Contains(out, "minted-77777777") || !strings.Contains(out, cutMarker) ||
		!strings.HasPrefix(out, "first "+secrets.Mask) || !strings.HasSuffix(out, "last "+secrets.Mask+"\n") {
		t.Fatalf("redacted output (%d bytes) kept a secret or lost a line", len(out))
	}
}

// TestRedactFile_failureWithholdsTheFile: when the redacted copy cannot be
// written, the original is replaced by the withheld marker, never kept.
func TestRedactFile_failureWithholdsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := os.WriteFile(path, []byte("secret minted-66666666\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".redacting", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RedactFile(path, secrets.NewRedactor("minted-66666666").Redact); err == nil {
		t.Fatal("a failed redaction was not reported")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != secrets.Withheld {
		t.Fatalf("the file after a failed redaction: %q", raw)
	}
}
