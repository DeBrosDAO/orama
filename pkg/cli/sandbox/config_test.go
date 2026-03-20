package sandbox

import "testing"

func TestConfig_Validate_EmptyVaultTarget(t *testing.T) {
	cfg := &Config{
		HetznerAPIToken: "test-token",
		Domain:          "test.example.com",
		FloatingIPs:     []FloatIP{{ID: 1, IP: "1.1.1.1"}, {ID: 2, IP: "2.2.2.2"}},
		SSHKey:          SSHKeyConfig{HetznerID: 1, VaultTarget: ""},
	}
	if err := cfg.validate(); err == nil {
		t.Error("validate() should reject empty VaultTarget")
	}
}

func TestConfig_Validate_WithVaultTarget(t *testing.T) {
	cfg := &Config{
		HetznerAPIToken: "test-token",
		Domain:          "test.example.com",
		FloatingIPs:     []FloatIP{{ID: 1, IP: "1.1.1.1"}, {ID: 2, IP: "2.2.2.2"}},
		SSHKey:          SSHKeyConfig{HetznerID: 1, VaultTarget: "sandbox/root"},
	}
	if err := cfg.validate(); err != nil {
		t.Errorf("validate() unexpected error: %v", err)
	}
}

func TestConfig_Defaults_SetsVaultTarget(t *testing.T) {
	cfg := &Config{}
	cfg.Defaults()

	if cfg.SSHKey.VaultTarget != "sandbox/root" {
		t.Errorf("Defaults() VaultTarget = %q, want sandbox/root", cfg.SSHKey.VaultTarget)
	}
	if cfg.Location != "nbg1" {
		t.Errorf("Defaults() Location = %q, want nbg1", cfg.Location)
	}
	if cfg.ServerType != "cx23" {
		t.Errorf("Defaults() ServerType = %q, want cx23", cfg.ServerType)
	}
}

func TestConfig_Defaults_PreservesExistingVaultTarget(t *testing.T) {
	cfg := &Config{
		SSHKey: SSHKeyConfig{VaultTarget: "custom/user"},
	}
	cfg.Defaults()

	if cfg.SSHKey.VaultTarget != "custom/user" {
		t.Errorf("Defaults() should preserve existing VaultTarget, got %q", cfg.SSHKey.VaultTarget)
	}
}
