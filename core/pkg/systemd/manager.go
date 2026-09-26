package systemd

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ServiceType represents the type of namespace service
type ServiceType string

const (
	ServiceTypeRQLite      ServiceType = "rqlite"
	ServiceTypeOlric       ServiceType = "olric"
	ServiceTypeGateway     ServiceType = "gateway"
	ServiceTypeSFU         ServiceType = "sfu"
	ServiceTypeTURN        ServiceType = "turn"
	ServiceTypePubsub      ServiceType = "pubsub"
	ServiceTypeWireGuard   ServiceType = "wireguard"
	ServiceTypeIPFS        ServiceType = "ipfs"
	ServiceTypeIPFSCluster ServiceType = "ipfs-cluster"
	ServiceTypeIPFSGC      ServiceType = "ipfs-gc"
	ServiceTypeVault       ServiceType = "vault"
	ServiceTypeCaddy       ServiceType = "caddy"
	ServiceTypeNtfy        ServiceType = "ntfy"
	ServiceTypeTor         ServiceType = "tor"
	ServiceTypeSNIRouter   ServiceType = "sni-router"
	ServiceTypeCoreDNS     ServiceType = "coredns"
)

// LeftoverHostUnits are pre-factory host daemons. Install no longer writes
// them, and install and upgrade delete the ones an older install left
// (pkg/install/installers.LegacyHostUnits). IndexSupervisor starts
// orama-namespace-*@index instead and stops and disables these in case one is
// still loaded.
var LeftoverHostUnits = []string{
	"orama-ipfs.service",
	"orama-ipfs-cluster.service",
	"orama-ipfs-gc.timer",
	"orama-olric.service",
	"orama-vault.service",
	"caddy.service",
	"ntfy.service",
	"orama-sni-router.service",
}

// LeftoverWireGuardUnit is disabled (not stopped) so wg0 is never bounced.
const LeftoverWireGuardUnit = "wg-quick@wg0.service"

// LeftoverNameserverUnit is the pre-factory CoreDNS unit. Disabled on install;
// NameserverSupervisor starts orama-namespace-coredns@nameserver instead.
const LeftoverNameserverUnit = "coredns.service"

// TemplateUnits are the orama-namespace-*@ templates copied to /etc/systemd/system.
var TemplateUnits = []string{
	"orama-namespace-rqlite@.service",
	"orama-namespace-olric@.service",
	"orama-namespace-gateway@.service",
	"orama-namespace-sfu@.service",
	"orama-namespace-turn@.service",
	"orama-namespace-pubsub@.service",
	"orama-namespace-wireguard@.service",
	"orama-namespace-ipfs@.service",
	"orama-namespace-ipfs-cluster@.service",
	"orama-namespace-ipfs-gc@.service",
	"orama-namespace-ipfs-gc@.timer",
	"orama-namespace-vault@.service",
	"orama-namespace-caddy@.service",
	"orama-namespace-ntfy@.service",
	"orama-namespace-tor@.service",
	"orama-namespace-sni-router@.service",
	"orama-namespace-coredns@.service",
}

// DeploymentTemplateUnits are the per-runtime templates a tenant's deployment
// runs as.
//
// The gateway used to write a unit per deployment, with `tee` into /etc, which
// only worked because it ran as root. It does not any more — User=orama,
// ProtectSystem=strict, NoNewPrivileges=yes — so the units are installed once,
// here, and the gateway only ever writes the environment file it owns and
// starts an instance of the template.
//
// orama-deploy-build@ and orama-deploy-clean@ are not runtimes: they are the
// oneshot units that install a Node.js deployment's npm dependencies (which
// used to run inside the gateway) and remove them again.
var DeploymentTemplateUnits = []string{
	"orama-deploy-node@.service",
	"orama-deploy-npm@.service",
	"orama-deploy-go@.service",
	"orama-deploy-build@.service",
	"orama-deploy-clean@.service",
}

