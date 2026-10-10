package installers

import (
	"archive/tar"
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

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// ntfy.go — feature #72. Self-hosted ntfy server installer.
//
// Generic infrastructure: installs the upstream `ntfy` binary, creates
// an `ntfy` system user and writes a hardened `/etc/ntfy/server.yml`. It
// runs as orama-namespace-ntfy@index, from the template shipped in
// core/systemd; install writes no unit of its own. The Caddy installer (caddy.go) is taught
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
	ntfyVersion = "2.28.0"

	// NtfyListenPort is the localhost port ntfy binds to. Caddy reverse-
	// proxies to it; exposed nowhere else.
	NtfyListenPort = constants.NtfyListenPort

	ntfyBinaryPath = "/usr/local/bin/ntfy"
	ntfyConfigDir  = "/etc/ntfy"
	ntfyConfigPath = "/etc/ntfy/server.yml"
	ntfyDataDir    = "/var/lib/ntfy"
	ntfyUser       = "ntfy"
)

// ntfyTarballSHA256 pins the SHA-256 of ntfy_<ntfyVersion>_linux_<arch>.tar.gz. It is recorded here,
// not read from the release's own checksums file, so a release altered after upload (tarball and
// checksums together) is refused. Update it with ntfyVersion: hash the downloaded tarball and
// compare it with upstream's checksums.txt.
var ntfyTarballSHA256 = map[string]string{
	"amd64": "881a1530e30e01f1dec202c7f41e1664e57edfb7844e73e21e345159ac3ea9b7",
	"arm64": "18a13411e315ba44781df222c432d27527fc089c2229a994c593beb9c1e247a0",
}

// verifyNtfyTarball refuses a tarball whose SHA-256 is not the pinned digest for arch.
func verifyNtfyTarball(arch string, data []byte) (string, error) {
	want, ok := ntfyTarballSHA256[arch]
	if !ok {
		return "", fmt.Errorf("no pinned ntfy %s digest for %s", ntfyVersion, arch)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return "", fmt.Errorf("ntfy tarball SHA-256 mismatch: got %s, want %s — refusing to install (possible supply-chain tampering)", got, want)
	}
	return got, nil
}

// NtfyInstaller installs and configures a self-hosted ntfy server.
// Designed for ns1 on devnet (per feature #72) and a dedicated node on
// production. Gated on by the orchestrator when WithNtfy is true.
type NtfyInstaller struct {
	*BaseInstaller
	run commandRunner
	// installed reports whether the pinned ntfy binary is in place; IsInstalled on a node.
	installed func() bool
	// root prefixes every directory and file the installer writes; "/" on a node.
	root string
}

// NewNtfyInstaller returns a new ntfy installer.
func NewNtfyInstaller(arch string, logWriter io.Writer) *NtfyInstaller {
	ni := &NtfyInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		run:           execRunner,
		root:          "/",
	}
	ni.installed = ni.IsInstalled
	return ni
}

func (ni *NtfyInstaller) path(p string) string { return filepath.Join(ni.root, p) }

// IsInstalled returns true when the ntfy binary is on disk AND reports
// a version matching the expected pin. A version mismatch returns
// false so an Install() upgrade path is triggered.
func (ni *NtfyInstaller) IsInstalled() bool {
	if _, err := os.Stat(ntfyBinaryPath); os.IsNotExist(err) {
		return false
	}
	// Older ntfy releases (the ones an upgrade replaces) have no version
	// flag: `--version` is "flag provided but not defined", so asking for it
	// made every install re-download ntfy over the running binary. `--help`
	// ends with "ntfy 2.28.0 (...), ..." on every release.
	out, err := exec.Command(ntfyBinaryPath, "--help").Output()
	if err != nil {
		return false
	}
	return ntfyReportsVersion(string(out), ntfyVersion)
}

// ntfyReportsVersion reports whether ntfy's --help output names version.
func ntfyReportsVersion(help, version string) bool {
	return strings.Contains(help, "ntfy "+version+" ")
}

// Install downloads the ntfy binary unless the pinned version is already in
// place, and lays out the data and config directories, which ensures the `ntfy`
// account that owns them (ensureDirs). The binary being in place says nothing
// about the account: a nuclear wipe deletes the account and a machine can keep
// the binary, so the account and the directories are ensured on every run and
// only the download is skipped.
func (ni *NtfyInstaller) Install() error {
	if ni.installed() {
		fmt.Fprintf(ni.logWriter, "  ✓ ntfy %s already installed\n", ntfyVersion)
	} else {
		fmt.Fprintf(ni.logWriter, "  Installing ntfy %s...\n", ntfyVersion)
		if err := ni.downloadBinary(); err != nil {
			return fmt.Errorf("ntfy: download binary: %w", err)
		}
		fmt.Fprintf(ni.logWriter, "  ✓ ntfy %s installed\n", ntfyVersion)
	}
	if err := ni.ensureDirs(); err != nil {
		return fmt.Errorf("ntfy: prepare directories: %w", err)
	}
	return nil
}

