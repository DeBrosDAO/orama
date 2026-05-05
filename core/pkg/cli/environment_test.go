package cli

import (
	"encoding/json"
	"os"
	"testing"
)

// writeTestConfig writes an EnvironmentConfig to a temp file and returns
// a helper that patches GetEnvironmentConfigPath to return that path.
// The returned cleanup restores the original function.
func writeTestConfig(t *testing.T, cfg *EnvironmentConfig) func() {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "envconfig-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()

	origFn := getEnvironmentConfigPathFn
	getEnvironmentConfigPathFn = func() (string, error) { return f.Name(), nil }
	return func() { getEnvironmentConfigPathFn = origFn }
}

func defaultTestConfig() *EnvironmentConfig {
	return &EnvironmentConfig{
		Environments: []Environment{
			{Name: "sandbox", GatewayURL: "https://dbrs.space", Description: "Sandbox cluster"},
			{Name: "devnet", GatewayURL: "https://orama-devnet.network", Description: "Development network"},
			{Name: "testnet", GatewayURL: "https://orama-testnet.network", Description: "Test network"},
		},
		ActiveEnvironment: "sandbox",
	}
}

func TestAddEnvironment_new(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := AddEnvironment("staging", "https://staging.example.com", "Staging env"); err != nil {
		t.Fatalf("AddEnvironment: %v", err)
	}

	env, err := GetEnvironmentByName("staging")
	if err != nil {
		t.Fatalf("GetEnvironmentByName: %v", err)
	}
	if env.GatewayURL != "https://staging.example.com" {
		t.Errorf("GatewayURL = %q, want %q", env.GatewayURL, "https://staging.example.com")
	}
	if env.Description != "Staging env" {
		t.Errorf("Description = %q, want %q", env.Description, "Staging env")
	}
}

func TestAddEnvironment_update(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := AddEnvironment("sandbox", "https://new.example.com", "Updated sandbox"); err != nil {
		t.Fatalf("AddEnvironment: %v", err)
	}

	env, err := GetEnvironmentByName("sandbox")
	if err != nil {
		t.Fatalf("GetEnvironmentByName: %v", err)
	}
	if env.GatewayURL != "https://new.example.com" {
		t.Errorf("GatewayURL = %q, want %q", env.GatewayURL, "https://new.example.com")
	}
	if env.Description != "Updated sandbox" {
		t.Errorf("Description = %q, want %q", env.Description, "Updated sandbox")
	}

	// Verify upsert didn't create a duplicate
	cfg, _ := LoadEnvironmentConfig()
	count := 0
	for _, e := range cfg.Environments {
		if e.Name == "sandbox" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("sandbox entries = %d, want 1", count)
	}
}

func TestRemoveEnvironment_existing(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := RemoveEnvironment("testnet"); err != nil {
		t.Fatalf("RemoveEnvironment: %v", err)
	}

	_, err := GetEnvironmentByName("testnet")
	if err == nil {
		t.Error("expected error for removed environment, got nil")
	}
}

func TestRemoveEnvironment_absent(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := RemoveEnvironment("nonexistent"); err != nil {
		t.Errorf("RemoveEnvironment(absent) = %v, want nil", err)
	}
}

func TestRemoveEnvironment_active_falls_back(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := RemoveEnvironment("sandbox"); err != nil {
		t.Fatalf("RemoveEnvironment: %v", err)
	}

	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig: %v", err)
	}
	if cfg.ActiveEnvironment != "devnet" {
		t.Errorf("ActiveEnvironment = %q, want %q", cfg.ActiveEnvironment, "devnet")
	}
}