// UnitFilesToInstall is every unit file copied into /etc/systemd/system by
// install and upgrade: the orama-namespace-*@ templates plus the shared,
// host-level TURN unit.
//
// orama-turn.service has to be listed explicitly (bugboard #283 part 2). It is
// not a template instance — one process serves every namespace on the host
// because 3478/5349 are host-exclusive — so it matches none of the
// orama-namespace-* globs. Without it the reconciler stops the legacy
// per-namespace unit and is then refused permission to start the shared one,
// leaving the node with no TURN at all.
//
// A fresh slice is returned so callers cannot alias TemplateUnits.
func UnitFilesToInstall() []string {
	units := make([]string, 0, len(TemplateUnits)+len(DeploymentTemplateUnits)+1)
	units = append(units, TemplateUnits...)
	units = append(units, DeploymentTemplateUnits...)
	units = append(units, HostTURNServiceName)
	return units
}

// Manager manages systemd units for namespace services
type Manager struct {
	logger        *zap.Logger
	systemdDir    string
	namespaceBase string // Base directory for namespace data

	// stale holds units whose inputs (env file, config) changed since they
	// started; StartService restarts them instead of leaving them running on
	// the old inputs.
	staleMu sync.Mutex
	stale   map[string]bool
	// deferred holds running stateful units whose changed inputs StartService
	// has already reported as waiting for a rolling restart. The reconcile
	// calls StartService every pass and the change stays pending until an
	// operator restarts the unit, so without this the same warning was
	// logged every pass. A new change (MarkConfigChanged) reports again.
	deferred map[string]bool

	// unitEnvDir is the root-owned tree the units' env files live in
	// (pkg/unitenv); writeUnitEnv stores one there.
	unitEnvDir   string
	writeUnitEnv func(namespace, service, contents string) error

	// Seams for tests; NewManager sets the real ones.
	unitActive  func(unit string) bool
	activeSince func(unit string) (time.Time, error)
	runUnitCmd  func(args ...string) ([]byte, error)
}

// NewManager creates a new systemd manager
func NewManager(namespaceBase string, logger *zap.Logger) *Manager {
	return &Manager{
		logger:        logger.With(zap.String("component", "systemd-manager")),
		systemdDir:    "/etc/systemd/system",
		namespaceBase: namespaceBase,
		stale:         map[string]bool{},
		deferred:      map[string]bool{},
		unitEnvDir:    unitenv.Dir,
		writeUnitEnv:  storeUnitEnv,
		unitActive:    queryUnitActive,
		activeSince:   queryActiveSince,
		runUnitCmd:    func(args ...string) ([]byte, error) { return Systemctl(args...).CombinedOutput() },
	}
}

// IndexNamespace is the reserved namespace holding the node's own services —
// the gateway, rqlite, IPFS, Olric, Caddy and the rest that serve the node
// itself rather than a tenant.
const IndexNamespace = "index"

// NameserverNamespace is the reserved namespace holding CoreDNS on a
// nameserver node.
const NameserverNamespace = "nameserver"

// NamespaceUnit returns the systemd unit name of one namespace service, with no
// type suffix: NamespaceUnit(ServiceTypeGateway, IndexNamespace) is
// "orama-namespace-gateway@index".
//
// These names were spelled out as literals wherever something needed one, and
// the copies drifted: `orama node logs gateway` read orama-node's journal,
// which has never carried the gateway's logs, because a copy of the table said
// the gateway ran inside orama-node. One spelling, in the package that owns
// unit naming.
func NamespaceUnit(serviceType ServiceType, namespace string) string {
	return fmt.Sprintf("orama-namespace-%s@%s", serviceType, namespace)
}

// serviceName returns the systemd service name for a namespace and service type
func (m *Manager) serviceName(namespace string, serviceType ServiceType) string {
	return NamespaceUnit(serviceType, namespace) + ".service"
}

// Systemctl builds an exec.Command for a systemctl call that changes state,
// run as root: directly when this process is root, otherwise through
// `orama-privhelper call`, which passes the request over the helper's socket
// and allows only Orama's own units (pkg/privhelper). No sudo: it cannot gain
// root under NoNewPrivileges, which the orama units set.
//
// Everything on a node that drives systemd goes through this. Read-only
// queries (is-active, show) need no privilege and call systemctl directly.
func Systemctl(args ...string) *exec.Cmd {
	return privhelper.Command(privhelper.ToolSystemctl, args...)
}