// Configure writes /etc/ntfy/server.yml. Called every Phase 4 (config
// regen) so operator-side knobs can be updated without re-installing.
// The base_url is exposed publicly via Caddy as https://push.<dnsZone>.
func (ni *NtfyInstaller) Configure(publicBaseURL string) error {
	if publicBaseURL == "" {
		return fmt.Errorf("ntfy Configure: publicBaseURL required (e.g. https://push.example.com)")
	}
	if err := ni.ensureDirs(); err != nil {
		return err
	}
	cfg := ni.generateServerYAML(publicBaseURL)
	if err := os.WriteFile(ni.path(ntfyConfigPath), []byte(cfg), 0640); err != nil {
		return fmt.Errorf("ntfy Configure: write server.yml: %w", err)
	}
	// Make config readable by ntfy user (group ntfy is set via ensureDirs).
	// A chown failure here means the unit cannot read the config, so it
	// fails the install now rather than as a confusing service-start error.
	if out, err := ni.run("chown", "root:"+ntfyUser, ni.path(ntfyConfigPath)); err != nil {
		return fmt.Errorf("ntfy Configure: chown %s to root:%s so the ntfy unit can read it: %w (%s)", ntfyConfigPath, ntfyUser, err, strings.TrimSpace(out))
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
	if _, err := ni.run("id", ntfyUser); err == nil {
		return nil
	}
	if out, err := ni.run("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", ntfyUser); err != nil {
		return fmt.Errorf("useradd: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

// ensureDirs creates and chowns the ntfy config + data directories. They are
// owned by the ntfy account, so the account is ensured first: Configure runs on
// every config regeneration, which can come without an Install.
func (ni *NtfyInstaller) ensureDirs() error {
	if err := ni.ensureUser(); err != nil {
		return fmt.Errorf("ntfy: create user: %w", err)
	}
	if err := os.MkdirAll(ni.path(ntfyConfigDir), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", ntfyConfigDir, err)
	}
	if err := os.MkdirAll(ni.path(ntfyDataDir), 0750); err != nil {
		return fmt.Errorf("mkdir %s: %w", ntfyDataDir, err)
	}
	// Data dir must be writable by the ntfy user. Config dir stays
	// root-owned so the unit can read it; group=ntfy so the service can
	// also stat it. Without the chown ntfy cannot write its cache database.
	if out, err := ni.run("chown", "-R", ntfyUser+":"+ntfyUser, ni.path(ntfyDataDir)); err != nil {
		return fmt.Errorf("chown %s to %s so ntfy can write its cache database: %w (%s)", ntfyDataDir, ntfyUser, err, strings.TrimSpace(out))
	}
	return nil
}

// downloadBinary fetches the ntfy release archive, verifies its
// SHA-256 against the digest pinned in this file, and installs the
// binary at /usr/local/bin/ntfy with 0755 permissions.
//
// Defense-in-depth: HTTPS to github.com pins the TLS chain; the
// pinned digest catches a release modified after upload, even with its
// checksums file (compromised maintainer, mirror swap, etc.). Either
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
	fmt.Fprintf(ni.logWriter, "    Downloading %s...\n", tarballURL)
	client := &http.Client{Timeout: 5 * time.Minute}

	// Download the tarball into a memory buffer (~20 MB; bounded by the
	// 200 MB CopyN guard). We need the bytes twice: once for SHA-256
	// verification, once for tar extraction.
	tarballBytes, err := httpGetLimited(client, tarballURL, 200*1024*1024)
	if err != nil {
		return fmt.Errorf("download tarball: %w", err)
	}

	actualHex, err := verifyNtfyTarball(arch, tarballBytes)
	if err != nil {
		return err
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
		return replaceBinary(ntfyBinaryPath, tr)
	}
	return fmt.Errorf("ntfy binary not found in release archive %s", tarballURL)
}

// maxNtfyBinaryBytes caps the extracted binary so a malicious archive cannot
// fill the disk. ntfy binaries are ~20 MB.
const maxNtfyBinaryBytes = 200 * 1024 * 1024

// replaceBinary writes src beside path and renames it over path. Writing into
// the path itself fails with "text file busy" while the binary runs, which is
// every upgrade of a live node; a rename swaps the directory entry and leaves
// the running process its old inode.
func replaceBinary(path string, src io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".new-*")
	if err != nil {
		return fmt.Errorf("create a temporary file beside %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(tmp, io.LimitReader(src, maxNtfyBinaryBytes+1))
	if err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if n > maxNtfyBinaryBytes {
		tmp.Close()
		return fmt.Errorf("binary in the archive is larger than %d bytes", maxNtfyBinaryBytes)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
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

// generateServerYAML produces the contents of /etc/ntfy/server.yml.
// Hardened defaults: listens on localhost, behind-proxy mode on, cache
// + persistence configured, attachments disabled. There is no auth-file
// in v1 — ntfy 2.28.0 with no auth-file does **not** default to deny.
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
# NOTE (bugboard #858): the cache is PER-NODE and message IDs are assigned
# per-instance, so a client recovering missed messages across the round-robin
# fan-out must use since=<unix-timestamp>/<duration>, NOT since=<message-id>
# (IDs differ between nodes). Each node's cache holds every fanned-out message.
cache-file: "/run/ntfy/cache.db"
cache-duration: "15m"

# Keepalive (bugboard #858): ntfy's 45s default is too long for aggressive
# carrier/mobile NATs, which silently drop idle long-lived /json streams — the
# client still thinks the stream is "open" but the server-side socket is gone,
# so real-time publishes are never delivered (the exact open-but-silent
# signature). A 25s interval keeps the NAT mapping warm and lets the client
# detect a dead connection (missing keepalive) ~2x faster so it can reconnect.
keepalive-interval: "25s"

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
`, publicBaseURL, NtfyListenPort, NtfyListenPort)
}
