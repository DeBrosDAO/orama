package sandbox

import "testing"

func TestSSHKeyDataEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{
			name:     "identical keys",
			a:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest comment1",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest comment1",
			expected: true,
		},
		{
			name:     "same key different comments",
			a:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest user@host",
			expected: true,
		},
		{
			name:     "same key one without comment",
			a:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault",
			expected: true,
		},
		{
			name:     "different key data",
			a:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBoldkey vault",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBnewkey vault",
			expected: false,
		},
		{
			name:     "different key types",
			a:        "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB vault",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault",
			expected: false,
		},
		{
			name:     "empty string a",
			a:        "",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault",
			expected: false,
		},
		{
			name:     "empty string b",
			a:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault",
			b:        "",
			expected: false,
		},
		{
			name:     "both empty",
			a:        "",
			b:        "",
			expected: false,
		},
		{
			name:     "single field only",
			a:        "ssh-ed25519",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest",
			expected: false,
		},
		{
			name:     "whitespace trimming",
			a:        "  ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest vault  ",
			b:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtest",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sshKeyDataEqual(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("sshKeyDataEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}