// StartTimer starts a namespace instantiated timer (e.g. ipfs-gc@index.timer).
func (m *Manager) StartTimer(namespace string, serviceType ServiceType) error {
	svcName := fmt.Sprintf("orama-namespace-%s@%s.timer", serviceType, namespace)
	m.logger.Info("Starting systemd timer",
		zap.String("timer", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("start", svcName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("Failed to start timer",
			zap.String("timer", svcName),
			zap.Error(err),
			zap.String("output", string(output)))
		return fmt.Errorf("failed to start %s: %w; output: %s", svcName, err, string(output))
	}
	return nil
}

// StartService starts a namespace service
func (m *Manager) StartService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)

	// "start" on a running unit is a no-op, so a unit whose env file or config
	// was rewritten kept running on the old inputs: a re-run install left the
	// gateway on the previous run's rqlite credentials, answering 503 for good.
	// Starting means running with the current inputs, so such a unit is
	// restarted. The env file's mtime against the unit's start time also covers
	// a node that went down between writing a file and starting the unit.
	verb := "start"
	if m.isUnitActive(svcName) {
		if !m.inputsChangedSinceStart(namespace, serviceType, svcName) {
			return nil
		}
		if !restartsOnInputChange(serviceType) {
			// A clustered, stateful service is never restarted as a side effect:
			// the same reconcile runs on every node, and restarting rqlite voters
			// (or Olric members) on several nodes at once is the one thing a
			// rolling procedure exists to prevent. Its new inputs take effect at
			// its next deliberate restart.
			if m.firstDeferral(svcName) {
				m.logger.Warn("Service inputs changed; they apply at its next rolling restart",
					zap.String("service", svcName))
			}
			return nil
		}
		verb = "restart"
	}

	m.logger.Info("Starting systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace),
		zap.String("verb", verb))

	output, err := m.runUnit(verb, svcName)
	if err != nil {
		m.logger.Error("Failed to start service",
			zap.String("service", svcName),
			zap.String("verb", verb),
			zap.Error(err),
			zap.String("output", string(output)))
		return fmt.Errorf("failed to %s %s: %w; output: %s", verb, svcName, err, string(output))
	}
	m.clearStale(svcName)

	m.logger.Info("Service started successfully",
		zap.String("service", svcName),
		zap.String("verb", verb),
		zap.String("output", string(output)))
	return nil
}

// statefulClusterServices hold replicated state or cluster membership and are
// never restarted implicitly (see StartService).
var statefulClusterServices = map[ServiceType]bool{
	ServiceTypeRQLite:      true,
	ServiceTypeOlric:       true,
	ServiceTypeIPFS:        true,
	ServiceTypeIPFSCluster: true,
	ServiceTypeVault:       true,
	ServiceTypeWireGuard:   true,
}

// restartsOnInputChange reports whether StartService may restart a running
// unit of this type because its inputs changed.
func restartsOnInputChange(st ServiceType) bool {
	return !statefulClusterServices[st]
}

// MarkConfigChanged records that a namespace service's configuration (other
// than its env file, which GenerateEnvFile tracks itself) changed, so the
// next StartService restarts a running unit.
func (m *Manager) MarkConfigChanged(namespace string, serviceType ServiceType) {
	m.staleMu.Lock()
	defer m.staleMu.Unlock()
	if m.stale == nil {
		m.stale = map[string]bool{}
	}
	svcName := m.serviceName(namespace, serviceType)
	m.stale[svcName] = true
	delete(m.deferred, svcName)
}

func (m *Manager) clearStale(svcName string) {
	m.staleMu.Lock()
	defer m.staleMu.Unlock()
	delete(m.stale, svcName)
	delete(m.deferred, svcName)
}

// firstDeferral records that a stateful unit's input change is waiting for a
// rolling restart, and reports whether this is the first time since the change.
func (m *Manager) firstDeferral(svcName string) bool {
	m.staleMu.Lock()
	defer m.staleMu.Unlock()
	if m.deferred[svcName] {
		return false
	}
	if m.deferred == nil {
		m.deferred = map[string]bool{}
	}
	m.deferred[svcName] = true
	return true
}

