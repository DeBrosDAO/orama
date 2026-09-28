package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/unitenv"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

var ErrServiceNotFound = errors.New("service not found")

// PortSpec defines a port and its name for checking availability
type PortSpec struct {
	Name string
	Port int
}

var ServicePorts = map[string][]PortSpec{
	"orama-olric": {
		{Name: "Olric HTTP", Port: constants.OlricHTTPPort},
		{Name: "Olric Memberlist", Port: constants.OlricMemberlistPort},
	},
	"orama-node": {
		{Name: "Gateway API", Port: constants.GatewayAPIPort}, // Gateway is embedded in orama-node
		{Name: "RQLite HTTP", Port: constants.RQLiteHTTPPort},
		{Name: "RQLite Raft", Port: constants.RQLiteRaftPort},
	},
	"orama-ipfs": {
		{Name: "IPFS API", Port: constants.IPFSAPIPort},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "IPFS Swarm", Port: constants.IPFSSwarmPort},
	},
	"orama-ipfs-cluster": {
		{Name: "IPFS Cluster API", Port: constants.IPFSClusterAPIPort},
	},
}

// DefaultPorts is used for fresh installs/upgrades before unit files exist.
func DefaultPorts() []PortSpec {
	return []PortSpec{
		{Name: "IPFS Swarm", Port: 4001},
		{Name: "IPFS API", Port: constants.IPFSAPIPort},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "Gateway API", Port: constants.GatewayAPIPort},
		{Name: "RQLite HTTP", Port: constants.RQLiteHTTPPort},
		{Name: "RQLite Raft", Port: constants.RQLiteRaftPort},
		{Name: "IPFS Cluster API", Port: constants.IPFSClusterAPIPort},
		{Name: "Olric HTTP", Port: constants.OlricHTTPPort},
		{Name: "Olric Memberlist", Port: constants.OlricMemberlistPort},
	}
}

// systemdUnitDir is where install and upgrade write unit files. A variable so
// the resolver can be exercised against a directory a test controls.
var systemdUnitDir = "/etc/systemd/system"

// unitSuffixes are the unit types the resolver understands. A name ending in
// anything else is a unit name, so "orama-node" means "orama-node.service".
var unitSuffixes = []string{".service", ".timer"}

// indexUnit is the unit name of one of the node's own services.
func indexUnit(serviceType systemd.ServiceType) string {
	return systemd.NamespaceUnit(serviceType, systemd.IndexNamespace)
}

// serviceAliases maps the short names the CLI accepts to the unit that serves
// that role, current name first.
//
// Every one of these runs as a namespace instance on a current node. The
// gateway and the index rqlite are their own units — orama-node does not bind
// the gateway port and never was rqlited's parent — and IPFS, Olric, Caddy and
// IPFS-Cluster moved off host-level units, which install still writes for
// rollback and deliberately disables. An alias therefore has to name the
// instance on a current node and the host unit only on one that predates the
// move, or `orama node logs olric` reads a journal that has had nothing in it
// since the migration. This is the precedence pkg/inspector/checks/system.go
// applies.
var serviceAliases = map[string][]string{
	"node":         {"orama-node"},
	"gateway":      {indexUnit(systemd.ServiceTypeGateway), "orama-node"},
	"rqlite":       {indexUnit(systemd.ServiceTypeRQLite), "orama-node"},
	"ipfs":         {indexUnit(systemd.ServiceTypeIPFS), "orama-ipfs"},
	"cluster":      {indexUnit(systemd.ServiceTypeIPFSCluster), "orama-ipfs-cluster"},
	"ipfs-cluster": {indexUnit(systemd.ServiceTypeIPFSCluster), "orama-ipfs-cluster"},
	"olric":        {indexUnit(systemd.ServiceTypeOlric), "orama-olric"},
	"caddy":        {indexUnit(systemd.ServiceTypeCaddy), "caddy"},
	"coredns":      {systemd.NamespaceUnit(systemd.ServiceTypeCoreDNS, systemd.NameserverNamespace), "coredns"},
	"turn":         {strings.TrimSuffix(systemd.HostTURNServiceName, ".service")}, // one shared host unit, every namespace
}

