package systemd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// ServiceType represents the type of namespace service
type ServiceType string

const (
	ServiceTypeRQLite       ServiceType = "rqlite"
	ServiceTypeOlric        ServiceType = "olric"
	ServiceTypeGateway      ServiceType = "gateway"
	ServiceTypeSFU          ServiceType = "sfu"
	ServiceTypeTURN         ServiceType = "turn"
	ServiceTypePubsub       ServiceType = "pubsub"
	ServiceTypeWireGuard    ServiceType = "wireguard"
	ServiceTypeIPFS         ServiceType = "ipfs"
	ServiceTypeIPFSCluster  ServiceType = "ipfs-cluster"
	ServiceTypeIPFSGC       ServiceType = "ipfs-gc"
	ServiceTypeVault        ServiceType = "vault"
	ServiceTypeCaddy        ServiceType = "caddy"
	ServiceTypeNtfy         ServiceType = "ntfy"
	ServiceTypeAnyoneClient ServiceType = "anyone-client"
	ServiceTypeSNIRouter    ServiceType = "sni-router"
	ServiceTypeCoreDNS      ServiceType = "coredns"
)

// LeftoverHostUnits are pre-factory host daemons. The installer still writes
// them for rollback but never enables them. IndexSupervisor starts
// orama-namespace-*@index instead, then disables these.
var LeftoverHostUnits = []string{
	"orama-ipfs.service",
	"orama-ipfs-cluster.service",
	"orama-ipfs-gc.timer",
	"orama-olric.service",
	"orama-vault.service",
	"orama-anyone-client.service",
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
	"orama-namespace-anyone-client@.service",
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
var DeploymentTemplateUnits = []string{
	"orama-deploy-node@.service",
	"orama-deploy-npm@.service",
	"orama-deploy-go@.service",
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
}

// NewManager creates a new systemd manager
func NewManager(namespaceBase string, logger *zap.Logger) *Manager {
	return &Manager{
		logger:        logger.With(zap.String("component", "systemd-manager")),
		systemdDir:    "/etc/systemd/system",
		namespaceBase: namespaceBase,
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

// Systemctl builds an exec.Command for systemctl, prepending sudo when the
// current process is not running as root.
//
// Everything on a node that drives systemd goes through this. The deployment
// runner had its own copy that called systemctl directly, which works only
// while the process is root — which it is today, and which the hardened
// gateway unit ends. Going through here is what reaches the sudoers rule that
// grants the orama user systemctl over orama-deploy-* units.
func Systemctl(args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("systemctl", args...)
	}
	return exec.Command("sudo", append([]string{"systemctl"}, args...)...)
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
	m.logger.Info("Starting systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := Systemctl("start", svcName)
	m.logger.Debug("Executing systemctl command",
		zap.String("cmd", cmd.String()),
		zap.Strings("args", cmd.Args))

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("Failed to start service",
			zap.String("service", svcName),
			zap.Error(err),
			zap.String("output", string(output)),
			zap.String("cmd", cmd.String()))
		return fmt.Errorf("failed to start %s: %w; output: %s", svcName, err, string(output))
	}

	m.logger.Info("Service started successfully",
		zap.String("service", svcName),
		zap.String("output", string(output)))
	return nil
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
	envFile := filepath.Join(m.namespaceBase, namespace, fmt.Sprintf("%s.env", serviceType))
	_, err := os.Stat(envFile)
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
	cmd := exec.Command("systemctl", "list-units", "--all", "--no-legend", "orama-namespace-*@*.service")
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

	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", pattern)
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

// GenerateEnvFile creates the environment file for a namespace service
func (m *Manager) GenerateEnvFile(namespace, nodeID string, serviceType ServiceType, envVars map[string]string) error {
	envDir := filepath.Join(m.namespaceBase, namespace)
	m.logger.Debug("Creating env directory",
		zap.String("dir", envDir))

	if err := os.MkdirAll(envDir, 0755); err != nil {
		m.logger.Error("Failed to create env directory",
			zap.String("dir", envDir),
			zap.Error(err))
		return fmt.Errorf("failed to create env directory: %w", err)
	}

	envFile := filepath.Join(envDir, fmt.Sprintf("%s.env", serviceType))

	var content strings.Builder
	content.WriteString("# Auto-generated environment file for namespace service\n")
	content.WriteString(fmt.Sprintf("# Namespace: %s\n", namespace))
	content.WriteString(fmt.Sprintf("# Node ID: %s\n", nodeID))
	content.WriteString(fmt.Sprintf("# Service: %s\n\n", serviceType))

	// Always include NODE_ID
	content.WriteString(fmt.Sprintf("NODE_ID=%s\n", nodeID))

	// Add all other environment variables
	for key, value := range envVars {
		content.WriteString(fmt.Sprintf("%s=%s\n", key, value))
	}

	m.logger.Debug("Writing env file",
		zap.String("file", envFile),
		zap.Int("size", content.Len()))

	if err := os.WriteFile(envFile, []byte(content.String()), 0644); err != nil {
		m.logger.Error("Failed to write env file",
			zap.String("file", envFile),
			zap.Error(err))
		return fmt.Errorf("failed to write env file: %w", err)
	}

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
	// Deliberately NOT the sudo-aware Systemctl() helper: `is-active` is a query
	// that needs no privilege, and the sudoers drop-in grants only
	// start/stop/restart/enable for this unit — routing it through sudo makes it
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
// A guard rather than a comment, because the unit files stay on disk for
// rollback: any code that decides what to start by looking for a unit file will
// find these and start them, and they then race orama-namespace-*@index for the
// same ports.
func IsLeftoverHostUnit(name string) bool {
	for _, u := range LeftoverHostUnits {
		if u == name {
			return true
		}
	}
	return name == LeftoverWireGuardUnit || name == LeftoverNameserverUnit
}