// inputsChangedSinceStart reports whether a running unit's inputs are newer
// than the process: marked stale this run, or an env file written after the
// unit last became active.
func (m *Manager) inputsChangedSinceStart(namespace string, serviceType ServiceType, svcName string) bool {
	m.staleMu.Lock()
	stale := m.stale[svcName]
	m.staleMu.Unlock()
	if stale {
		return true
	}
	st, err := os.Stat(m.envFilePath(namespace, serviceType))
	if err != nil {
		return false // no env file: nothing on disk that the unit could be missing
	}
	since, err := m.unitActiveSince(svcName)
	if err != nil {
		// Not a restart: if this kept failing, restarting would bounce the
		// service on every reconcile. Changes made by this process are
		// covered by the stale mark above.
		m.logger.Warn("Cannot read a unit's start time to tell whether its env file is newer; leaving it running",
			zap.String("service", svcName), zap.Error(err))
		return false
	}
	return st.ModTime().After(since)
}

// The seams default to the real systemctl when a Manager is built without
// NewManager (a zero value in tests).
func (m *Manager) isUnitActive(unit string) bool {
	if m.unitActive != nil {
		return m.unitActive(unit)
	}
	return queryUnitActive(unit)
}

func (m *Manager) unitActiveSince(unit string) (time.Time, error) {
	if m.activeSince != nil {
		return m.activeSince(unit)
	}
	return queryActiveSince(unit)
}

func (m *Manager) runUnit(args ...string) ([]byte, error) {
	if m.runUnitCmd != nil {
		return m.runUnitCmd(args...)
	}
	return Systemctl(args...).CombinedOutput()
}

// queryUnitActive asks systemd whether unit is active (a query; no privilege).
func queryUnitActive(unit string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

// activeEnterLayout is ActiveEnterTimestamp as `systemctl show
// --timestamp=us+utc` prints it. UTC, because time.Parse reads an unknown zone
// abbreviation as offset zero, which would skew a local-time value.
const activeEnterLayout = "Mon 2006-01-02 15:04:05.000000 UTC"

// queryActiveSince is when unit last became active, to the microsecond.
func queryActiveSince(unit string) (time.Time, error) {
	out, err := exec.Command("systemctl", "show", "--timestamp=us+utc", "-p", "ActiveEnterTimestamp", "--value", unit).Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	return parseActiveEnter(strings.TrimSpace(string(out)))
}

func parseActiveEnter(s string) (time.Time, error) {
	t, err := time.Parse(activeEnterLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ActiveEnterTimestamp %q: %w", s, err)
	}
	return t, nil
}

// StopService stops a namespace service
func (m *Manager) StopService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)
	m.logger.Info("Stopping systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("stop", svcName)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Don't error if service is already stopped or doesn't exist
		if strings.Contains(string(output), "not loaded") || strings.Contains(string(output), "inactive") {
			m.logger.Debug("Service already stopped or not loaded", zap.String("service", svcName))
			return nil
		}
		return fmt.Errorf("failed to stop %s: %w; output: %s", svcName, err, string(output))
	}

	m.logger.Info("Service stopped successfully", zap.String("service", svcName))
	return nil
}

// RestartService restarts a namespace service
func (m *Manager) RestartService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)
	m.logger.Info("Restarting systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("restart", svcName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart %s: %w; output: %s", svcName, err, string(output))
	}

	m.logger.Info("Service restarted successfully", zap.String("service", svcName))
	return nil
}

// EnableService enables a namespace service to start on boot
func (m *Manager) EnableService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)
	m.logger.Info("Enabling systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("enable", svcName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to enable %s: %w; output: %s", svcName, err, string(output))
	}

	m.logger.Info("Service enabled successfully", zap.String("service", svcName))
	return nil
}

