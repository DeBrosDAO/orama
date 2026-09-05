package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// oramaServiceHardening contains common systemd security directives for orama services.
const oramaServiceHardening = `User=orama
Group=orama
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictNamespaces=yes
ProtectProc=invisible`

// anyoneClientHardening is the host-service block for debian-anon (bugboard #244).
const anyoneClientHardening = `User=debian-anon
Group=debian-anon
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictNamespaces=yes
ProtectProc=invisible`

// oramaNodeHardening is like oramaServiceHardening but WITHOUT NoNewPrivileges.
// The node process (which includes the gateway) needs to use sudo to manage
// namespace systemd services. NoNewPrivileges prevents sudo from working.
const oramaNodeHardening = `User=orama
Group=orama
ProtectSystem=strict
ProtectHome=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectProc=invisible
ProtectKernelModules=yes
RestrictNamespaces=yes`

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

	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")
	return fmt.Sprintf(`[Unit]
Description=IPFS Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%[6]s
ReadWritePaths=%[2]s %[7]s
ReadOnlyPaths=%[8]s
Environment=HOME=%[1]s
Environment=IPFS_PATH=%[2]s
ExecStartPre=/bin/bash -c 'if [ -f %[8]s/swarm.key ] && [ ! -f %[2]s/swarm.key ]; then cp %[8]s/swarm.key %[2]s/swarm.key && chmod 600 %[2]s/swarm.key; fi'
ExecStart=%[5]s daemon --enable-pubsub-experiment --repo-dir=%[2]s
Restart=always
RestartSec=5
StandardOutput=append:%[4]s
StandardError=append:%[4]s
SyslogIdentifier=orama-ipfs

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=4G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ipfsRepoPath, ssg.oramaDir, logFile, ipfsBinary, oramaServiceHardening, logsDir, secretsDir)
}

// IPFS garbage-collection schedule. The daemon runs WITHOUT --enable-gc by
// design (see GenerateIPFSService), so GC cadence is owned here: a one-shot
// `ipfs repo gc` driven by a timer, off the request path and staggered across
// nodes so the cluster never GCs in lockstep.
const (
	// ipfsGCOnBootSec delays the first GC after boot so the daemon + cluster stabilize.
	ipfsGCOnBootSec = "20min"
	// ipfsGCInterval is how often the scheduled GC runs after the first.
	ipfsGCInterval = "6h"
	// ipfsGCRandomizedDelaySec staggers GC across nodes (avoids a correlated cluster-wide stall).
	ipfsGCRandomizedDelaySec = "30min"
)

// GenerateIPFSGCService generates the one-shot unit that runs `ipfs repo gc`,
// triggered by orama-ipfs-gc.timer. GC reclaims disk from blocks that have been
// unpinned (e.g. by a tenant's retention/prune job). Without it, unpinned blocks
// accumulate forever because the daemon runs without in-process GC.
func (ssg *SystemdServiceGenerator) GenerateIPFSGCService(ipfsBinary string) string {
	ipfsRepoPath := filepath.Join(ssg.oramaDir, "data", "ipfs", "repo")
	logFile := filepath.Join(ssg.oramaDir, "logs", "ipfs-gc.log")
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")

	// No [Install] section: the unit is triggered by the timer, never enabled directly.
	return fmt.Sprintf(`[Unit]
Description=IPFS repo garbage collection (one-shot)
After=orama-ipfs.service
Requires=orama-ipfs.service

[Service]
Type=oneshot
%[5]s
ReadWritePaths=%[2]s %[7]s
InaccessiblePaths=%[8]s
Environment=HOME=%[1]s
Environment=IPFS_PATH=%[2]s
ExecStart=%[4]s repo gc
TimeoutStartSec=1800
StandardOutput=append:%[6]s
StandardError=append:%[6]s
SyslogIdentifier=orama-ipfs-gc
`, ssg.oramaHome, ipfsRepoPath, ssg.oramaDir, ipfsBinary, oramaServiceHardening, logFile, logsDir, secretsDir)
}

// GenerateIPFSGCTimer generates the timer that triggers orama-ipfs-gc.service.
func (ssg *SystemdServiceGenerator) GenerateIPFSGCTimer() string {
	return fmt.Sprintf(`[Unit]
Description=Schedule periodic IPFS repo garbage collection

[Timer]
OnBootSec=%[1]s
OnUnitActiveSec=%[2]s
RandomizedDelaySec=%[3]s
Unit=orama-ipfs-gc.service

[Install]
WantedBy=timers.target
`, ipfsGCOnBootSec, ipfsGCInterval, ipfsGCRandomizedDelaySec)
}

// GenerateIPFSClusterService generates the IPFS Cluster systemd unit.
// Refuses to emit a unit with an empty CLUSTER_SECRET (bugboard #109).
func (ssg *SystemdServiceGenerator) GenerateIPFSClusterService(clusterBinary string) (string, error) {
	clusterPath := filepath.Join(ssg.oramaDir, "data", "ipfs-cluster")
	logFile := filepath.Join(ssg.oramaDir, "logs", "ipfs-cluster.log")
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")

	clusterSecretPath := filepath.Join(ssg.oramaDir, "secrets", "cluster-secret")
	data, err := os.ReadFile(clusterSecretPath)
	if err != nil {
		return "", fmt.Errorf("read cluster-secret: %w", err)
	}
	clusterSecret := strings.TrimSpace(string(data))
	if clusterSecret == "" {
		return "", fmt.Errorf("cluster-secret is empty; refusing to start IPFS Cluster without CLUSTER_SECRET")
	}

	return fmt.Sprintf(`[Unit]
Description=IPFS Cluster Service
After=orama-ipfs.service
Wants=orama-ipfs.service
Requires=orama-ipfs.service

[Service]
Type=simple
%[6]s
ReadWritePaths=%[2]s %[8]s
InaccessiblePaths=%[9]s
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
Environment=IPFS_CLUSTER_PATH=%[2]s
Environment=CLUSTER_SECRET=%[5]s
ExecStartPre=/bin/bash -c 'mkdir -p %[2]s && chmod 700 %[2]s'
ExecStartPre=/bin/bash -c 'for i in $(seq 1 30); do curl -sf -X POST http://127.0.0.1:10107/api/v0/id > /dev/null 2>&1 && exit 0; sleep 1; done; echo "IPFS API not ready after 30s"; exit 1'
ExecStart=%[4]s daemon
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=orama-ipfs-cluster

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=2G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, clusterPath, logFile, clusterBinary, clusterSecret, oramaServiceHardening, ssg.oramaDir, logsDir, secretsDir), nil
}

