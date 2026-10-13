package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"gopkg.in/yaml.v3"
)

func writeACMENodeYAML(t *testing.T, oramaDir, body string) {
	t.Helper()
	dir := filepath.Join(oramaDir, "configs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A regeneration (upgrade) must keep the CA the node was installed with;
// dropping staging back to production would spend the production rate limits.
func TestACMECA_CarriedForwardFromNodeYAML(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "node:\n  id: x\ntls:\n  acme_ca: \"https://acme-staging-v02.api.letsencrypt.org/directory\"\n")
	cg := NewConfigGenerator(dir)
	got, err := cg.ACMECA()
	if err != nil || got != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatalf("got %q, %v", got, err)
	}
	cg.SetACMECA("https://other.example/dir")
	if got, _ := cg.ACMECA(); got != "https://other.example/dir" {
		t.Errorf("an explicit CA must win over the carried-forward one, got %q", got)
	}
}

func TestACMECA_FreshInstallIsDefaultAndBrokenYAMLIsAnError(t *testing.T) {
	if got, err := NewConfigGenerator(t.TempDir()).ACMECA(); err != nil || got != "" {
		t.Fatalf("fresh install: %q, %v", got, err)
	}
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "tls: [unclosed\n")
	if _, err := NewConfigGenerator(dir).ACMECA(); err == nil || !strings.Contains(err.Error(), "acme_ca") {
		t.Fatalf("an unreadable node.yaml must be an error, got %v", err)
	}
}

// node.yaml is decoded with KnownFields(true): a block the template renders
// but config.Config does not declare fails the install at validation.
func TestNodeYAMLWithACMECA_ParsesAsConfig(t *testing.T) {
	dir := t.TempDir()
	cg := NewConfigGenerator(dir)
	cg.SetACMECA("https://acme-staging-v02.api.letsencrypt.org/directory")
	rendered, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.example", "stagenet.example", false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("rendered node.yaml does not parse as config.Config: %v", err)
	}
	if cfg.TLS.ACMECA != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Errorf("tls.acme_ca = %q", cfg.TLS.ACMECA)
	}
}

// node.yaml is the orama user's file and acme_ca lands in the Caddyfile root
// writes: a value that was never checked, or was edited since, must not reach
// it.
func TestACMECA_RevalidatesTheValueReadFromNodeYAML(t *testing.T) {
	for _, bad := range []string{
		"http://ca.example/dir",
		"staging",
		"https://",
		"https://ca.example/dir }\\n:80 {",
		"https://ca.example/dir extra",
	} {
		dir := t.TempDir()
		writeACMENodeYAML(t, dir, "tls:\n  acme_ca: \""+bad+"\"\n")
		if got, err := NewConfigGenerator(dir).ACMECA(); err == nil {
			t.Errorf("%q from node.yaml must be refused, got %q", bad, got)
		}
	}
}

func TestValidateACMECA(t *testing.T) {
	if err := ValidateACMECA("https://acme-staging-v02.api.letsencrypt.org/directory"); err != nil {
		t.Errorf("a plain https directory URL must pass: %v", err)
	}
	for _, bad := range []string{"", "https://ca.example/dir\r", "https://ca.example/{dir}", "https://ca.example/\"dir"} {
		if err := ValidateACMECA(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