// DisableService disables a namespace service
func (m *Manager) DisableService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)
	m.logger.Info("Disabling systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("disable", svcName)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Don't error if service is already disabled or doesn't exist
		if strings.Contains(string(output), "not loaded") {
			m.logger.Debug("Service not loaded", zap.String("service", svcName))
			return nil
		}
		return fmt.Errorf("failed to disable %s: %w; output: %s", svcName, err, string(output))
	}

	m.logger.Info("Service disabled successfully", zap.String("service", svcName))
	return nil
}

// IsServiceActive checks if a namespace service is active
func (m *Manager) IsServiceActive(namespace string, serviceType ServiceType) (bool, error) {
	svcName := m.serviceName(namespace, serviceType)
	cmd := exec.Command("systemctl", "is-active", svcName)
	output, err := cmd.CombinedOutput()

	outputStr := strings.TrimSpace(string(output))
	m.logger.Debug("Checking service status",
		zap.String("service", svcName),
		zap.String("status", outputStr),
		zap.Error(err))

	if err != nil {
		// is-active returns exit code 3 if service is inactive/activating
		if outputStr == "inactive" || outputStr == "failed" {
			m.logger.Debug("Service is not active",
				zap.String("service", svcName),
				zap.String("status", outputStr))
			return false, nil
		}
		// "activating" means the service is starting - return false to wait longer, but no error
		if outputStr == "activating" {
			m.logger.Debug("Service is still activating",
				zap.String("service", svcName))
			return false, nil
		}
		m.logger.Error("Failed to check service status",
			zap.String("service", svcName),
			zap.Error(err),
			zap.String("output", outputStr))
		return false, fmt.Errorf("failed to check service status: %w; output: %s", err, outputStr)
	}

	isActive := outputStr == "active"
	m.logger.Debug("Service status check complete",
		zap.String("service", svcName),
		zap.Bool("active", isActive))
	return isActive, nil
}

// ReloadDaemon reloads systemd daemon configuration
func (m *Manager) ReloadDaemon() error {
	m.logger.Info("Reloading systemd daemon")
	cmd := Systemctl("daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %w; output: %s", err, string(output))
	}
	return nil
}

// serviceExists checks if a namespace service has an env file on disk,
// indicating the service was provisioned for this namespace.
func (m *Manager) serviceExists(namespace string, serviceType ServiceType) bool {
	_, err := os.Stat(m.envFilePath(namespace, serviceType))
	return err == nil
}

// StopAllNamespaceServices stops all namespace services for a given namespace
func (m *Manager) StopAllNamespaceServices(namespace string) error {
	m.logger.Info("Stopping all namespace services", zap.String("namespace", namespace))

	// Stop in reverse dependency order: SFU → TURN → Gateway → Olric → RQLite
	// SFU and TURN are conditional — only stop if they exist
	for _, svcType := range []ServiceType{ServiceTypeSFU, ServiceTypeTURN} {
		if m.serviceExists(namespace, svcType) {
			if err := m.StopService(namespace, svcType); err != nil {
				m.logger.Warn("Failed to stop service",
					zap.String("namespace", namespace),
					zap.String("service_type", string(svcType)),
					zap.Error(err))
			}
		}
	}

	// Core services always exist
	for _, svcType := range []ServiceType{ServiceTypeGateway, ServiceTypeOlric, ServiceTypeRQLite} {
		if err := m.StopService(namespace, svcType); err != nil {
			m.logger.Warn("Failed to stop service",
				zap.String("namespace", namespace),
				zap.String("service_type", string(svcType)),
				zap.Error(err))
			// Continue stopping other services even if one fails
		}
	}

	return nil
}

// StartAllNamespaceServices starts all namespace services for a given namespace
func (m *Manager) StartAllNamespaceServices(namespace string) error {
	m.logger.Info("Starting all namespace services", zap.String("namespace", namespace))

	// Start core services in dependency order: RQLite → Olric → Gateway
	for _, svcType := range []ServiceType{ServiceTypeRQLite, ServiceTypeOlric, ServiceTypeGateway} {
		if err := m.StartService(namespace, svcType); err != nil {
			return fmt.Errorf("failed to start %s service: %w", svcType, err)
		}
	}

	// Start WebRTC services if provisioned: TURN → SFU
	for _, svcType := range []ServiceType{ServiceTypeTURN, ServiceTypeSFU} {
		if m.serviceExists(namespace, svcType) {
			if err := m.StartService(namespace, svcType); err != nil {
				return fmt.Errorf("failed to start %s service: %w", svcType, err)
			}
		}
	}

	return nil
}

