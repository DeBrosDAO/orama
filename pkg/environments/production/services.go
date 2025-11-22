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
func (ssg *SystemdServiceGenerator) GenerateIPFSService(nodeType string, ipfsBinary string) string {
	var ipfsRepoPath string
	if nodeType == "bootstrap" {
		ipfsRepoPath = filepath.Join(ssg.debrosDir, "data", "bootstrap", "ipfs", "repo")
	} else {
		ipfsRepoPath = filepath.Join(ssg.debrosDir, "data", "node", "ipfs", "repo")
	}

	logFile := filepath.Join(ssg.debrosDir, "logs", fmt.Sprintf("ipfs-%s.log", nodeType))

	return fmt.Sprintf(`[Unit]
Description=IPFS Daemon (%[1]s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[2]s
Environment=IPFS_PATH=%[3]s
ExecStartPre=/bin/bash -c 'if [ -f %[4]s/secrets/swarm.key ] && [ ! -f %[3]s/swarm.key ]; then cp %[4]s/secrets/swarm.key %[3]s/swarm.key && chmod 600 %[3]s/swarm.key; fi'
ExecStart=%[6]s daemon --enable-pubsub-experiment --repo-dir=%[3]s
Restart=always
RestartSec=5
StandardOutput=file:%[5]s
StandardError=file:%[5]s
SyslogIdentifier=ipfs-%[1]s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%[4]s

[Install]
WantedBy=multi-user.target
`, nodeType, ssg.debrosHome, ipfsRepoPath, ssg.debrosDir, logFile, ipfsBinary)
}

// GenerateIPFSClusterService generates the IPFS Cluster systemd unit
func (ssg *SystemdServiceGenerator) GenerateIPFSClusterService(nodeType string, clusterBinary string) string {
	var clusterPath string
	if nodeType == "bootstrap" {
		clusterPath = filepath.Join(ssg.debrosDir, "data", "bootstrap", "ipfs-cluster")
	} else {
		clusterPath = filepath.Join(ssg.debrosDir, "data", "node", "ipfs-cluster")
	}

	logFile := filepath.Join(ssg.debrosDir, "logs", fmt.Sprintf("ipfs-cluster-%s.log", nodeType))

	return fmt.Sprintf(`[Unit]
Description=IPFS Cluster Service (%[1]s)
After=debros-ipfs-%[1]s.service
Wants=debros-ipfs-%[1]s.service
Requires=debros-ipfs-%[1]s.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%[2]s
Environment=HOME=%[2]s
Environment=IPFS_CLUSTER_PATH=%[3]s
ExecStart=%[6]s daemon
Restart=always
RestartSec=5
StandardOutput=file:%[4]s
StandardError=file:%[4]s
SyslogIdentifier=ipfs-cluster-%[1]s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%[5]s

[Install]
WantedBy=multi-user.target
`, nodeType, ssg.debrosHome, clusterPath, logFile, ssg.debrosDir, clusterBinary)
}

// GenerateRQLiteService generates the RQLite systemd unit
func (ssg *SystemdServiceGenerator) GenerateRQLiteService(nodeType string, rqliteBinary string, httpPort, raftPort int, joinAddr string, advertiseIP string) string {
	var dataDir string
	if nodeType == "bootstrap" {
		dataDir = filepath.Join(ssg.debrosDir, "data", "bootstrap", "rqlite")
	} else {
		dataDir = filepath.Join(ssg.debrosDir, "data", "node", "rqlite")
	}

	// Use public IP for advertise if provided, otherwise default to localhost
	if advertiseIP == "" {
		advertiseIP = "127.0.0.1"
	}

	args := fmt.Sprintf(
		`-http-addr 0.0.0.0:%d -http-adv-addr %s:%d -raft-adv-addr %s:%d -raft-addr 0.0.0.0:%d`,
		httpPort, advertiseIP, httpPort, advertiseIP, raftPort, raftPort,
	)

	if joinAddr != "" {
		args += fmt.Sprintf(` -join %s -join-attempts 30 -join-interval 10s`, joinAddr)
	}

	args += fmt.Sprintf(` %s`, dataDir)

	logFile := filepath.Join(ssg.debrosDir, "logs", fmt.Sprintf("rqlite-%s.log", nodeType))

	return fmt.Sprintf(`[Unit]
Description=RQLite Database (%[1]s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[2]s
ExecStart=%[6]s %[3]s
Restart=always
RestartSec=5
StandardOutput=file:%[4]s
StandardError=file:%[4]s
SyslogIdentifier=rqlite-%[1]s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%[5]s

[Install]
WantedBy=multi-user.target
`, nodeType, ssg.debrosHome, args, logFile, ssg.debrosDir, rqliteBinary)
}

