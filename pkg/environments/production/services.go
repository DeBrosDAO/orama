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
	oramaHome string
	oramaDir  string
}

// NewSystemdServiceGenerator creates a new service generator
func NewSystemdServiceGenerator(oramaHome, oramaDir string) *SystemdServiceGenerator {
	return &SystemdServiceGenerator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
	}
}

// GenerateIPFSService generates the IPFS daemon systemd unit
func (ssg *SystemdServiceGenerator) GenerateIPFSService(ipfsBinary string) string {
	ipfsRepoPath := filepath.Join(ssg.oramaDir, "data", "ipfs", "repo")
	logFile := filepath.Join(ssg.oramaDir, "logs", "ipfs.log")

	return fmt.Sprintf(`[Unit]
Description=IPFS Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[1]s
Environment=IPFS_PATH=%[2]s
ExecStartPre=/bin/bash -c 'if [ -f %[3]s/secrets/swarm.key ] && [ ! -f %[2]s/swarm.key ]; then cp %[3]s/secrets/swarm.key %[2]s/swarm.key && chmod 600 %[2]s/swarm.key; fi'
ExecStart=%[5]s daemon --enable-pubsub-experiment --repo-dir=%[2]s
Restart=always
RestartSec=5
StandardOutput=append:%[4]s
StandardError=append:%[4]s
SyslogIdentifier=debros-ipfs

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[3]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ipfsRepoPath, ssg.oramaDir, logFile, ipfsBinary)
}

// GenerateIPFSClusterService generates the IPFS Cluster systemd unit
func (ssg *SystemdServiceGenerator) GenerateIPFSClusterService(clusterBinary string) string {
	clusterPath := filepath.Join(ssg.oramaDir, "data", "ipfs-cluster")
	logFile := filepath.Join(ssg.oramaDir, "logs", "ipfs-cluster.log")

	// Read cluster secret from file to pass to daemon
	clusterSecretPath := filepath.Join(ssg.oramaDir, "secrets", "cluster-secret")
	clusterSecret := ""
	if data, err := os.ReadFile(clusterSecretPath); err == nil {
		clusterSecret = strings.TrimSpace(string(data))
	}

	return fmt.Sprintf(`[Unit]
Description=IPFS Cluster Service
After=debros-ipfs.service
Wants=debros-ipfs.service
Requires=debros-ipfs.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
Environment=IPFS_CLUSTER_PATH=%[2]s
Environment=CLUSTER_SECRET=%[5]s
ExecStartPre=/bin/bash -c 'mkdir -p %[2]s && chmod 700 %[2]s'
ExecStartPre=/bin/bash -c 'for i in $(seq 1 30); do curl -sf -X POST http://127.0.0.1:4501/api/v0/id > /dev/null 2>&1 && exit 0; sleep 1; done; echo "IPFS API not ready after 30s"; exit 1'
ExecStart=%[4]s daemon
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=debros-ipfs-cluster

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[1]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, clusterPath, logFile, clusterBinary, clusterSecret)
}

// GenerateRQLiteService generates the RQLite systemd unit
func (ssg *SystemdServiceGenerator) GenerateRQLiteService(rqliteBinary string, httpPort, raftPort int, joinAddr string, advertiseIP string) string {
	dataDir := filepath.Join(ssg.oramaDir, "data", "rqlite")
	logFile := filepath.Join(ssg.oramaDir, "logs", "rqlite.log")

	// Use public IP for advertise if provided, otherwise default to localhost
	if advertiseIP == "" {
		advertiseIP = "127.0.0.1"
	}

	// Bind RQLite to localhost only - external access via SNI gateway
	args := fmt.Sprintf(
		`-http-addr 127.0.0.1:%d -http-adv-addr %s:%d -raft-adv-addr %s:%d -raft-addr 127.0.0.1:%d`,
		httpPort, advertiseIP, httpPort, advertiseIP, raftPort, raftPort,
	)

	if joinAddr != "" {
		args += fmt.Sprintf(` -join %s -join-attempts 30 -join-interval 10s`, joinAddr)
	}

	args += fmt.Sprintf(` %s`, dataDir)

	return fmt.Sprintf(`[Unit]
Description=RQLite Database
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debros
Group=debros
Environment=HOME=%[1]s
ExecStart=%[5]s %[2]s
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=debros-rqlite

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[4]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, args, logFile, dataDir, rqliteBinary)
}

// GenerateOlricService generates the Olric systemd unit
func (ssg *SystemdServiceGenerator) GenerateOlricService(olricBinary string) string {
	olricConfigPath := filepath.Join(ssg.oramaDir, "configs", "olric", "config.yaml")
	logFile := filepath.Join(ssg.oramaDir, "logs", "olric.log")

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
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=olric

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[4]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, olricConfigPath, logFile, ssg.oramaDir, olricBinary)
}

// GenerateNodeService generates the DeBros Node systemd unit
func (ssg *SystemdServiceGenerator) GenerateNodeService() string {
	configFile := "node.yaml"
	logFile := filepath.Join(ssg.oramaDir, "logs", "node.log")
	// Note: systemd StandardOutput/StandardError paths should not contain substitution variables
	// Use absolute paths directly as they will be resolved by systemd at runtime

	return fmt.Sprintf(`[Unit]
Description=DeBros Network Node
After=debros-ipfs-cluster.service debros-olric.service
Wants=debros-ipfs-cluster.service debros-olric.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
ExecStart=%[1]s/bin/orama-node --config %[2]s/configs/%[3]s
Restart=always
RestartSec=5
StandardOutput=append:%[4]s
StandardError=append:%[4]s
SyslogIdentifier=debros-node

PrivateTmp=yes
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
ReadWritePaths=%[2]s /etc/systemd/system

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ssg.oramaDir, configFile, logFile)
}

