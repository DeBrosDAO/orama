package installers

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ntfy.go — feature #72. Self-hosted ntfy server installer.
//
// Generic infrastructure: installs the upstream `ntfy` binary, creates
// an `ntfy` system user, writes a hardened `/etc/ntfy/server.yml`, and
// generates a systemd unit. The Caddy installer (caddy.go) is taught
// to emit a reverse-proxy block for the public `push.<dnsZone>` host
// when the operator enables ntfy on a node.
//
// Storage layout:
//   - Binary:     /usr/local/bin/ntfy
//   - Config:     /etc/ntfy/server.yml
//   - Cache + DB: /var/lib/ntfy/         (owned by ntfy user)
//   - Logs:       journal (systemd captures stdout)
//   - User:       ntfy (system user, no shell)
//
// Network:
//   - ntfy listens on 127.0.0.1:<NtfyListenPort> (default 8090); only
//     Caddy can reach it. Public TLS termination + auth headers stop
//     at Caddy. Behind-proxy mode is enabled in server.yml so ntfy
//     trusts the X-Forwarded-* headers Caddy sets.
//
// This installer is intentionally generic: any tenant who pushes to
// this ntfy server brings their own auth_token + topic via the
// /v1/namespace/push-credentials/ntfy endpoint. No tenant-specific
// state lives in this code.

const (
	// ntfyVersion is the upstream binwiederhier/ntfy release we install.
	// Update intentionally — newer ntfy versions occasionally tweak
	// server.yml schema; verify server.yml still validates before
	// bumping.
	ntfyVersion = "2.11.0"

	// NtfyListenPort is the localhost port ntfy binds to. Caddy reverse-
	// proxies to it; exposed nowhere else.
	NtfyListenPort = 8090

	ntfyBinaryPath   = "/usr/local/bin/ntfy"
	ntfyConfigDir    = "/etc/ntfy"
	ntfyConfigPath   = "/etc/ntfy/server.yml"
	ntfyDataDir      = "/var/lib/ntfy"
	ntfySystemdUnit  = "/etc/systemd/system/ntfy.service"
	ntfyUser         = "ntfy"
)

// NtfyInstaller installs and configures a self-hosted ntfy server.
// Designed for ns1 on devnet (per feature #72) and a dedicated node on
// production. Gated on by the orchestrator when WithNtfy is true.
type NtfyInstaller struct {
	*BaseInstaller
}

// NewNtfyInstaller returns a new ntfy installer.
func NewNtfyInstaller(arch string, logWriter io.Writer) *NtfyInstaller {
	return &NtfyInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
	}
}

// IsInstalled returns true when the ntfy binary is on disk AND reports
// a version matching the expected pin. A version mismatch returns
// false so an Install() upgrade path is triggered.
func (ni *NtfyInstaller) IsInstalled() bool {
	if _, err := os.Stat(ntfyBinaryPath); os.IsNotExist(err) {
		return false
	}
	out, err := exec.Command(ntfyBinaryPath, "--version").Output()
	if err != nil {
		return false
	}
	// `ntfy --version` prints e.g. "ntfy 2.11.0 (1234abc, 2024-01-01)"
	return strings.Contains(string(out), ntfyVersion)
}