// ListNamespaceServices returns all namespace services currently registered in systemd
func (m *Manager) ListNamespaceServices() ([]string, error) {
	cmd := exec.Command("systemctl", "list-units", "--all", "--no-legend", "--plain", "orama-namespace-*@*.service")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list namespace services: %w; output: %s", err, string(output))
	}

	var services []string
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			services = append(services, fields[0])
		}
	}

	return services, nil
}

// StopAllNamespaceServicesGlobally stops ALL namespace services on this node (for upgrade/maintenance)
func (m *Manager) StopAllNamespaceServicesGlobally() error {
	m.logger.Info("Stopping all namespace services globally")

	services, err := m.ListNamespaceServices()
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, svc := range services {
		m.logger.Info("Stopping service", zap.String("service", svc))
		cmd := Systemctl("stop", svc)
		if output, err := cmd.CombinedOutput(); err != nil {
			m.logger.Warn("Failed to stop service",
				zap.String("service", svc),
				zap.Error(err),
				zap.String("output", string(output)))
			// Continue stopping other services
		}
	}

	return nil
}

// StopDeploymentServicesForNamespace stops all deployment systemd units for a given namespace.
//
// A deployment runs as an instance of a per-runtime template:
// orama-deploy-{runtime}@{namespace}-{name}.service, with dots replaced by
// hyphens. The glob has to name the runtime segment too — `orama-deploy-<ns>-*`
// matched the units the gateway used to write itself and matches none of these.
// This is best-effort: individual failures are logged but do not abort the operation.
func (m *Manager) StopDeploymentServicesForNamespace(namespace string) {
	// Match the sanitization from deployments/process.InstanceName.
	sanitizedNS := strings.ReplaceAll(namespace, ".", "-")
	pattern := fmt.Sprintf("orama-deploy-*@%s-*", sanitizedNS)

	m.logger.Info("Stopping deployment services for namespace",
		zap.String("namespace", namespace),
		zap.String("pattern", pattern))

	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", "--plain", pattern)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Warn("Failed to list deployment services",
			zap.String("namespace", namespace),
			zap.Error(err))
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	stopped := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		svc := fields[0]

		// Stop the service
		if stopOut, stopErr := Systemctl("stop", svc).CombinedOutput(); stopErr != nil {
			m.logger.Warn("Failed to stop deployment service",
				zap.String("service", svc),
				zap.Error(stopErr),
				zap.String("output", string(stopOut)))
		}

		// Disable the service
		if disOut, disErr := Systemctl("disable", svc).CombinedOutput(); disErr != nil {
			m.logger.Warn("Failed to disable deployment service",
				zap.String("service", svc),
				zap.Error(disErr),
				zap.String("output", string(disOut)))
		}

		// Remove the service file
		serviceFile := filepath.Join(m.systemdDir, svc)
		if !strings.HasSuffix(serviceFile, ".service") {
			serviceFile += ".service"
		}
		if rmErr := os.Remove(serviceFile); rmErr != nil && !os.IsNotExist(rmErr) {
			m.logger.Warn("Failed to remove deployment service file",
				zap.String("file", serviceFile),
				zap.Error(rmErr))
		}

		stopped++
		m.logger.Info("Stopped deployment service", zap.String("service", svc))
	}

	if stopped > 0 {
		m.ReloadDaemon()
		m.logger.Info("Deployment services cleanup complete",
			zap.String("namespace", namespace),
			zap.Int("stopped", stopped))
	}
}

// CleanupOrphanedProcesses finds and kills any orphaned namespace processes not managed by systemd
// This is for cleaning up after migration from old exec.Command approach
func (m *Manager) CleanupOrphanedProcesses() error {
	m.logger.Info("Cleaning up orphaned namespace processes")

	// Find processes listening on namespace ports (10000-10999 range)
	// This is a safety measure during migration
	cmd := exec.Command("bash", "-c", "lsof -ti:10000-10999 2>/dev/null | xargs -r kill -TERM 2>/dev/null || true")
	if output, err := cmd.CombinedOutput(); err != nil {
		m.logger.Debug("Orphaned process cleanup completed",
			zap.Error(err),
			zap.String("output", string(output)))
	}

	return nil
}

