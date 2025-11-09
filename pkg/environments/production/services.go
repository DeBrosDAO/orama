package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SystemdServiceGenerator generates systemd unit files
type SystemdServiceGenerator struct {
	debrosHome string
	debrosDir  string
}

// NewSystemdServiceGenerator creates a new service generator
func NewSystemdServiceGenerator(debrosHome, debrosDir string) *SystemdServiceGenerator {
	return &SystemdServiceGenerator{
		debrosHome: debrosHome,
		debrosDir:  debrosDir,
	}
}

// GenerateIPFSService generates the IPFS daemon systemd unit
func (ssg *SystemdServiceGenerator) GenerateIPFSService(nodeType string) string {
	var ipfsRepoPath string
	if nodeType == "bootstrap" {
		ipfsRepoPath = filepath.Join(ssg.debrosDir, "data", "bootstrap", "ipfs", "repo")
	} else {
		ipfsRepoPath = filepath.Join(ssg.debrosDir, "data", "node", "ipfs", "repo")
	}

	return fmt.Sprintf(`[Unit]
Description=IPFS Daemon (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%s
Environment=IPFS_PATH=%s
ExecStartPre=/bin/bash -c 'if [ -f %s/secrets/swarm.key ] && [ ! -f %s/swarm.key ]; then cp %s/secrets/swarm.key %s/swarm.key && chmod 600 %s/swarm.key; fi'
ExecStart=/usr/bin/ipfs daemon --enable-pubsub-experiment --repo-dir=%s
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipfs-%s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeType, ssg.debrosHome, ipfsRepoPath, ssg.debrosDir, ipfsRepoPath, ssg.debrosDir, ipfsRepoPath, ipfsRepoPath, ipfsRepoPath, nodeType, ssg.debrosDir)
}

// GenerateIPFSClusterService generates the IPFS Cluster systemd unit
func (ssg *SystemdServiceGenerator) GenerateIPFSClusterService(nodeType string) string {
	var clusterPath string
	if nodeType == "bootstrap" {
		clusterPath = filepath.Join(ssg.debrosDir, "data", "bootstrap", "ipfs-cluster")
	} else {
		clusterPath = filepath.Join(ssg.debrosDir, "data", "node", "ipfs-cluster")
	}

	return fmt.Sprintf(`[Unit]
Description=IPFS Cluster Service (%s)
After=debros-ipfs-%s.service
Wants=debros-ipfs-%s.service
Requires=debros-ipfs-%s.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%s
Environment=HOME=%s
Environment=CLUSTER_PATH=%s
ExecStart=/usr/local/bin/ipfs-cluster-service daemon --config %s/service.json
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipfs-cluster-%s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeType, nodeType, nodeType, nodeType, ssg.debrosHome, ssg.debrosHome, clusterPath, clusterPath, nodeType, ssg.debrosDir)
}

// GenerateRQLiteService generates the RQLite systemd unit
func (ssg *SystemdServiceGenerator) GenerateRQLiteService(nodeType string, httpPort, raftPort int, joinAddr string) string {
	var dataDir string
	if nodeType == "bootstrap" {
		dataDir = filepath.Join(ssg.debrosDir, "data", "bootstrap", "rqlite")
	} else {
		dataDir = filepath.Join(ssg.debrosDir, "data", "node", "rqlite")
	}

	args := fmt.Sprintf(
		`-http-addr 0.0.0.0:%d -http-adv-addr 127.0.0.1:%d -raft-adv-addr 127.0.0.1:%d -raft-addr 0.0.0.0:%d`,
		httpPort, httpPort, raftPort, raftPort,
	)

	if joinAddr != "" {
		args += fmt.Sprintf(` -join %s -join-attempts 30 -join-interval 10s`, joinAddr)
	}

	args += fmt.Sprintf(` %s`, dataDir)

	return fmt.Sprintf(`[Unit]
Description=RQLite Database (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%s
ExecStart=/usr/local/bin/rqlited %s
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=rqlite-%s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeType, ssg.debrosHome, args, nodeType, ssg.debrosDir)
}

// GenerateOlricService generates the Olric systemd unit
func (ssg *SystemdServiceGenerator) GenerateOlricService() string {
	olricConfigPath := filepath.Join(ssg.debrosDir, "configs", "olric", "config.yaml")

	return fmt.Sprintf(`[Unit]
Description=Olric Cache Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%s
Environment=OLRIC_SERVER_CONFIG=%s
ExecStart=/usr/local/bin/olric-server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=olric

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, ssg.debrosHome, olricConfigPath, ssg.debrosDir)
}

