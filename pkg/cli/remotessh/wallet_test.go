package remotessh

import "testing"

func TestParseVaultTarget(t *testing.T) {
	tests := []struct {
		target   string
		wantHost string
		wantUser string
	}{
		{"sandbox/root", "sandbox", "root"},
		{"192.168.1.1/ubuntu", "192.168.1.1", "ubuntu"},
		{"my-host/my-user", "my-host", "my-user"},
		{"noslash", "noslash", ""},
		{"a/b/c", "a", "b/c"},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			host, user := parseVaultTarget(tt.target)
			if host != tt.wantHost {
				t.Errorf("parseVaultTarget(%q) host = %q, want %q", tt.target, host, tt.wantHost)
			}
			if user != tt.wantUser {
				t.Errorf("parseVaultTarget(%q) user = %q, want %q", tt.target, user, tt.wantUser)
			}
		})
	}
}
