package installers

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestNtfyInstaller returns an NtfyInstaller suitable for unit
// tests — no filesystem or network dependencies.
func newTestNtfyInstaller() *NtfyInstaller {
	return &NtfyInstaller{
		BaseInstaller: NewBaseInstaller("amd64", io.Discard),
		run:           func(string, ...string) (string, error) { return "", nil },
		installed:     func() bool { return true },
		root:          "/",
	}
}

func TestNtfyServerYAML_listensOnLocalhostOnly(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")

	// Hardening invariant #1: NEVER bind to 0.0.0.0. Caddy fronts ntfy;
	// public access to ntfy directly bypasses ntfy:Caddy TLS termination.
	if !strings.Contains(cfg, `listen-http: "127.0.0.1:`) {
		t.Errorf("server.yml must listen on 127.0.0.1; got:\n%s", cfg)
	}
	if strings.Contains(cfg, "0.0.0.0") {
		t.Errorf("server.yml must NOT bind 0.0.0.0; got:\n%s", cfg)
	}
}

func TestNtfyServerYAML_behindProxyModeOn(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")
	if !strings.Contains(cfg, "behind-proxy: true") {
		t.Errorf("server.yml must set behind-proxy: true (Caddy fronts); got:\n%s", cfg)
	}
}

func TestNtfyServerYAML_baseURLEmbedded(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")
	if !strings.Contains(cfg, "https://push.dbrs.space") {
		t.Errorf("server.yml missing public base_url; got:\n%s", cfg)
	}
}

func TestNtfyServerYAML_attachmentsDisabled(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")
	if !strings.Contains(cfg, `attachment-cache-dir: ""`) {
		t.Errorf("attachments should be disabled (Orama uses tiny payloads); got:\n%s", cfg)
	}
}

func TestNtfyServerYAML_webUIDisabled(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")
	if !strings.Contains(cfg, `web-root: "disable"`) {
		t.Errorf("web-root must be disabled (operators manage via FS, not UI); got:\n%s", cfg)
	}
}

func TestNtfyServerYAML_logFormatJSON(t *testing.T) {
	ni := newTestNtfyInstaller()
	cfg := ni.generateServerYAML("https://push.dbrs.space")
	if !strings.Contains(cfg, `log-format: "json"`) {
		t.Errorf("log-format should be json for journal parsing; got:\n%s", cfg)
	}
}

func TestNtfyConfigure_rejectsEmptyBaseURL(t *testing.T) {
	ni := newTestNtfyInstaller()
	err := ni.Configure("")
	if err == nil {
		t.Error("Configure should reject empty publicBaseURL")
	}
}

func TestVerifyNtfyTarball_refusesAnythingButThePinnedDigest(t *testing.T) {
	if _, err := verifyNtfyTarball("amd64", []byte("not the release")); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("a wrong tarball: %v", err)
	}
	if _, err := verifyNtfyTarball("riscv64", nil); err == nil {
		t.Error("an arch with no pinned digest was accepted")
	}
}

// Every arch the installer supports has a well-formed pinned digest.
func TestNtfyTarballSHA256_coversEverySupportedArch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		d, ok := ntfyTarballSHA256[arch]
		if !ok || len(d) != 64 || strings.ToLower(d) != d {
			t.Errorf("%s: pinned digest %q", arch, d)
		}
	}
}

// ntfy runs from the orama-namespace-ntfy@ template, not from a unit the
// installer writes, so the paths and user the installer lays out have to be
// the ones that template names.
func TestNtfyInstaller_matchesTheNamespaceTemplate(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	tmpl := filepath.Join(filepath.Dir(file), "..", "..", "..", "systemd", "orama-namespace-ntfy@.service")
	data, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{
		"User=" + ntfyUser,
		"ExecStart=" + ntfyBinaryPath + " serve --config " + ntfyConfigPath,
		"ReadWritePaths=" + ntfyDataDir,
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("%s does not contain %q: the installer and the template disagree", filepath.Base(tmpl), want)
		}
	}
	if NtfyListenPort != 10109 {
		t.Errorf("NtfyListenPort drift; got %d", NtfyListenPort)
	}
}
