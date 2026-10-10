package join

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfigScalar_baseDomainAndACMECA(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "http_gateway:\n  base_domain: \"example.test\"\ntls:\n  acme_ca: \"https://acme-staging-v02.api.letsencrypt.org/directory\"\n"
	if err := os.WriteFile(filepath.Join(dir, "configs", "node.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{oramaDir: dir}
	if got := h.readBaseDomain(); got != "example.test" {
		t.Fatalf("base domain = %q", got)
	}
	if got := h.readACMECA(); got != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatalf("acme ca = %q", got)
	}

	h.oramaDir = t.TempDir()
	if h.readACMECA() != "" || h.readBaseDomain() != "" {
		t.Fatal("a missing node.yaml returned a value")
	}
}
