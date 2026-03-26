package namespace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	caddyfilePath = "/etc/caddy/Caddyfile"

	// Caddy stores ACME certs under this directory relative to its data dir.
	caddyACMECertDir = "certificates/acme-v02.api.letsencrypt.org-directory"

	turnCertBeginMarker = "# BEGIN TURN CERT: "
	turnCertEndMarker   = "# END TURN CERT: "
)

// provisionTURNCertViaCaddy appends the TURN domain to the local Caddyfile,
// reloads Caddy to trigger DNS-01 ACME certificate provisioning, and waits
// for the cert files to appear. Returns the cert/key paths on success.
// If Caddy is not available or cert provisioning times out, returns an error
// so the caller can fall back to a self-signed cert.
func provisionTURNCertViaCaddy(domain, acmeEndpoint string, timeout time.Duration) (certPath, keyPath string, err error) {
	// Check if cert already exists from a previous provisioning
	certPath, keyPath = caddyCertPaths(domain)
	if _, err := os.Stat(certPath); err == nil {
		return certPath, keyPath, nil
	}

	// Read current Caddyfile
	data, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read Caddyfile: %w", err)
	}

	caddyfile := string(data)

	// Check if domain block already exists (idempotent)
	marker := turnCertBeginMarker + domain
	if strings.Contains(caddyfile, marker) {
		// Block already present — just wait for cert
		return waitForCaddyCert(domain, timeout)
	}

	// Append a minimal Caddyfile block for the TURN domain
	block := fmt.Sprintf(`
%s%s
%s {
    tls {
        issuer acme {
            dns orama {
                endpoint %s
            }
        }
    }
    respond "OK" 200
}
%s%s
`, turnCertBeginMarker, domain, domain, acmeEndpoint, turnCertEndMarker, domain)

	if err := os.WriteFile(caddyfilePath, []byte(caddyfile+block), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	// Reload Caddy to pick up the new domain
	if err := reloadCaddy(); err != nil {
		return "", "", fmt.Errorf("failed to reload Caddy: %w", err)
	}

	// Wait for cert to be provisioned
	return waitForCaddyCert(domain, timeout)
}

// removeTURNCertFromCaddy removes the TURN domain block from the Caddyfile
// and reloads Caddy. Safe to call even if the block doesn't exist.
func removeTURNCertFromCaddy(domain string) error {
	data, err := os.ReadFile(caddyfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Caddyfile: %w", err)
	}

	caddyfile := string(data)
	beginMarker := turnCertBeginMarker + domain
	endMarker := turnCertEndMarker + domain

	beginIdx := strings.Index(caddyfile, beginMarker)
	if beginIdx == -1 {
		return nil // Block not found, nothing to remove
	}

	endIdx := strings.Index(caddyfile, endMarker)
	if endIdx == -1 {
		return nil // Malformed markers, skip
	}

	// Include the end marker line itself
	endIdx += len(endMarker)
	// Also consume the trailing newline if present
	if endIdx < len(caddyfile) && caddyfile[endIdx] == '\n' {
		endIdx++
	}

	// Remove leading newline before the begin marker if present
	if beginIdx > 0 && caddyfile[beginIdx-1] == '\n' {
		beginIdx--
	}

	newCaddyfile := caddyfile[:beginIdx] + caddyfile[endIdx:]
	if err := os.WriteFile(caddyfilePath, []byte(newCaddyfile), 0644); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	return reloadCaddy()
}

// caddyCertPaths returns the expected cert and key file paths in Caddy's
// storage for a given domain. Caddy stores ACME certs as standard PEM files.
func caddyCertPaths(domain string) (certPath, keyPath string) {
	dataDir := caddyDataDir()
	certDir := filepath.Join(dataDir, caddyACMECertDir, domain)
	return filepath.Join(certDir, domain+".crt"), filepath.Join(certDir, domain+".key")
}

// caddyDataDir returns Caddy's data directory. Caddy uses XDG_DATA_HOME/caddy
// if set, otherwise falls back to $HOME/.local/share/caddy.
func caddyDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "caddy")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root" // Caddy runs as root in our setup
	}
	return filepath.Join(home, ".local", "share", "caddy")
}

// waitForCaddyCert polls for the cert file to appear with a timeout.
func waitForCaddyCert(domain string, timeout time.Duration) (string, string, error) {
	certPath, keyPath := caddyCertPaths(domain)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(certPath); err == nil {
			if _, err := os.Stat(keyPath); err == nil {
				return certPath, keyPath, nil
			}
		}
		time.Sleep(5 * time.Second)
	}

	return "", "", fmt.Errorf("timed out waiting for Caddy to provision cert for %s (checked %s)", domain, certPath)
}

// reloadCaddy sends a reload signal to Caddy via systemctl.
func reloadCaddy() error {
	cmd := exec.Command("systemctl", "reload", "caddy")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl reload caddy failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