// envFilePath is where a namespace service's env file lives: the root-owned
// tree its unit's EnvironmentFile= names (pkg/unitenv).
func (m *Manager) envFilePath(namespace string, serviceType ServiceType) string {
	dir := m.unitEnvDir
	if dir == "" {
		dir = unitenv.Dir
	}
	return unitenv.Path(dir, namespace, string(serviceType))
}

// storeUnitEnv writes an env file into the root-owned tree: directly when
// this process is root (the installer), otherwise through orama-privhelper.
func storeUnitEnv(namespace, service, contents string) error {
	if os.Geteuid() != 0 {
		return privhelper.SetUnitEnv(namespace, service, contents)
	}
	g, err := user.LookupGroup("orama")
	if err != nil {
		return fmt.Errorf("look up the orama group: %w", err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("orama group id %q: %w", g.Gid, err)
	}
	return unitenv.Write(unitenv.Dir, namespace, service, []byte(contents), unitenv.Owner{UID: 0, GID: gid})
}

// RemoveNamespaceEnv removes every env file of namespace, as its data
// directory is removed.
func (m *Manager) RemoveNamespaceEnv(namespace string) error {
	if os.Geteuid() != 0 {
		return privhelper.ClearUnitEnv(namespace)
	}
	return unitenv.ClearNamespace(unitenv.Dir, namespace)
}

// GenerateEnvFile creates the environment file for a namespace service. It
// rewrites the file only when the content differs, and marks the service for
// a restart when it does.
func (m *Manager) GenerateEnvFile(namespace, nodeID string, serviceType ServiceType, envVars map[string]string) error {
	// Before anything touches the disk: the namespace names a directory here.
	if err := validateEnvFile(namespace, nodeID, serviceType, envVars); err != nil {
		return fmt.Errorf("refusing to write the env file: %w", err)
	}
	envDir := filepath.Join(m.namespaceBase, namespace)
	m.logger.Debug("Creating env directory",
		zap.String("dir", envDir))

	if err := os.MkdirAll(envDir, 0755); err != nil {
		m.logger.Error("Failed to create env directory",
			zap.String("dir", envDir),
			zap.Error(err))
		return fmt.Errorf("failed to create env directory: %w", err)
	}

	envFile := m.envFilePath(namespace, serviceType)

	var content strings.Builder
	content.WriteString("# Auto-generated environment file for namespace service\n")
	content.WriteString(fmt.Sprintf("# Namespace: %s\n", namespace))
	content.WriteString(fmt.Sprintf("# Node ID: %s\n", nodeID))
	content.WriteString(fmt.Sprintf("# Service: %s\n\n", serviceType))

	// Always include NODE_ID
	content.WriteString(fmt.Sprintf("NODE_ID=%s\n", nodeID))

	// Add all other environment variables, in a fixed order: identical inputs
	// must render identical bytes, or every reconcile would look like a change
	// and restart the service.
	keys := make([]string, 0, len(envVars))
	for key := range envVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		content.WriteString(fmt.Sprintf("%s=%s\n", key, envVars[key]))
	}

	if existing, err := os.ReadFile(envFile); err == nil && string(existing) == content.String() {
		return nil
	}

	m.logger.Debug("Writing env file",
		zap.String("file", envFile),
		zap.Int("size", content.Len()))

	write := m.writeUnitEnv
	if write == nil {
		write = storeUnitEnv
	}
	if err := write(namespace, string(serviceType), content.String()); err != nil {
		m.logger.Error("Failed to write env file",
			zap.String("file", envFile),
			zap.Error(err))
		return fmt.Errorf("failed to write env file: %w", err)
	}
	m.MarkConfigChanged(namespace, serviceType)

	m.logger.Info("Generated environment file",
		zap.String("file", envFile),
		zap.String("namespace", namespace),
		zap.String("service_type", string(serviceType)))

	return nil
}

