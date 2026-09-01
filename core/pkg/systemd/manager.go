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
	ServiceTypeRQLite  ServiceType = "rqlite"
	ServiceTypeOlric   ServiceType = "olric"
	ServiceTypeGateway ServiceType = "gateway"
	ServiceTypeSFU     ServiceType = "sfu"
	ServiceTypeTURN    ServiceType = "turn"
	ServiceTypePubsub  ServiceType = "pubsub"
)

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

// serviceName returns the systemd service name for a namespace and service type
func (m *Manager) serviceName(namespace string, serviceType ServiceType) string {
	return fmt.Sprintf("orama-namespace-%s@%s.service", serviceType, namespace)
}

// systemctl builds an exec.Command for systemctl, prepending sudo when
// the current process is not running as root.
func systemctl(args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("systemctl", args...)
	}
	return exec.Command("sudo", append([]string{"systemctl"}, args...)...)
}

// StartService starts a namespace service
func (m *Manager) StartService(namespace string, serviceType ServiceType) error {
	svcName := m.serviceName(namespace, serviceType)
	m.logger.Info("Starting systemd service",
		zap.String("service", svcName),
		zap.String("namespace", namespace))

	cmd := systemctl("start", svcName)
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

	cmd := systemctl("stop", svcName)
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

	cmd := systemctl("restart", svcName)
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

	cmd := systemctl("enable", svcName)
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

	cmd := systemctl("disable", svcName)
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
	cmd := systemctl("daemon-reload")
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
		cmd := systemctl("stop", svc)
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
// Deployment units follow the naming pattern: orama-deploy-{namespace}-{name}.service
// (with dots replaced by hyphens, matching process/manager.go:getServiceName).
// This is best-effort: individual failures are logged but do not abort the operation.
func (m *Manager) StopDeploymentServicesForNamespace(namespace string) {
	// Match the sanitization from deployments/process/manager.go:getServiceName
	sanitizedNS := strings.ReplaceAll(namespace, ".", "-")
	pattern := fmt.Sprintf("orama-deploy-%s-*", sanitizedNS)

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
		if stopOut, stopErr := systemctl("stop", svc).CombinedOutput(); stopErr != nil {
			m.logger.Warn("Failed to stop deployment service",
				zap.String("service", svc),
				zap.Error(stopErr),
				zap.String("output", string(stopOut)))
		}

		// Disable the service
		if disOut, disErr := systemctl("disable", svc).CombinedOutput(); disErr != nil {
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

	templates := []string{
		"orama-namespace-rqlite@.service",
		"orama-namespace-olric@.service",
		"orama-namespace-gateway@.service",
		"orama-namespace-sfu@.service",
		"orama-namespace-turn@.service",
		"orama-namespace-pubsub@.service",
	}

	for _, template := range templates {
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