// GenerateOlricService generates the Olric systemd unit
func (ssg *SystemdServiceGenerator) GenerateOlricService(olricBinary string) string {
	olricConfigPath := filepath.Join(ssg.debrosDir, "configs", "olric", "config.yaml")
	logFile := filepath.Join(ssg.debrosDir, "logs", "olric.log")

	return fmt.Sprintf(`[Unit]
Description=Olric Cache Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[1]s
Environment=OLRIC_SERVER_CONFIG=%[2]s
ExecStart=%[5]s
Restart=always
RestartSec=5
StandardOutput=file:%[3]s
StandardError=file:%[3]s
SyslogIdentifier=olric

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%[4]s

[Install]
WantedBy=multi-user.target
`, ssg.debrosHome, olricConfigPath, logFile, ssg.debrosDir, olricBinary)
}

// GenerateNodeService generates the DeBros Node systemd unit
func (ssg *SystemdServiceGenerator) GenerateNodeService(nodeType string) string {
	var configFile string
	if nodeType == "bootstrap" {
		configFile = "bootstrap.yaml"
	} else {
		configFile = "node.yaml"
	}

	logFile := filepath.Join(ssg.debrosDir, "logs", fmt.Sprintf("node-%s.log", nodeType))

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
StandardOutput=file:%s
StandardError=file:%s
SyslogIdentifier=debros-node-%s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeType, nodeType, nodeType, nodeType, ssg.debrosHome, ssg.debrosHome, ssg.debrosHome, ssg.debrosDir, configFile, logFile, logFile, nodeType, ssg.debrosDir)
}

// GenerateGatewayService generates the DeBros Gateway systemd unit
func (ssg *SystemdServiceGenerator) GenerateGatewayService(nodeType string) string {
	nodeService := fmt.Sprintf("debros-node-%s.service", nodeType)
	olricService := "debros-olric.service"
	logFile := filepath.Join(ssg.debrosDir, "logs", "gateway.log")
	return fmt.Sprintf(`[Unit]
Description=DeBros Gateway
After=%s %s
Wants=%s %s

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%s
Environment=HOME=%s
ExecStart=%s/bin/gateway --config %s/data/gateway.yaml
Restart=always
RestartSec=5
StandardOutput=file:%s
StandardError=file:%s
SyslogIdentifier=debros-gateway

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, nodeService, olricService, nodeService, olricService, ssg.debrosHome, ssg.debrosHome, ssg.debrosHome, ssg.debrosDir, logFile, logFile, ssg.debrosDir)
}

// GenerateAnyoneClientService generates the Anyone Client SOCKS5 proxy systemd unit
func (ssg *SystemdServiceGenerator) GenerateAnyoneClientService() string {
	logFile := filepath.Join(ssg.debrosDir, "logs", "anyone-client.log")

	return fmt.Sprintf(`[Unit]
Description=Anyone Client SOCKS5 Proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[1]s
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/lib/node_modules/.bin
ExecStart=/usr/bin/npx --yes @anyone-protocol/anyone-client
Restart=always
RestartSec=5
StandardOutput=file:%[2]s
StandardError=file:%[2]s
SyslogIdentifier=anyone-client

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=%[3]s

[Install]
WantedBy=multi-user.target
`, ssg.debrosHome, logFile, ssg.debrosDir)
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