// InstallTemplateUnits installs the systemd template unit files
func (m *Manager) InstallTemplateUnits(sourceDir string) error {
	m.logger.Info("Installing systemd template units", zap.String("source", sourceDir))

	for _, template := range UnitFilesToInstall() {
		source := filepath.Join(sourceDir, template)
		dest := filepath.Join(m.systemdDir, template)

		data, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("failed to read template %s: %w", template, err)
		}

		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("failed to write template %s: %w", template, err)
		}

		m.logger.Info("Installed template unit", zap.String("template", template))
	}

	// Reload systemd daemon to recognize new templates
	if err := m.ReloadDaemon(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %w", err)
	}

	m.logger.Info("All template units installed successfully")
	return nil
}

// HostTURNServiceName is the shared, host-level TURN unit (bugboard #283).
//
// TURN binds the well-known ports 3478/5349, which are exclusive per host, so
// one process serves every namespace on the node instead of one unit per
// namespace fighting over the same ports. It is a plain unit, not a template
// instance, because it belongs to the host rather than to any namespace — the
// same shape as orama-ipfs.service and orama-sni-router.service.
const HostTURNServiceName = "orama-turn.service"

// StartHostTURN enables and starts the shared TURN unit.
//
// Enabling is what makes the unit's [Install] section mean anything: without it
// TURN would stay down across a host reboot until a reconcile tick noticed, so
// every namespace on the node would lose its relay for up to a minute after
// every boot. Idempotent, so it is safe on every start.
func (m *Manager) StartHostTURN() error {
	m.logger.Info("Starting shared TURN service", zap.String("service", HostTURNServiceName))
	if output, err := Systemctl("enable", HostTURNServiceName).CombinedOutput(); err != nil {
		m.logger.Warn("Failed to enable shared TURN service; it will not start on boot",
			zap.String("service", HostTURNServiceName), zap.String("output", string(output)), zap.Error(err))
	}
	output, err := Systemctl("start", HostTURNServiceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start %s: %w; output: %s", HostTURNServiceName, err, string(output))
	}
	return nil
}

// StopHostTURN stops the shared TURN unit. An already-stopped or not-installed
// unit is not an error — this runs on every node, including ones that hold no
// TURN allocation at all.
func (m *Manager) StopHostTURN() error {
	output, err := Systemctl("stop", HostTURNServiceName).CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "not loaded") || strings.Contains(string(output), "inactive") {
			return nil
		}
		return fmt.Errorf("failed to stop %s: %w; output: %s", HostTURNServiceName, err, string(output))
	}
	m.logger.Info("Shared TURN service stopped", zap.String("service", HostTURNServiceName))
	return nil
}

// IsHostTURNActive reports whether the shared TURN unit is running.
func (m *Manager) IsHostTURNActive() (bool, error) {
	// Deliberately NOT the privileged Systemctl() helper: `is-active` is a query
	// that needs no privilege, and orama-privhelper allows only
	// start/stop/restart/enable/disable for this unit — routing it there makes it
	// fail always, which reads as "TURN is down" and silently disables the whole
	// host-TURN reconcile. IsServiceActive uses a bare command for the same reason.
	output, err := exec.Command("systemctl", "is-active", HostTURNServiceName).CombinedOutput()
	status := strings.TrimSpace(string(output))
	if err != nil {
		switch status {
		case "inactive", "failed", "activating", "deactivating", "unknown":
			return false, nil
		}
		return false, fmt.Errorf("failed to check %s: %w; output: %s", HostTURNServiceName, err, status)
	}
	return status == "active", nil
}

// IsLeftoverHostUnit reports whether name is one of the pre-factory host
// daemons the installer disables.
//
// A guard rather than a comment, because a node not yet upgraded still has the
// unit files on disk: any code that decides what to start by looking for a
// unit file will find these and start them, and they then race
// orama-namespace-*@index for the same ports.
func IsLeftoverHostUnit(name string) bool {
	for _, u := range LeftoverHostUnits {
		if u == name {
			return true
		}
	}
	return name == LeftoverWireGuardUnit || name == LeftoverNameserverUnit
}
