package noderesolver

import "testing"

// The vault target says which wallet key opens a node. It was computed in two
// places — here and in the push command — so a machine could be addressed with
// one key by the resolver and a different one by push.

func TestNewNode_defaults_the_ssh_user_to_root(t *testing.T) {
	n := NewNode("1.2.3.4", "", "devnet")
	if n.User != "root" {
		t.Errorf("User = %q, want root", n.User)
	}
	if n.VaultTarget != "1.2.3.4/root" {
		t.Errorf("VaultTarget = %q, want 1.2.3.4/root", n.VaultTarget)
	}
}

func TestNewNode_keeps_an_explicit_ssh_user(t *testing.T) {
	n := NewNode("1.2.3.4", "debros", "testnet")
	if n.User != "debros" {
		t.Errorf("User = %q, want debros", n.User)
	}
	if n.VaultTarget != "1.2.3.4/debros" {
		t.Errorf("VaultTarget = %q, want 1.2.3.4/debros", n.VaultTarget)
	}
}

// Every ephemeral sandbox node is created from one shared key, so its target
// is fixed rather than derived from the host.
func TestNewNode_sandbox_nodes_share_one_key(t *testing.T) {
	n := NewNode("5.6.7.8", "root", "sandbox")
	if n.VaultTarget != "sandbox/root" {
		t.Errorf("VaultTarget = %q, want sandbox/root", n.VaultTarget)
	}
}

func TestNewNode_sandbox_target_ignores_a_custom_user(t *testing.T) {
	n := NewNode("5.6.7.8", "ubuntu", "sandbox")
	if n.VaultTarget != "sandbox/root" {
		t.Errorf("VaultTarget = %q, want sandbox/root", n.VaultTarget)
	}
	if n.User != "ubuntu" {
		t.Errorf("User = %q, want the login to stay ubuntu", n.User)
	}
}

func TestNewNode_carries_the_environment(t *testing.T) {
	if got := NewNode("1.2.3.4", "root", "testnet").Environment; got != "testnet" {
		t.Errorf("Environment = %q, want testnet", got)
	}
}

// An empty environment is what a caller passes before the active one is known.
// It must still produce a usable per-host target rather than the sandbox one.
func TestNewNode_empty_environment_uses_the_per_host_target(t *testing.T) {
	n := NewNode("1.2.3.4", "", "")
	if n.VaultTarget != "1.2.3.4/root" {
		t.Errorf("VaultTarget = %q, want 1.2.3.4/root", n.VaultTarget)
	}
}