// GenerateRQLiteService generates the RQLite systemd unit
func (ssg *SystemdServiceGenerator) GenerateRQLiteService(rqliteBinary string, httpPort, raftPort int, joinAddr string, advertiseIP string) string {
	dataDir := filepath.Join(ssg.oramaDir, "data", "rqlite")
	logFile := filepath.Join(ssg.oramaDir, "logs", "rqlite.log")
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")

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
%[6]s
ReadWritePaths=%[4]s %[8]s
InaccessiblePaths=%[9]s
Environment=HOME=%[1]s
ExecStart=%[5]s %[2]s
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=orama-rqlite

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, args, logFile, dataDir, rqliteBinary, oramaServiceHardening, ssg.oramaDir, logsDir, secretsDir)
}

// GenerateOlricService generates the Olric systemd unit
func (ssg *SystemdServiceGenerator) GenerateOlricService(olricBinary string) string {
	olricConfigPath := filepath.Join(ssg.oramaDir, "configs", "olric", "config.yaml")
	olricConfigDir := filepath.Join(ssg.oramaDir, "configs", "olric")
	logFile := filepath.Join(ssg.oramaDir, "logs", "olric.log")
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")

	return fmt.Sprintf(`[Unit]
Description=Olric Cache Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%[6]s
ReadWritePaths=%[7]s %[8]s
InaccessiblePaths=%[9]s
Environment=HOME=%[1]s
Environment=OLRIC_SERVER_CONFIG=%[2]s
ExecStart=%[5]s
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=olric

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=4G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, olricConfigPath, logFile, ssg.oramaDir, olricBinary, oramaServiceHardening, olricConfigDir, logsDir, secretsDir)
}

// GenerateNodeService generates the Orama Node systemd unit
func (ssg *SystemdServiceGenerator) GenerateNodeService() string {
	configFile := "node.yaml"
	logFile := filepath.Join(ssg.oramaDir, "logs", "node.log")
	// Note: systemd StandardOutput/StandardError paths should not contain substitution variables
	// Use absolute paths directly as they will be resolved by systemd at runtime

	return fmt.Sprintf(`[Unit]
Description=Orama Network Node
After=network-online.target
Wants=network-online.target
# The node no longer exits because the cluster is unreachable — it degrades and
# keeps converging — so a restart now means a genuine crash. Disabling the start
# limit explicitly keeps systemd from giving up on a node that crash-looped for
# an unrelated reason, which would otherwise leave it in failed state until an
# operator noticed. StartLimitIntervalSec belongs in [Unit], not [Service].
StartLimitIntervalSec=0

[Service]
Type=simple
%[5]s
AmbientCapabilities=CAP_NET_ADMIN
# /etc/wireguard so the peer sync can persist mesh membership to wg0.conf.
# Without it the sync applied peers to the running interface and silently failed
# to write the file, so every wg-quick up after a reboot brought the mesh back
# as it was at install time.
ReadWritePaths=%[2]s /etc/wireguard
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
ExecStart=%[1]s/bin/orama-node --config %[2]s/configs/%[3]s
Restart=always
RestartSec=5
StandardOutput=append:%[4]s
StandardError=append:%[4]s
SyslogIdentifier=orama-node

PrivateTmp=yes
LimitNOFILE=65536
# Shutdown is: announce maintenance, wait up to 10s for the boot supervisor to
# leave its current attempt, then tear services down and hand raft leadership
# over. 60s leaves that sequence room; at 30s systemd used to SIGKILL right as
# the leadership transfer began.
TimeoutStopSec=60
KillMode=mixed
MemoryMax=8G
MemorySwapMax=0
OOMScoreAdjust=-500

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ssg.oramaDir, configFile, logFile, oramaNodeHardening)
}