// validUnitName is systemd's unit-name charset, plus the single '@' that
// separates a template from its instance and an optional unit-type suffix.
//
// The name reaches two places that read more than a literal string: it becomes
// a path under systemdUnitDir, where "../" walks out of the directory, and it
// becomes the argument of journalctl -u, which takes a glob — so
// "orama-namespace-olric@*" would read every namespace's journal in one
// command. Excluding '/' makes the first impossible and excluding the glob
// metacharacters makes the second name exactly one unit.
var validUnitName = regexp.MustCompile(`^[A-Za-z0-9:_.-]+(@[A-Za-z0-9:_.-]*)?(\.(service|timer))?$`)

// ResolveServiceName resolves an alias or unit name to the unit installed on
// this node, and reports which units it looked for when there is none.
func ResolveServiceName(alias string) (string, error) {
	if units, ok := serviceAliases[strings.ToLower(alias)]; ok {
		for _, unit := range units {
			if unitFileExists(unit) {
				return unit, nil
			}
		}
		return "", fmt.Errorf("alias %q resolves to %s, and this node has no unit file for it",
			alias, strings.Join(units, " or "))
	}

	// A leading dash would be a flag rather than a name if this string ever
	// reached a command line in a position that is not already an option value.
	if strings.HasPrefix(alias, "-") || !validUnitName.MatchString(alias) {
		return "", fmt.Errorf("%q is not a unit name. Use one of %s, or a full unit name "+
			"such as orama-namespace-olric@<namespace>", alias, strings.Join(ServiceAliases(), ", "))
	}

	if unitFileExists(alias) {
		return strings.TrimSuffix(alias, ".service"), nil
	}

	return "", fmt.Errorf("no unit named %q on this node. Use one of %s, or a full unit name "+
		"such as orama-namespace-olric@<namespace>", alias, strings.Join(ServiceAliases(), ", "))
}

