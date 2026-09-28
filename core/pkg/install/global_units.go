package install

import (
	"fmt"
	"strings"

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

	globalProviderUser = "orama-provider"
	globalSBWSUser     = "orama-sbws"
	globalReporterUser = "orama-reporter"
	globalArchiverUser = "orama-archiver"
	globalRepairUser   = "orama-repair"
	globalTorUser      = "debian-tor"

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
PrivatePIDs=yes
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
	gateway := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", constants.GlobalIPFSGatewayPort)
	swarm := fmt.Sprintf("[\"/ip4/0.0.0.0/tcp/%d\",\"/ip4/0.0.0.0/udp/%d/quic-v1\"]",
		constants.GlobalIPFSSwarmPort, constants.GlobalIPFSSwarmPort)
	exec := fmt.Sprintf("%s/ipfs daemon --repo-dir=%s", globalBinDir, globalIPFSHome)
	pre := fmt.Sprintf("ExecStartPre=%s/ipfs config --repo-dir=%s Addresses.API %s\nExecStartPre=%s/ipfs config --repo-dir=%s Addresses.Gateway %s\nExecStartPre=%s/ipfs config --repo-dir=%s --json Addresses.Swarm %s\n",
		globalBinDir, globalIPFSHome, api,
		globalBinDir, globalIPFSHome, gateway,
		globalBinDir, globalIPFSHome, swarm)
	return renderGlobalUnit("Orama public IPFS", globalIPFSUser, globalIPFSHome, exec, pre)
}

// RenderGlobalIPFSGCUnit is the oneshot that garbage-collects the public Kubo
// repo. Its timer is RenderGlobalIPFSGCTimer. Neither is PartOf orama-node.
func RenderGlobalIPFSGCUnit() string {
	exec := fmt.Sprintf("%s/ipfs repo gc --repo-dir=%s", globalBinDir, globalIPFSHome)
	return renderGlobalOneshot("Orama public IPFS garbage collection", globalIPFSUser, "orama-global/ipfs", globalIPFSHome, exec)
}

// RenderGlobalIPFSGCTimer fires the public Kubo GC. It is not tied to orama-node.
func RenderGlobalIPFSGCTimer() string {
	return `[Unit]
Description=Schedule garbage collection of the public Kubo repo
# Restarting orama-node must not restart this timer.

[Timer]
OnActiveSec=20min
OnUnitActiveSec=6h
RandomizedDelaySec=30min
Unit=orama-global-ipfs-gc.service

[Install]
WantedBy=timers.target
`
}

// RenderGlobalProviderUnit is orama-global-provider.service. It may read the
// public Kubo RPC token through the supplementary group, and nothing else
// in that home.
func RenderGlobalProviderUnit() string {
	exec := fmt.Sprintf("%s/orama-global provider --listen 0.0.0.0:%d", globalBinDir, constants.GlobalProviderPort)
	return renderGlobalUnitExtra("Orama storage provider", globalProviderUser, globalProviderUser, "orama-ipfs-pub-rpc",
		"orama-global/provider", "/var/lib/orama-global/provider", exec, "")
}

// RenderGlobalRelayUnit is orama-global-relay.service. Metrics listen on
// loopback only.
func RenderGlobalRelayUnit() string {
	exec := fmt.Sprintf("%s/orama-relay --metrics-addr 127.0.0.1:%d", globalBinDir, constants.GlobalRelayMetricsPort)
	return renderGlobalUnit("Orama relay", globalRelayUser, globalRelayHome, exec, "")
}

func renderGlobalUnit(description, user, home, exec, extra string) string {
	state := strings.TrimPrefix(home, "/var/lib/")
	return renderGlobalUnitExtra(description, user, user, "", state, home, exec, extra)
}

func renderGlobalUnitExtra(description, user, group, supplementary, state, home, exec, extra string) string {
	supp := ""
	if supplementary != "" {
		supp = "SupplementaryGroups=" + supplementary + "\n"
	}
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=%s
Group=%s
%sStateDirectory=%s
StateDirectoryMode=0700
%sReadWritePaths=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5
LimitNOFILE=65535
%s
[Install]
WantedBy=multi-user.target
`, description, user, group, supp, state, extra, home, home, exec, globalSandboxTail)
}

func renderGlobalOneshot(description, user, state, home, exec string) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target

[Service]
Type=oneshot
User=%s
Group=%s
StateDirectory=%s
StateDirectoryMode=0700
WorkingDirectory=%s
ExecStart=%s
%s
`, description, user, user, state, home, exec, globalSandboxTail)
}

// RenderGlobalTorRelayUnit is the public relay. Tor is the distro binary.
// MemoryDenyWriteExecute is safe for tor and is not set on the Go services.
func RenderGlobalTorRelayUnit() string {
	return renderTorUnit("Orama Tor relay", "orama-global/tor-relay",
		fmt.Sprintf("ORPort %d", constants.GlobalTorORPort))
}

// RenderGlobalTorDirauthUnit is a directory authority. It is not started on
// an ordinary global node.
func RenderGlobalTorDirauthUnit() string {
	return renderTorUnit("Orama Tor directory authority", "orama-global/tor-dirauth",
		fmt.Sprintf("ORPort %d\nDirPort %d", constants.GlobalTorORPort, constants.GlobalTorDirPort))
}

// RenderGlobalTorOnionUnit is the validator onion that forwards tx submission
// to the local chain RPC. It publishes no ORPort.
func RenderGlobalTorOnionUnit() string {
	return renderTorUnit("Orama validator tx onion", "orama-global/tor-onion",
		fmt.Sprintf("HiddenServicePort 80 127.0.0.1:%d", constants.ChainRPCPort))
}

func renderTorUnit(description, state, note string) string {
	home := "/var/lib/" + state
	exec := "/usr/bin/tor -f " + home + "/torrc"
	extra := "MemoryDenyWriteExecute=yes\n# " + note + "\n"
	return renderGlobalUnitExtra(description, globalTorUser, globalTorUser, "", state, home, exec, extra)
}

// RenderGlobalSBWSUnit measures relay bandwidth. Dirauth hosts only.
func RenderGlobalSBWSUnit() string {
	return renderGlobalUnit("Orama sbws", globalSBWSUser, "/var/lib/orama-global/sbws", "/usr/bin/sbws generate", "")
}

// RenderGlobalReporterUnit posts bandwidth measurements. Dirauth hosts only.
func RenderGlobalReporterUnit() string {
	exec := fmt.Sprintf("%s/orama-global reporter", globalBinDir)
	return renderGlobalUnit("Orama bandwidth reporter", globalReporterUser, "/var/lib/orama-global/reporter", exec, "")
}

// RenderGlobalArchiverUnit bundles chain history. It can read the chain home
// through orama-chain-ro and cannot write it.
func RenderGlobalArchiverUnit() string {
	exec := fmt.Sprintf("%s/orama-global archiver --chain-home %s", globalBinDir, constants.ChainHome)
	return renderGlobalUnitExtra("Orama history archiver", globalArchiverUser, globalArchiverUser, "orama-chain-ro",
		"orama-global/archiver", "/var/lib/orama-global/archiver", exec, "")
}

// RenderGlobalRepairUnit holds repair seeds. A host that runs it does not
// also run the storage provider.
func RenderGlobalRepairUnit() string {
	exec := fmt.Sprintf("%s/orama-global repair", globalBinDir)
	return renderGlobalUnit("Orama repair delegate", globalRepairUser, "/var/lib/orama-global/repair", exec, "")
}