// GenerateNodeService generates the DeBros Node systemd unit
func (ssg *SystemdServiceGenerator) GenerateNodeService(nodeType string) string {
	var configFile string
	if nodeType == "bootstrap" {
		configFile = "bootstrap.yaml"
	} else {
		configFile = "node.yaml"
	}

	return fmt.Sprintf(`[Unit]
Description=DeBros Network Node (%s)
After=debros-ipfs-cluster-%s.service
Wants=debros-ipfs-cluster-%s.service
Requires=debros-ipfs-cluster-%s.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%s
Environment=HOME=%s
ExecStart=%s/bin/node --config %s/configs/%s
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-node-%s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeType, nodeType, nodeType, nodeType, ssg.debrosHome, ssg.debrosHome, ssg.debrosHome, ssg.debrosDir, configFile, nodeType, ssg.debrosDir)
}

// GenerateGatewayService generates the DeBros Gateway systemd unit
func (ssg *SystemdServiceGenerator) GenerateGatewayService(nodeType string) string {
	nodeService := fmt.Sprintf("debros-node-%s.service", nodeType)
	return fmt.Sprintf(`[Unit]
Description=DeBros Gateway
After=%s
Wants=%s

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%s
Environment=HOME=%s
ExecStart=%s/bin/gateway --config %s/configs/gateway.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=debros-gateway

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeService, nodeService, ssg.debrosHome, ssg.debrosHome, ssg.debrosHome, ssg.debrosDir, ssg.debrosDir)
}

// SystemdController manages systemd service operations
type SystemdController struct {
	systemdDir string
}

// NewSystemdController creates a new controller
func NewSystemdController() *SystemdController {
	return &SystemdController{
		systemdDir: "/etc/systemd/system",
	}
}

// WriteServiceUnit writes a systemd unit file
func (sc *SystemdController) WriteServiceUnit(name string, content string) error {
	unitPath := filepath.Join(sc.systemdDir, name)
	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write unit file %s: %w", name, err)
	}
	return nil
}

// DaemonReload reloads the systemd daemon
func (sc *SystemdController) DaemonReload() error {
	cmd := exec.Command("systemctl", "daemon-reload")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %w", err)
	}
	return nil
}

// EnableService enables a service to start on boot
func (sc *SystemdController) EnableService(name string) error {
	cmd := exec.Command("systemctl", "enable", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to enable service %s: %w", name, err)
	}
	return nil
}

// StartService starts a service immediately
func (sc *SystemdController) StartService(name string) error {
	cmd := exec.Command("systemctl", "start", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start service %s: %w", name, err)
	}
	return nil
}

// RestartService restarts a service
func (sc *SystemdController) RestartService(name string) error {
	cmd := exec.Command("systemctl", "restart", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart service %s: %w", name, err)
	}
	return nil
}

// StopService stops a service
func (sc *SystemdController) StopService(name string) error {
	cmd := exec.Command("systemctl", "stop", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}
	return nil
}

// StatusService gets the status of a service
func (sc *SystemdController) StatusService(name string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	// Check for "inactive" vs actual error
	if strings.Contains(err.Error(), "exit status 3") {
		return false, nil // Service is inactive
	}

	return false, fmt.Errorf("failed to check service status %s: %w", name, err)
}
