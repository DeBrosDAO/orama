package functions

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFunctionYAML writes a function.yaml into a fresh temp dir and returns it.
func writeFunctionYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write function.yaml: %v", err)
	}
	return dir
}

func TestLoadConfig_RawHTTPResponse_true(t *testing.T) {
	dir := writeFunctionYAML(t, "name: rpc-proxy\nraw_http_response: true\n")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.RawHTTPResponse {
		t.Error("RawHTTPResponse = false, want true")
	}
}

func TestLoadConfig_RawHTTPResponse_defaultsFalse(t *testing.T) {
	dir := writeFunctionYAML(t, "name: plain-fn\n")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RawHTTPResponse {
		t.Error("RawHTTPResponse = true, want false (omitted in yaml)")
	}
}

func TestLoadConfig_RawHTTPResponse_explicitFalse(t *testing.T) {
	dir := writeFunctionYAML(t, "name: plain-fn\nraw_http_response: false\n")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RawHTTPResponse {
		t.Error("RawHTTPResponse = true, want false")
	}
}