// GenerateVaultService generates the Orama Vault Guardian systemd unit.
// The vault guardian runs on every node, storing Shamir secret shares.
// It binds to the WireGuard overlay only (no public exposure).
func (ssg *SystemdServiceGenerator) GenerateVaultService() string {
	logFile := filepath.Join(ssg.oramaDir, "logs", "vault.log")
	dataDir := filepath.Join(ssg.oramaDir, "data", "vault")

	return fmt.Sprintf(`[Unit]
Description=Orama Vault Guardian
After=network-online.target wg-quick@wg0.service
Wants=network-online.target
Requires=wg-quick@wg0.service
PartOf=orama-node.service

[Service]
Type=simple
User=orama
Group=orama
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictNamespaces=yes
ReadWritePaths=%[2]s
ExecStart=%[1]s/bin/vault-guardian --config %[2]s/vault.yaml
Restart=on-failure
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=orama-vault

PrivateTmp=yes
LimitMEMLOCK=67108864
MemoryMax=512M
MemorySwapMax=0
TimeoutStopSec=30
KillMode=mixed

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, dataDir, logFile)
}

// GenerateGatewayService generates the Orama Gateway systemd unit
func (ssg *SystemdServiceGenerator) GenerateGatewayService() string {
	logFile := filepath.Join(ssg.oramaDir, "logs", "gateway.log")
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	dataDir := filepath.Join(ssg.oramaDir, "data")
	configsDir := filepath.Join(ssg.oramaDir, "configs")
	tlsCache := filepath.Join(ssg.oramaDir, "tls-cache")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")
	return fmt.Sprintf(`[Unit]
Description=Orama Gateway
After=orama-node.service orama-olric.service
Wants=orama-node.service orama-olric.service

[Service]
Type=simple
%[4]s
ReadWritePaths=%[5]s %[6]s %[7]s %[8]s
ReadOnlyPaths=%[9]s
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
ExecStart=%[1]s/bin/gateway --config %[2]s/data/gateway.yaml
Restart=always
RestartSec=5
StandardOutput=append:%[3]s
StandardError=append:%[3]s
SyslogIdentifier=orama-gateway

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=4G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ssg.oramaDir, logFile, oramaServiceHardening, dataDir, logsDir, configsDir, tlsCache, secretsDir)
}

// GenerateAnyoneClientService generates the Anyone Client SOCKS5 proxy systemd unit.
// Uses the same anon binary as the relay, but with a client-only config (SocksPort only, no relay).
func (ssg *SystemdServiceGenerator) GenerateAnyoneClientService() string {
	logFile := filepath.Join(ssg.oramaDir, "logs", "anyone-client.log")

	return fmt.Sprintf(`[Unit]
Description=Anyone Client SOCKS5 Proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%[2]s
ExecStart=/usr/bin/anon -f /etc/anon/anonrc
Restart=on-failure
RestartSec=5
StandardOutput=append:%[1]s
StandardError=append:%[1]s
SyslogIdentifier=anyone-client

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=1G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, logFile, anyoneClientHardening)
}

// GenerateCoreDNSService generates the CoreDNS systemd unit
func (ssg *SystemdServiceGenerator) GenerateCoreDNSService() string {
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")
	return fmt.Sprintf(`[Unit]
Description=CoreDNS DNS Server with RQLite backend
Documentation=https://coredns.io
After=network-online.target orama-node.service
Wants=network-online.target orama-node.service

[Service]
Type=simple
%[1]s
ReadWritePaths=%[2]s
InaccessiblePaths=%[3]s
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile
Restart=on-failure
RestartSec=5
SyslogIdentifier=coredns

PrivateTmp=yes
LimitNOFILE=65536
TimeoutStopSec=30
KillMode=mixed
MemoryMax=1G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, oramaServiceHardening, logsDir, secretsDir)
}

