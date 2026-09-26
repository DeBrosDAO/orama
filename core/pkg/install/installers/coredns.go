package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// CoreDNSInstaller handles CoreDNS installation with RQLite plugin
type CoreDNSInstaller struct {
	*BaseInstaller
	version   string
	oramaHome string
}

// NewCoreDNSInstaller creates a new CoreDNS installer
func NewCoreDNSInstaller(arch string, logWriter io.Writer, oramaHome string) *CoreDNSInstaller {
	return &CoreDNSInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       constants.CoreDNSVersion,
		oramaHome:     oramaHome,
	}
}

// Install builds and installs CoreDNS with the custom RQLite plugin
// DisableResolvedStubListener disables systemd-resolved's DNS stub listener
// so CoreDNS can bind to port 53. This is required on Ubuntu/Debian systems
// where systemd-resolved listens on 127.0.0.53:53 by default.
func (ci *CoreDNSInstaller) DisableResolvedStubListener() error {
	// Check if systemd-resolved is running
	if err := exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run(); err != nil {
		return nil // Not running, nothing to do
	}

	fmt.Fprintf(ci.logWriter, "  Disabling systemd-resolved DNS stub listener (for CoreDNS)...\n")

	// Disable the stub listener
	resolvedConf := "/etc/systemd/resolved.conf.d/no-stub.conf"
	if err := os.MkdirAll("/etc/systemd/resolved.conf.d", 0755); err != nil {
		return fmt.Errorf("failed to create resolved.conf.d: %w", err)
	}
	conf := "[Resolve]\nDNSStubListener=no\n"
	if err := os.WriteFile(resolvedConf, []byte(conf), 0644); err != nil {
		return fmt.Errorf("failed to write resolved config: %w", err)
	}

	// Point resolv.conf to localhost (CoreDNS) and a fallback
	resolvConf := "nameserver 127.0.0.1\nnameserver 8.8.8.8\n"
	if err := os.Remove("/etc/resolv.conf"); err != nil && !os.IsNotExist(err) {
		// It might be a symlink
		fmt.Fprintf(ci.logWriter, "    ⚠️  Could not remove /etc/resolv.conf: %v\n", err)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(resolvConf), 0644); err != nil {
		return fmt.Errorf("failed to write resolv.conf: %w", err)
	}

	// Restart systemd-resolved
	if output, err := exec.Command("systemctl", "restart", "systemd-resolved").CombinedOutput(); err != nil {
		fmt.Fprintf(ci.logWriter, "    ⚠️  Failed to restart systemd-resolved: %v (%s)\n", err, string(output))
	}

	fmt.Fprintf(ci.logWriter, "  ✓ systemd-resolved stub listener disabled\n")
	return nil
}

// Configure writes the CoreDNS configuration. It writes no zone records: the
// zone lives in index rqlite, and orama-node's DNS component owns it — each
// nameserver claims an nsN slot and writes its glue, its apex and wildcard A
// records, and the zone's NS set and SOA follow the claimed slots
// (pkg/node/dns_nameservers.go).
func (ci *CoreDNSInstaller) Configure(domain string, rq rqlite.Endpoint) error {
	configDir := "/etc/coredns"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create Corefile (uses only RQLite plugin)
	// The Corefile carries the cluster-wide rqlite password, so only root and
	// the orama group (CoreDNS runs as orama) may read it. Chmod/chown as well,
	// because WriteFile keeps an existing file's mode — it used to be 0644.
	corefile := ci.generateCorefile(domain, rq)
	corefilePath := filepath.Join(configDir, "Corefile")
	if err := os.WriteFile(corefilePath, []byte(corefile), 0o640); err != nil {
		return fmt.Errorf("failed to write Corefile: %w", err)
	}
	return restrictToOramaGroup(corefilePath)
}

// generateCorefile creates the CoreDNS configuration (RQLite only). The
// plugin reaches the index rqlited where it binds, with its credentials.
func (ci *CoreDNSInstaller) generateCorefile(domain string, rq rqlite.Endpoint) string {
	authBlock := fmt.Sprintf("        username %s\n        password %s\n", rq.Username, rq.Password)

	return fmt.Sprintf(`# CoreDNS configuration for %s
# Uses RQLite for ALL DNS records (static + dynamic)
# Static records (SOA, NS, A) are seeded into RQLite during installation

%s {
    # RQLite handles all records: SOA, NS, A, TXT (ACME), etc.
    rqlite {
        dsn %s
        refresh 5s
        ttl 30
        cache_size 10000
%s    }

    # Enable logging and error reporting
    log
    errors
    # NOTE: No cache here — the rqlite plugin has its own cache.
    # CoreDNS cache would cache NXDOMAIN and break ACME DNS-01 challenges.
}

# Recursion for this node's own processes (apt, ACME, the gateway), refused to
# everyone else so the node is not an open resolver (BSI/CERT-Bund).
#
# Both blocks share one listener on purpose. This block used to be restricted
# with "bind 127.0.0.1": a socket bound to 127.0.0.1:53 takes every packet sent
# to 127.0.0.1, so the node resolved its OWN zone through this forwarder —
# out to a public resolver and back through its cache. Caddy's ACME DNS-01
# propagation check ran that way and saw stale or negative answers until it
# timed out, so no certificate was ever issued. With one listener, a name in
# the zone above matches the authoritative block and everything else lands
# here, where the acl limits recursion to loopback clients.
. {
    acl {
        allow net 127.0.0.0/8 ::1/128
        block
    }
    forward . 8.8.8.8 8.8.4.4 1.1.1.1
    cache 300
    errors
}
`, domain, domain, rq.BaseURL(), authBlock)
}

// restrictToOramaGroup makes path root:orama 0640.
func restrictToOramaGroup(path string) error {
	g, err := user.LookupGroup("orama")
	if err != nil {
		return fmt.Errorf("look up the orama group for %s: %w", path, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("orama group id %q: %w", g.Gid, err)
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return fmt.Errorf("chown %s root:orama: %w", path, err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return fmt.Errorf("chmod %s 0640: %w", path, err)
	}
	return nil
}
