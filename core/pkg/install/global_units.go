package install

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Global unit accounts and paths. These templates are not installed: install
// still writes only orama-node.service and the orama-namespace-* templates.
// chain/scripts/stagenet/deploy.sh writes its own orama-global-chain unit for
// the stagenet mesh; this one is the global-role unit, with no WireGuard
// dependency and no cluster secret path.
const (
	globalBinDir    = "/usr/lib/orama-global/bin"
	globalChainUser = "orama-chain"
	globalIPFSUser  = "orama-ipfs-pub"
	globalRelayUser = "orama-relay"

	globalIPFSHome  = "/var/lib/orama-global/ipfs"
	globalRelayHome = "/var/lib/orama-global/relay"
)

// globalSandbox is the hardening every global unit shares. It hides /opt/orama
// and the cluster secret locations, and it does not join orama-node.service:
// restarting the supervisor must not restart a chain, the public Kubo, or the
// relay. Private ranges are denied so the unit cannot dial the WireGuard mesh.
const globalSandboxTail = `NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectClock=yes
ProtectKernelLogs=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectHostname=yes
ProtectProc=invisible
RestrictNamespaces=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
RestrictAddressFamilies=AF_INET AF_UNIX
LockPersonality=yes
UMask=0077
CapabilityBoundingSet=
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
TemporaryFileSystem=/opt/orama:ro
InaccessiblePaths=/var/lib/orama-unit-env /etc/wireguard /etc/orama /var/lib/orama-gateway-keys
IPAddressDeny=10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10
IPAddressAllow=localhost
`

// RenderGlobalChainUnit is orama-global-chain.service. p2p, rpc, grpc and the
// REST API are flags oramad's start command registers. CometBFT v0.39's
// AddNodeFlags does not register an instrumentation flag, so the prometheus
// listen address is recorded here and still has to be set in config.toml.
func RenderGlobalChainUnit() string {
	exec := fmt.Sprintf("%s/oramad start --home %s --p2p.laddr tcp://0.0.0.0:%d --rpc.laddr tcp://127.0.0.1:%d --grpc.enable=true --grpc.address 127.0.0.1:%d --api.enable=true --api.address tcp://127.0.0.1:%d",
		globalBinDir, constants.ChainHome, constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort, constants.ChainAPIPort)
	return renderGlobalUnit(
		"Orama L1 node (oramad)",
		globalChainUser,
		constants.ChainHome,
		exec,
		fmt.Sprintf("# CometBFT v0.39 registers no prometheus flag; config.toml prometheus_listen_addr is 127.0.0.1:%d.\n", constants.ChainPrometheusPort),
	)
}

// RenderGlobalIPFSUnit is orama-global-ipfs.service. The API is the public
// Kubo's loopback API, not the cluster daemon on IPFSAPIPort.
func RenderGlobalIPFSUnit() string {
	api := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", constants.GlobalIPFSAPIPort)
	exec := fmt.Sprintf("%s/ipfs daemon --repo-dir=%s", globalBinDir, globalIPFSHome)
	pre := fmt.Sprintf("ExecStartPre=%s/ipfs config --repo-dir=%s Addresses.API %s\n", globalBinDir, globalIPFSHome, api)
	return renderGlobalUnit("Orama public IPFS", globalIPFSUser, globalIPFSHome, exec, pre)
}

// RenderGlobalRelayUnit is orama-global-relay.service. Metrics listen on
// loopback only.
func RenderGlobalRelayUnit() string {
	exec := fmt.Sprintf("%s/orama-relay --metrics-addr 127.0.0.1:%d", globalBinDir, constants.GlobalRelayMetricsPort)
	return renderGlobalUnit("Orama relay", globalRelayUser, globalRelayHome, exec, "")
}

func renderGlobalUnit(description, user, home, exec, extra string) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=%s
Group=%s
%sReadWritePaths=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5
LimitNOFILE=65535
%s
[Install]
WantedBy=multi-user.target
`, description, user, user, extra, home, home, exec, globalSandboxTail)
}
