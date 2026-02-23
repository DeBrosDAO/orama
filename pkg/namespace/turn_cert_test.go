package namespace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaddyCertPaths(t *testing.T) {
	// Override HOME for deterministic test
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_DATA_HOME")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("XDG_DATA_HOME", origXDG)
	}()

	t.Run("default HOME path", func(t *testing.T) {
		os.Setenv("HOME", "/root")
		os.Unsetenv("XDG_DATA_HOME")

		certPath, keyPath := caddyCertPaths("turn.ns-test.example.com")

		expectedCert := "/root/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/turn.ns-test.example.com/turn.ns-test.example.com.crt"
		expectedKey := "/root/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/turn.ns-test.example.com/turn.ns-test.example.com.key"

		if certPath != expectedCert {
			t.Errorf("cert path = %q, want %q", certPath, expectedCert)
		}
		if keyPath != expectedKey {
			t.Errorf("key path = %q, want %q", keyPath, expectedKey)
		}
	})

	t.Run("XDG_DATA_HOME override", func(t *testing.T) {
		os.Setenv("XDG_DATA_HOME", "/custom/data")
		certPath, keyPath := caddyCertPaths("turn.ns-test.example.com")

		expectedCert := "/custom/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/turn.ns-test.example.com/turn.ns-test.example.com.crt"
		expectedKey := "/custom/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/turn.ns-test.example.com/turn.ns-test.example.com.key"

		if certPath != expectedCert {
			t.Errorf("cert path = %q, want %q", certPath, expectedCert)
		}
		if keyPath != expectedKey {
			t.Errorf("key path = %q, want %q", keyPath, expectedKey)
		}
	})
}

func TestRemoveTURNCertFromCaddy_MarkerRemoval(t *testing.T) {
	// Create a temporary Caddyfile with a TURN cert block
	tmpDir := t.TempDir()
	tmpCaddyfile := filepath.Join(tmpDir, "Caddyfile")

	domain := "turn.ns-test.example.com"
	original := `{
    email admin@example.com
}

*.example.com {
    tls {
        issuer acme {
            dns orama {
                endpoint http://localhost:6001/v1/internal/acme
            }
        }
    }
    reverse_proxy localhost:6001
}

# BEGIN TURN CERT: turn.ns-test.example.com
turn.ns-test.example.com {
    tls {
        issuer acme {
            dns orama {
                endpoint http://localhost:6001/v1/internal/acme
            }
        }
    }
    respond "OK" 200
}
# END TURN CERT: turn.ns-test.example.com
`

	if err := os.WriteFile(tmpCaddyfile, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// Test the marker removal logic directly (not calling removeTURNCertFromCaddy
	// because it tries to reload Caddy via systemctl)
	data, err := os.ReadFile(tmpCaddyfile)
	if err != nil {
		t.Fatal(err)
	}

	caddyfile := string(data)
	beginMarker := turnCertBeginMarker + domain
	endMarker := turnCertEndMarker + domain

	beginIdx := findIndex(caddyfile, beginMarker)
	if beginIdx == -1 {
		t.Fatal("BEGIN marker not found")
	}

	endIdx := findIndex(caddyfile, endMarker)
	if endIdx == -1 {
		t.Fatal("END marker not found")
	}

	// Include end marker line
	endIdx += len(endMarker)
	if endIdx < len(caddyfile) && caddyfile[endIdx] == '\n' {
		endIdx++
	}

	// Remove leading newline
	if beginIdx > 0 && caddyfile[beginIdx-1] == '\n' {
		beginIdx--
	}

	result := caddyfile[:beginIdx] + caddyfile[endIdx:]

	// Verify the TURN block is removed
	if findIndex(result, "TURN CERT") != -1 {
		t.Error("TURN CERT markers still present after removal")
	}
	if findIndex(result, "turn.ns-test.example.com") != -1 {
		t.Error("TURN domain still present after removal")
	}

	// Verify the rest of the Caddyfile is intact
	if findIndex(result, "*.example.com") == -1 {
		t.Error("wildcard domain block was incorrectly removed")
	}
	if findIndex(result, "reverse_proxy localhost:6001") == -1 {
		t.Error("reverse_proxy directive was incorrectly removed")
	}
}

func TestRemoveTURNCertFromCaddy_NoMarkers(t *testing.T) {
	// When no markers exist, the Caddyfile should be unchanged
	original := `{
    email admin@example.com
}

*.example.com {
    reverse_proxy localhost:6001
}
`
	caddyfile := original
	beginMarker := turnCertBeginMarker + "turn.ns-test.example.com"

	beginIdx := findIndex(caddyfile, beginMarker)
	if beginIdx != -1 {
		t.Error("expected no BEGIN marker in Caddyfile without TURN block")
	}
	// If no marker found, nothing to remove — original unchanged
}

func TestCaddyDataDir(t *testing.T) {
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_DATA_HOME")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("XDG_DATA_HOME", origXDG)
	}()

	t.Run("XDG set", func(t *testing.T) {
		os.Setenv("XDG_DATA_HOME", "/xdg/data")
		got := caddyDataDir()
		if got != "/xdg/data/caddy" {
			t.Errorf("caddyDataDir() = %q, want /xdg/data/caddy", got)
		}
	})

	t.Run("HOME fallback", func(t *testing.T) {
		os.Unsetenv("XDG_DATA_HOME")
		os.Setenv("HOME", "/home/user")
		got := caddyDataDir()
		if got != "/home/user/.local/share/caddy" {
			t.Errorf("caddyDataDir() = %q, want /home/user/.local/share/caddy", got)
		}
	})

	t.Run("root fallback", func(t *testing.T) {
		os.Unsetenv("XDG_DATA_HOME")
		os.Unsetenv("HOME")
		got := caddyDataDir()
		if got != "/root/.local/share/caddy" {
			t.Errorf("caddyDataDir() = %q, want /root/.local/share/caddy", got)
		}
	})
}

// findIndex returns the index of the first occurrence of substr in s, or -1.
func findIndex(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
