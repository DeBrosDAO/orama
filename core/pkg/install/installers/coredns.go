package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
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

// resolvedStubDropIn is the systemd-resolved drop-in a nameserver gets: the
// stub listener is off so CoreDNS owns :53.
const resolvedStubDropIn = "[Resolve]\nDNSStubListener=no\n"

// DisableResolvedStubListener disables systemd-resolved's DNS stub listener
// so CoreDNS can bind to port 53. This is required on Ubuntu/Debian systems
// where systemd-resolved listens on 127.0.0.53:53 by default.
func (ci *CoreDNSInstaller) DisableResolvedStubListener() error {
	// Check if systemd-resolved is running
	if err := exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run(); err != nil {
		return nil // Not running, nothing to do
	}

	fmt.Fprintf(ci.logWriter, "  Disabling systemd-resolved DNS stub listener (for CoreDNS)...\n")

	changed, err := writeResolvedDropIn(resolvedDropInDir, resolvedStubDropInFile, resolvedStubDropIn)
	if err != nil {
		return err
	}

	// Point resolv.conf to localhost (CoreDNS) and a fallback
	resolvConf := "nameserver 127.0.0.1\nnameserver 8.8.8.8\n"
	if err := os.Remove("/etc/resolv.conf"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove /etc/resolv.conf so CoreDNS can own it: %w", err)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(resolvConf), 0644); err != nil {
		return fmt.Errorf("failed to write resolv.conf: %w", err)
	}

	// Restart systemd-resolved only when the drop-in changed. Leaving the stub
	// listener up means CoreDNS cannot bind :53, and the install used to report
	// success anyway.
	if changed {
		if output, err := exec.Command("systemctl", "restart", "systemd-resolved").CombinedOutput(); err != nil {
			return fmt.Errorf("restart systemd-resolved after disabling its stub listener: %w\n%s", err, output)
		}
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
	// The Corefile carries the cluster-wide rqlite password, so it is 0640:
	// chmod as well, because WriteFile keeps an existing file's mode — it used
	// to be 0644. Its owner is left as it is: the group is the account the
	// installed CoreDNS unit runs as, and it changes only together with that
	// unit (RestrictCorefileToCoreDNS, after the templates are installed). A
	// node upgraded from a release that ran CoreDNS as orama keeps root:orama
	// until then, so its CoreDNS can read the Corefile until its new unit is.
	corefile := ci.generateCorefile(domain, rq)
	corefilePath := CorefilePath
	if err := os.WriteFile(corefilePath, []byte(corefile), 0o640); err != nil {
		return fmt.Errorf("failed to write Corefile: %w", err)
	}
	if err := os.Chmod(corefilePath, 0o640); err != nil {
		return fmt.Errorf("chmod %s 0640: %w", corefilePath, err)
	}
	return nil
}

// CorefilePath is CoreDNS's config, read by orama-namespace-coredns@.
const CorefilePath = "/etc/coredns/Corefile"

// RestrictCorefileToCoreDNS makes the Corefile root:<group CoreDNS runs as>
// 0640. It runs right after the namespace templates are installed, so the
// Corefile's group and the installed CoreDNS unit's account change together.
func RestrictCorefileToCoreDNS() error {
	service := string(systemd.ServiceTypeCoreDNS)
	group, err := systemd.ServiceUser(service, systemd.Isolated(service))
	if err != nil {
		return fmt.Errorf("the group CoreDNS reads its Corefile as: %w", err)
	}
	return restrictToGroup(CorefilePath, group)
}

// coreDNSRecordRefresh is how often the rqlite plugin reloads the zone's records
// from the node's rqlite replica: how stale a nameserver's answer can be.
const coreDNSRecordRefresh = 5 * time.Second

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
        refresh %s
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
`, domain, domain, rq.BaseURL(), coreDNSRecordRefresh, authBlock)
}

// restrictToGroup makes path root:group 0640.
func restrictToGroup(path, group string) error {
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("look up the %s group for %s: %w", group, path, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("%s group id %q: %w", group, g.Gid, err)
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return fmt.Errorf("chown %s root:%s: %w", path, group, err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return fmt.Errorf("chmod %s 0640: %w", path, err)
	}
	return nil
}