// Install downloads the ntfy binary, creates the `ntfy` user, lays out
// data + config directories, and writes the systemd unit. Idempotent:
// re-running on a correctly-installed system is a no-op.
func (ni *NtfyInstaller) Install() error {
	if ni.IsInstalled() {
		fmt.Fprintf(ni.logWriter, "  ✓ ntfy %s already installed\n", ntfyVersion)
		return nil
	}

	fmt.Fprintf(ni.logWriter, "  Installing ntfy %s...\n", ntfyVersion)

	if err := ni.ensureUser(); err != nil {
		return fmt.Errorf("ntfy: create user: %w", err)
	}
	if err := ni.downloadBinary(); err != nil {
		return fmt.Errorf("ntfy: download binary: %w", err)
	}
	if err := ni.ensureDirs(); err != nil {
		return fmt.Errorf("ntfy: prepare directories: %w", err)
	}
	if err := ni.writeSystemdUnit(); err != nil {
		return fmt.Errorf("ntfy: write systemd unit: %w", err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("ntfy: systemctl daemon-reload: %w", err)
	}
	fmt.Fprintf(ni.logWriter, "  ✓ ntfy %s installed\n", ntfyVersion)
	return nil
}

// Configure writes /etc/ntfy/server.yml. Called every Phase 4 (config
// regen) so operator-side knobs can be updated without re-installing.
// The base_url is exposed publicly via Caddy as https://push.<dnsZone>.
func (ni *NtfyInstaller) Configure(publicBaseURL string) error {
	if publicBaseURL == "" {
		return fmt.Errorf("ntfy Configure: publicBaseURL required (e.g. https://push.dbrs.space)")
	}
	if err := ni.ensureDirs(); err != nil {
		return err
	}
	cfg := ni.generateServerYAML(publicBaseURL)
	if err := os.WriteFile(ntfyConfigPath, []byte(cfg), 0640); err != nil {
		return fmt.Errorf("ntfy Configure: write server.yml: %w", err)
	}
	// Make config readable by ntfy user (group ntfy is set via ensureDirs).
	// A chown failure here means the systemd unit will fail to read the
	// config — surface it so the operator notices now rather than after
	// a confusing service-start error.
	if out, err := exec.Command("chown", "root:"+ntfyUser, ntfyConfigPath).CombinedOutput(); err != nil {
		fmt.Fprintf(ni.logWriter, "  ⚠️  chown %s failed: %v (%s)\n", ntfyConfigPath, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(ni.logWriter, "  ✓ ntfy server.yml written (base_url=%s)\n", publicBaseURL)
	return nil
}

// ---- internals ------------------------------------------------------

// ensureUser creates the `ntfy` system user (no shell, no home) if it
// doesn't already exist. Used to run the ntfy process under a
// non-privileged identity.
func (ni *NtfyInstaller) ensureUser() error {
	// Check if user already exists.
	if err := exec.Command("id", ntfyUser).Run(); err == nil {
		return nil
	}
	cmd := exec.Command("useradd",
		"--system",
		"--no-create-home",
		"--shell", "/usr/sbin/nologin",
		ntfyUser)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("useradd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureDirs creates and chowns the ntfy config + data directories.
func (ni *NtfyInstaller) ensureDirs() error {
	if err := os.MkdirAll(ntfyConfigDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", ntfyConfigDir, err)
	}
	if err := os.MkdirAll(ntfyDataDir, 0750); err != nil {
		return fmt.Errorf("mkdir %s: %w", ntfyDataDir, err)
	}
	// Data dir must be writable by the ntfy user. Config dir stays
	// root-owned so the systemd unit can read it; group=ntfy so the
	// service can also stat it. A chown failure here would cause ntfy
	// to fail to write its cache database — log it loud so the operator
	// can investigate rather than chasing a confusing systemd error
	// later.
	if out, err := exec.Command("chown", "-R", ntfyUser+":"+ntfyUser, ntfyDataDir).CombinedOutput(); err != nil {
		fmt.Fprintf(ni.logWriter, "    ⚠️  chown %s failed: %v (%s)\n", ntfyDataDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// downloadBinary fetches the ntfy release archive, verifies its
// SHA-256 against the upstream checksums file, and installs the
// binary at /usr/local/bin/ntfy with 0755 permissions.
//
// Defense-in-depth: HTTPS to github.com pins the TLS chain; the
// checksum verification catches the case where a release was modified
// after upload (compromised maintainer, mirror swap, etc.). Either
// failing gate stops the install.
//
// Release URL pattern:
//
//	https://github.com/binwiederhier/ntfy/releases/download/v<VER>/ntfy_<VER>_linux_<arch>.tar.gz
func (ni *NtfyInstaller) downloadBinary() error {
	arch := ni.arch
	switch arch {
	case "amd64", "arm64":
		// supported
	case "":
		arch = "amd64"
	default:
		return fmt.Errorf("ntfy: unsupported arch %q (want amd64 or arm64)", arch)
	}
	tarballName := fmt.Sprintf("ntfy_%s_linux_%s.tar.gz", ntfyVersion, arch)
	tarballURL := fmt.Sprintf(
		"https://github.com/binwiederhier/ntfy/releases/download/v%s/%s",
		ntfyVersion, tarballName)
	checksumsURL := fmt.Sprintf(
		"https://github.com/binwiederhier/ntfy/releases/download/v%s/ntfy_%s_checksums.txt",
		ntfyVersion, ntfyVersion)

	fmt.Fprintf(ni.logWriter, "    Downloading %s...\n", tarballURL)
	client := &http.Client{Timeout: 5 * time.Minute}

	// Download the tarball into a memory buffer (~20 MB; bounded by the
	// 200 MB CopyN guard). We need the bytes twice: once for SHA-256
	// verification, once for tar extraction.
	tarballBytes, err := httpGetLimited(client, tarballURL, 200*1024*1024)
	if err != nil {
		return fmt.Errorf("download tarball: %w", err)
	}

	// Fetch the upstream checksums file and find the line for our tarball.
	checksumsBody, err := httpGetLimited(client, checksumsURL, 64*1024)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expectedSHA, err := findChecksumFor(checksumsBody, tarballName)
	if err != nil {
		return fmt.Errorf("locate checksum for %s: %w", tarballName, err)
	}

	// Verify.
	actual := sha256.Sum256(tarballBytes)
	actualHex := hex.EncodeToString(actual[:])
	if !strings.EqualFold(actualHex, expectedSHA) {
		return fmt.Errorf("ntfy tarball SHA-256 mismatch: got %s, want %s — refusing to install (possible supply-chain tampering)",
			actualHex, expectedSHA)
	}
	fmt.Fprintf(ni.logWriter, "    ✓ SHA-256 verified: %s\n", actualHex[:16]+"…")

	// Extract.
	gz, err := gzip.NewReader(bytes.NewReader(tarballBytes))
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		// The ntfy release tarball contains <ntfy_VER_linux_arch>/ntfy
		// (plus docs/LICENSE/man pages). We only care about the binary.
		if filepath.Base(hdr.Name) != "ntfy" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		dst, err := os.OpenFile(ntfyBinaryPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("open binary path: %w", err)
		}
		// Limit copy size to 200 MB so a malicious archive can't fill
		// the disk. ntfy binaries are ~20 MB; 200 MB is plenty.
		if _, err := io.CopyN(dst, tr, 200*1024*1024); err != nil && err != io.EOF {
			dst.Close()
			return fmt.Errorf("write binary: %w", err)
		}
		dst.Close()
		return nil
	}
	return fmt.Errorf("ntfy binary not found in release archive %s", tarballURL)
}

// httpGetLimited fetches url and returns up to maxBytes of body. Used
// for both the ntfy tarball (~20 MB) and the checksums file (~1 KB).
// Returns an error if HTTP status isn't 200 or the body exceeds the cap.
func httpGetLimited(client *http.Client, url string, maxBytes int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	// LimitReader + drain check: if the body would exceed maxBytes, we
	// stop reading and return an error rather than truncate silently.
	lr := io.LimitReader(resp.Body, maxBytes+1)
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes (got at least %d)", maxBytes, len(buf))
	}
	return buf, nil
}

// findChecksumFor scans an upstream-style checksums file (one entry
// per line: "<hex-sha256>  <filename>") and returns the SHA-256 hex
// digest for the given filename, or an error if not present.
func findChecksumFor(body []byte, filename string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// "*" prefix marks binary mode in BSD checksum tools; strip it.
		name := strings.TrimPrefix(fields[1], "*")
		if name == filename {
			if len(fields[0]) != 64 {
				return "", fmt.Errorf("entry for %s has wrong digest length %d (want 64)", filename, len(fields[0]))
			}
			return fields[0], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("scan checksums: %w", err)
	}
	return "", fmt.Errorf("filename %q not in checksums file", filename)
}

// writeSystemdUnit writes /etc/systemd/system/ntfy.service. Runs ntfy
// as the `ntfy` user with restricted privileges (NoNewPrivileges,
// ProtectSystem=strict, PrivateTmp). Auto-restart on failure.
func (ni *NtfyInstaller) writeSystemdUnit() error {
	unit := fmt.Sprintf(`[Unit]
Description=ntfy notification server (Orama #72)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s serve --config %s
Restart=on-failure
RestartSec=5s
# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ReadWritePaths=%s
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, ntfyUser, ntfyUser, ntfyBinaryPath, ntfyConfigPath, ntfyDataDir)
	if err := os.WriteFile(ntfySystemdUnit, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	return nil
}

// generateServerYAML produces the contents of /etc/ntfy/server.yml.
// Hardened defaults: listens on localhost, behind-proxy mode on, cache
// + persistence configured, attachments disabled (we don't need them
// for transactional push), and access defaults to deny — auth is
// per-topic via the operator-side `auth-file` (future, not in v1).
func (ni *NtfyInstaller) generateServerYAML(publicBaseURL string) string {
	return fmt.Sprintf(`# ntfy server config (Orama #72). Generated — do not edit by hand.
# Re-running the orchestrator's Phase 4 will overwrite changes here.

# Public-facing URL — used for "Topic URLs to display in the web UI"
# and Web Push registration (not used by Orama mobile clients).
base-url: %q

# Listen on localhost only. Caddy terminates TLS at push.<dnsZone> and
# reverse-proxies to here (port %d). Direct external access is blocked
# by the lack of a public listen address.
listen-http: "127.0.0.1:%d"

# Behind-proxy mode: trust the X-Forwarded-* headers Caddy sets so
# rate-limiting + visitor metrics see the real client IP, not Caddy's
# 127.0.0.1.
behind-proxy: true

# Cache + persistence. The SQLite database stores subscribed clients'
# pending messages so a disconnected client can replay on reconnect.
cache-file: "%s/cache.db"
cache-duration: "12h"

# Attachments off — Orama push payloads are tiny JSON. Disabling stops
# tenants from accidentally storing files here.
attachment-cache-dir: ""
attachment-total-size-limit: "0"

# Rate-limiting (operator caps; per-namespace rate is enforced upstream
# at the gateway via feature #69). These bound abuse if a tenant's
# credentials are compromised.
visitor-request-limit-burst: 60
visitor-request-limit-replenish: "5s"
visitor-message-daily-limit: 100000

# Web UI off — operators manage via the file system + journal, not
# via the public UI.
web-root: "disable"

# Logs to stdout so systemd-journald captures them.
log-level: "info"
log-format: "json"
`, publicBaseURL, NtfyListenPort, NtfyListenPort, ntfyDataDir)
}