// GenerateGatewayService generates the DeBros Gateway systemd unit
func (ssg *SystemdServiceGenerator) GenerateGatewayService() string {
	logFile := filepath.Join(ssg.oramaDir, "logs", "gateway.log")
	return fmt.Sprintf(`[Unit]
Description=DeBros Gateway
After=debros-node.service debros-olric.service
Wants=debros-node.service debros-olric.service

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
ExecStart=%[1]s/bin/gateway --config %[2]s/data/gateway.yaml
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=debros-gateway

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Note: NoNewPrivileges is omitted because it conflicts with AmbientCapabilities
# The service needs CAP_NET_BIND_SERVICE to bind to privileged ports (80, 443)
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[2]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ssg.oramaDir, logFile)
}

// GenerateAnyoneClientService generates the Anyone Client SOCKS5 proxy systemd unit
func (ssg *SystemdServiceGenerator) GenerateAnyoneClientService() string {
	logFile := filepath.Join(ssg.oramaDir, "logs", "anyone-client.log")

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
WorkingDirectory=%[1]s
ExecStart=/usr/bin/npx anyone-client
Restart=always
RestartSec=5
StandardOutput=append:%[2]s
StandardError=append:%[2]s
SyslogIdentifier=anyone-client

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=no
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
ReadWritePaths=%[3]s

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, logFile, ssg.oramaDir)
}

// GenerateAnyoneRelayService generates the Anyone Relay operator systemd unit
// Uses debian-anon user created by the anon apt package
func (ssg *SystemdServiceGenerator) GenerateAnyoneRelayService() string {
	return `[Unit]
Description=Anyone Relay (Orama Network)
Documentation=https://docs.anyone.io
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=debian-anon
Group=debian-anon
ExecStart=/usr/bin/anon --agree-to-terms
Restart=always
RestartSec=10
SyslogIdentifier=anon-relay

# Security hardening
NoNewPrivileges=yes
ProtectSystem=full
ProtectHome=read-only
PrivateTmp=yes
ReadWritePaths=/var/lib/anon /var/log/anon /etc/anon

[Install]
WantedBy=multi-user.target
`
}

// GenerateCoreDNSService generates the CoreDNS systemd unit
func (ssg *SystemdServiceGenerator) GenerateCoreDNSService() string {
	return `[Unit]
Description=CoreDNS DNS Server with RQLite backend
Documentation=https://coredns.io
After=network-online.target debros-node.service
Wants=network-online.target debros-node.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile
Restart=on-failure
RestartSec=5
SyslogIdentifier=coredns

NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
`
}

// GenerateCaddyService generates the Caddy systemd unit for SSL/TLS
func (ssg *SystemdServiceGenerator) GenerateCaddyService() string {
	return `[Unit]
Description=Caddy HTTP/2 Server
Documentation=https://caddyserver.com/docs/
After=network-online.target debros-node.service coredns.service
Wants=network-online.target
Requires=debros-node.service

[Service]
Type=simple
User=caddy
Group=caddy
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile
TimeoutStopSec=5s
LimitNOFILE=1048576
LimitNPROC=512
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=on-failure
RestartSec=5
SyslogIdentifier=caddy

[Install]
WantedBy=multi-user.target
`
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