// GenerateCaddyService generates the Caddy systemd unit for SSL/TLS
func (ssg *SystemdServiceGenerator) GenerateCaddyService() string {
	logsDir := filepath.Join(ssg.oramaDir, "logs")
	secretsDir := filepath.Join(ssg.oramaDir, "secrets")
	return fmt.Sprintf(`[Unit]
Description=Caddy HTTP/2 Server
Documentation=https://caddyserver.com/docs/
After=network-online.target orama-node.service coredns.service
Wants=network-online.target
Requires=orama-node.service

[Service]
Type=simple
%[1]s
ReadWritePaths=%[2]s /var/lib/caddy /etc/caddy
InaccessiblePaths=%[3]s
Environment=XDG_DATA_HOME=/var/lib/caddy
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
ExecStartPre=/bin/sh -c 'for i in $$(seq 1 30); do curl -so /dev/null http://localhost:10104/health 2>/dev/null && exit 0; sleep 2; done; echo "Gateway not ready after 60s"; exit 1'
ExecStartPre=/bin/sh -c 'DOMAIN=$$(grep -oP "^\*\\.\K[^ {]+" /etc/caddy/Caddyfile | tail -1); [ -z "$$DOMAIN" ] && exit 0; for i in $$(seq 1 30); do dig +short +timeout=2 "$$DOMAIN" SOA 2>/dev/null | grep -q . && exit 0; sleep 2; done; echo "DNS not resolving $$DOMAIN after 60s (ACME may fail)"; exit 0'
TimeoutStartSec=180
ExecStart=/usr/bin/caddy run --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile
TimeoutStopSec=5s
LimitNOFILE=1048576
LimitNPROC=512
PrivateTmp=true
Restart=on-failure
RestartSec=5
SyslogIdentifier=caddy
KillMode=mixed
MemoryMax=2G
MemorySwapMax=0

[Install]
WantedBy=multi-user.target
`, oramaServiceHardening, logsDir, secretsDir)
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

// DisableService disables a service from starting on boot
func (sc *SystemdController) DisableService(name string) error {
	cmd := exec.Command("systemctl", "disable", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to disable service %s: %w", name, err)
	}
	return nil
}

// RemoveServiceUnit removes a systemd unit file from disk
func (sc *SystemdController) RemoveServiceUnit(name string) error {
	unitPath := filepath.Join(sc.systemdDir, name)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove unit file %s: %w", name, err)
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

// InstallNamespaceTemplates installs the systemd template units the namespace
// services are instantiated from.
//
// This must run BEFORE anything starts orama-node: the supervisor's first act
// is to start orama-namespace-wireguard@index, and with no template installed
// systemd answers "Unit ... not found", the supervisor exits, and systemd
// restarts it. Install used to depend on that retry loop to converge — it
// worked, so nobody noticed the ordering was backwards.
//
// Every template is required, and the error names the one that failed. The two
// byte-identical copies this replaces logged a warning and `continue`d past
// each read or write error, so a node missing half its templates finished
// installing and reported success; and when every template failed, the
// installed count stayed zero, the daemon-reload was skipped, and the function
// returned nil — total failure, reported as success.
func (ps *ProductionSetup) InstallNamespaceTemplates() error {
	sourceDir := OramaSystemdDir
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		sourceDir = filepath.Join(ps.oramaHome, "src", "systemd")
	}
	if _, err := os.Stat(sourceDir); err != nil {
		return fmt.Errorf("no systemd template directory at %s or %s: %w",
			OramaSystemdDir, filepath.Join(ps.oramaHome, "src", "systemd"), err)
	}

	// The manager logs each template; the operator-facing summary is ours.
	if err := systemd.NewManager("", zap.NewNop()).InstallTemplateUnits(sourceDir); err != nil {
		return fmt.Errorf("install namespace systemd templates from %s: %w", sourceDir, err)
	}

	ps.logf("  ✓ Installed %d namespace template units from %s (daemon reloaded)",
		len(systemd.UnitFilesToInstall()), sourceDir)
	return nil
}