// ServiceAliases lists the accepted aliases, sorted. The CLI's own help reads
// it, so adding an alias documents it.
func ServiceAliases() []string {
	names := make([]string, 0, len(serviceAliases))
	for name := range serviceAliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unitFileExists reports whether a unit file able to serve this unit name is
// installed.
//
// A template instance — "orama-namespace-olric@anchat" — has no file of its
// own: systemd instantiates it from the template, "orama-namespace-olric@.service",
// and that is what is on disk. Statting the instance name says "not found" for
// every tenant service on the node, which is what made `orama node logs
// orama-namespace-olric@<namespace>` fail even though the unit was running.
func unitFileExists(unit string) bool {
	name, suffix := unit, ".service"
	for _, s := range unitSuffixes {
		if strings.HasSuffix(unit, s) {
			name, suffix = strings.TrimSuffix(unit, s), s
			break
		}
	}

	// "foo@bar" is instantiated from "foo@.service". "foo@" is the template
	// itself: systemd will not run it, and journalctl -u against it reads
	// nothing, so it is refused here rather than resolved to an empty journal.
	if at := strings.IndexByte(name, '@'); at >= 0 {
		if at == len(name)-1 {
			return false
		}
		name = name[:at+1]
	}

	_, err := os.Stat(filepath.Join(systemdUnitDir, name+suffix))
	return err == nil
}

// ServiceUnitExists reports whether a systemd unit file is installed for the
// given service name (e.g. "caddy"). Used to guard restart/start logic so it
// only touches services actually present on this node.
func ServiceUnitExists(service string) bool {
	return unitFileExists(service)
}

// IsServiceActive checks if a systemd service is currently active (running)
func IsServiceActive(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 3:
				return false, nil
			case 4:
				return false, ErrServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

// IsServiceEnabled checks if a systemd service is enabled to start on boot
func IsServiceEnabled(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				return false, nil // Service is disabled
			case 4:
				return false, ErrServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

// IsServiceMasked checks if a systemd service is masked.
func IsServiceMasked(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", service)
	output, err := cmd.CombinedOutput()
	return maskedFromIsEnabled(string(output), err)
}

// maskedFromIsEnabled reads `systemctl is-enabled`. A disabled unit exits 1
// with the word "disabled". That is not a masked unit, and it is not a
// failure to ask: template instances the supervisor starts are disabled, and
// treating that exit as an error aborts the upgrade after the node is already
// back, with the maintenance flag still set.
func maskedFromIsEnabled(output string, runErr error) (bool, error) {
	line := strings.TrimSpace(output)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if strings.HasPrefix(line, "masked") {
		return true, nil
	}
	switch line {
	case "disabled", "enabled", "enabled-runtime", "static", "indirect",
		"generated", "transient", "linked", "linked-runtime", "alias", "aliased":
		return false, nil
	}
	if runErr != nil {
		return false, runErr
	}
	return false, nil
}

// GetProductionServices returns a list of all Orama production service names that exist,
// including both global services and namespace-specific services
func GetProductionServices() []string {
	// Global/default service names.
	//
	// orama-node only. The pre-factory host daemons — orama-olric, orama-ipfs,
	// orama-ipfs-cluster, orama-vault — are systemd.LeftoverHostUnits: the installer still
	// writes their unit files for rollback but deliberately disables them,
	// because IndexSupervisor runs orama-namespace-*@index instead.
	//
	// This list started them again on every upgrade and restart. They then
	// raced @index for 10102, 10107, :53 and :443, and IndexSupervisor stopped
	// them again on its next start — an oscillation that looked like a flaky
	// service and was really two owners for one port. The unit files exist on
	// disk, so a presence check could never tell the difference.
	globalServices := []string{
		"orama-node",
	}

	var existing []string

	// Add existing global services
	for _, svc := range globalServices {
		if systemd.IsLeftoverHostUnit(svc + ".service") {
			continue
		}
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			existing = append(existing, svc)
		}
	}

	return append(existing, discoverNamespaceUnits(namespacesDataDir, unitenv.Dir)...)
}

// namespacesDataDir holds one subdirectory per provisioned namespace.
const namespacesDataDir = "/opt/orama/.orama/data/namespaces"

// namespaceServiceTypes are the per-namespace units GetProductionServices
// looks for.
var namespaceServiceTypes = []string{
	"rqlite", "olric", "gateway", "sfu", "turn", "pubsub",
	"wireguard", "ipfs", "ipfs-cluster", "ipfs-gc", "vault",
	"caddy", "ntfy", "tor", "sni-router", "coredns",
}

// timerDrivenServiceTypes run as a oneshot fired by a .timer of the same
// instance. The timer is the unit that lives: the oneshot is inactive between
// runs, and `systemctl restart` on it does not reschedule anything — it runs
// the job right now and blocks until it ends. For ipfs-gc that was a full
// `ipfs repo gc` inside every upgrade (up to its 30-minute TimeoutStartSec),
// or, straight after the ipfs@ restart, "cannot connect to the api" and
// "Failed to restart ... exit status 1" on every node.
var timerDrivenServiceTypes = map[string]bool{
	"ipfs-gc": true,
}

// TimerBackingServices is the oneshot .service behind every timer in units.
// Masking and unmasking must cover both: a CLI before 0.122.111 masked the
// ipfs-gc service itself, so a node it stopped keeps that service masked —
// the timer would fire into a unit that cannot start, and GC would silently
// never run — unless the start or upgrade after it unmasks the service too.
// Only masking is extended: the oneshot has no [Install] section, so it is
// never enabled.
func TimerBackingServices(units []string) []string {
	var out []string
	for _, u := range units {
		if base, ok := strings.CutSuffix(u, ".timer"); ok {
			out = append(out, base+".service")
		}
	}
	return out
}

// UnmaskAll unmasks each unit that is masked. A unit that cannot be checked
// or unmasked is an error: a masked unit cannot start.
func UnmaskAll(units []string) error {
	for _, u := range units {
		masked, err := IsServiceMasked(u)
		if err != nil {
			return fmt.Errorf("check whether %s is masked: %w", u, err)
		}
		if !masked {
			continue
		}
		if out, err := exec.Command("systemctl", "unmask", u).CombinedOutput(); err != nil {
			return fmt.Errorf("unmask %s: %w: %s", u, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// discoverNamespaceUnits names the namespace units provisioned on this node.
//
// Instances are found from the namespaces data directory, one subdirectory per
// namespace: /etc/systemd/system only holds the templates
// (orama-namespace-gateway@.service), and restarting a template without an
// instance is a no-op. A service type counts as provisioned when its unit env
// file exists in envDir.
func discoverNamespaceUnits(namespacesDir, envDir string) []string {
	nsEntries, err := os.ReadDir(namespacesDir)
	if err != nil {
		return nil
	}
	var units []string
	for _, nsEntry := range nsEntries {
		if !nsEntry.IsDir() {
			continue
		}
		ns := nsEntry.Name()
		for _, svcType := range namespaceServiceTypes {
			if _, err := os.Stat(unitenv.Path(envDir, ns, svcType)); err != nil {
				continue
			}
			unit := fmt.Sprintf("orama-namespace-%s@%s", svcType, ns)
			if timerDrivenServiceTypes[svcType] {
				unit += ".timer"
			}
			units = append(units, unit)
		}
	}
	return units
}

// CollectPortsForServices returns a list of ports used by the specified services
func CollectPortsForServices(services []string, skipActive bool) ([]PortSpec, error) {
	seen := make(map[int]PortSpec)
	for _, svc := range services {
		if skipActive {
			active, err := IsServiceActive(svc)
			if err != nil {
				return nil, fmt.Errorf("unable to check %s: %w", svc, err)
			}
			if active {
				continue
			}
		}
		for _, spec := range ServicePorts[svc] {
			if _, ok := seen[spec.Port]; !ok {
				seen[spec.Port] = spec
			}
		}
	}
	ports := make([]PortSpec, 0, len(seen))
	for _, spec := range seen {
		ports = append(ports, spec)
	}
	return ports, nil
}

// EnsurePortsAvailable checks if the specified ports are available.
// If a port is in use, it identifies the process and gives actionable guidance.
func EnsurePortsAvailable(action string, ports []PortSpec) error {
	var conflicts []string
	for _, spec := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", spec.Port))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") {
				processInfo := identifyPortProcess(spec.Port)
				conflicts = append(conflicts, fmt.Sprintf("  - %s (port %d): %s", spec.Name, spec.Port, processInfo))
				continue
			}
			return fmt.Errorf("%s cannot continue: failed to inspect %s (port %d): %w", action, spec.Name, spec.Port, err)
		}
		_ = ln.Close()
	}
	if len(conflicts) > 0 {
		msg := fmt.Sprintf("%s cannot continue: the following ports are already in use:\n%s\n\n", action, strings.Join(conflicts, "\n"))
		msg += "Please stop the conflicting services before running this command.\n"
		msg += "Common fixes:\n"
		msg += "  - Docker:           sudo systemctl stop docker docker.socket\n"
		msg += "  - Old IPFS:         sudo systemctl stop ipfs\n"
		msg += "  - systemd-resolved: already handled by installer (port 53)\n"
		msg += "  - Other services:   sudo kill <PID> or sudo systemctl stop <service>"
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// identifyPortProcess uses ss/lsof to find what process is using a port
func identifyPortProcess(port int) string {
	// Try ss first (available on most Linux)
	out, err := exec.Command("ss", "-tlnp", fmt.Sprintf("sport = :%d", port)).CombinedOutput()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if strings.Contains(line, "users:") {
				// Extract process info from ss output like: users:(("docker-proxy",pid=2049,fd=4))
				if idx := strings.Index(line, "users:"); idx != -1 {
					return strings.TrimSpace(line[idx:])
				}
			}
		}
	}

	// Fallback: try lsof
	out, err = exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-sTCP:LISTEN", "-n", "-P").CombinedOutput()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 1 {
			return strings.TrimSpace(lines[1]) // first data line after header
		}
	}

	return "unknown process"
}

// NamespaceServiceOrder defines the dependency order for namespace services.
// RQLite must start first (database), then Olric (cache), then Gateway (depends on both).
// TURN and SFU are optional WebRTC services that start after Gateway.
var NamespaceServiceOrder = []string{"rqlite", "olric", "gateway", "turn", "sfu"}

// StartServicesOrdered starts services respecting namespace dependency order.
// Namespace services are started in order: rqlite → olric (+ wait) → gateway.
// Non-namespace services are started after.
// The action parameter is the systemctl command (e.g., "start" or "restart").
func StartServicesOrdered(services []string, action string) {
	// Separate namespace services by type, and collect non-namespace services
	nsServices := make(map[string][]string) // svcType → []svcName
	var other []string

	for _, svc := range services {
		matched := false
		for _, svcType := range NamespaceServiceOrder {
			prefix := "orama-namespace-" + svcType + "@"
			if strings.HasPrefix(svc, prefix) {
				nsServices[svcType] = append(nsServices[svcType], svc)
				matched = true
				break
			}
		}
		if !matched {
			other = append(other, svc)
		}
	}

	// Start namespace services in dependency order
	for _, svcType := range NamespaceServiceOrder {
		svcs := nsServices[svcType]
		for _, svc := range svcs {
			fmt.Printf("  %s%sing %s...\n", strings.ToUpper(action[:1]), action[1:], svc)
			if err := exec.Command("systemctl", action, svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to %s %s: %v\n", action, svc, err)
			} else {
				fmt.Printf("  ✓ %s\n", svc)
			}
		}

		// After starting all Olric instances, wait for each one's memberlist
		// port to accept TCP connections before starting gateways.
		//
		// This is an optimisation, not a correctness gate: a gateway that comes
		// up before Olric retries the connection (initializeOlricClientWithRetry)
		// and, failing that, keeps retrying in the background
		// (startOlricReconnectLoop) with its cache endpoints disabled until it
		// succeeds. Waiting here just means the gateway starts with a working
		// cache instead of spending its first minute without one. A timeout is
		// therefore a warning, not a failure.
		if svcType == "olric" && len(svcs) > 0 {
			fmt.Printf("  Waiting for namespace Olric instances to become ready...\n")
			for _, svc := range svcs {
				ns := strings.TrimPrefix(svc, "orama-namespace-olric@")
				addr, err := getOlricMemberlistAddr(unitenv.Dir, rootfs.At(config.ProductionBaseDir), ns)
				if err != nil {
					fmt.Printf("  ⚠️  Could not determine the Olric memberlist address for namespace %s: %v\n", ns, err)
					continue
				}
				if err := waitForTCPAddr(addr, olricReadyTimeout); err != nil {
					fmt.Printf("  ⚠️  Olric memberlist %s not ready for namespace %s: %v\n", addr, ns, err)
				} else {
					fmt.Printf("  ✓ Olric ready for namespace %s (%s)\n", ns, addr)
				}
			}
		}
	}

	// Start any remaining non-namespace services
	for _, svc := range other {
		fmt.Printf("  %s%sing %s...\n", strings.ToUpper(action[:1]), action[1:], svc)
		if err := exec.Command("systemctl", action, svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to %s %s: %v\n", action, svc, err)
		} else {
			fmt.Printf("  ✓ %s\n", svc)
		}
	}
}
