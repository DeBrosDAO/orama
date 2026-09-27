package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// testGatewayURL is the gateway the isolated test environment names.
const testGatewayURL = "https://gw.example.test"

// isolateHome points HOME at an empty directory holding one environment,
// "unit", so a test never reads the developer's own ~/.orama.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".orama")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"environments":[{"name":"unit","gateway_url":"` + testGatewayURL + `","is_active":true}],"active_environment":"unit"}`
	if err := os.WriteFile(filepath.Join(dir, "environments.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}
