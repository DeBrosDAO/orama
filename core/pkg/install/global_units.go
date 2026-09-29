package install

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
)

// Global unit accounts and paths. `orama global install` (InstallGlobal)
// writes the chain (RenderGlobalChainUnit, under cosmovisor), public Kubo and
// its GC timer, provider, archiver, indexer and repair units; the cluster
// install writes none of them, and the relay, Tor, sbws and reporter
// renderers here are not installed by anything yet.
// chain/scripts/stagenet/deploy.sh writes its own orama-global-chain unit for
// the stagenet mesh; this one is the global-role unit, with no WireGuard
// dependency and no cluster secret path.
const (
	globalBinDir = constants.GlobalBinDir
	// globalCosmovisor is where `orama global install` puts the pinned cosmovisor.
	globalCosmovisor = globalBinDir + "/" + constants.CosmovisorBinary
	globalChainUser  = constants.ChainUser
	globalIPFSUser   = "orama-ipfs-pub"
	globalRelayUser  = "orama-relay"

	globalProviderUser = "orama-provider"
	globalSBWSUser     = "orama-sbws"
	globalReporterUser = "orama-reporter"
	globalArchiverUser = "orama-archiver"
	globalRepairUser   = "orama-repair"
	globalIndexerUser  = "orama-indexer"
	globalTorUser      = "debian-tor"

	globalIPFSHome  = constants.GlobalIPFSHome
	globalRelayHome = constants.GlobalRelayHome
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

// RenderGlobalChainUnit is orama-global-chain.service. oramad runs under
// cosmovisor: the unit starts cosmovisor, which runs
// ChainHome/cosmovisor/current/bin/oramad with the arguments after "run" and
// switches current to a staged upgrades/<name> binary at the upgrade height.
// It never downloads a binary (pkg/cosmovisor stages them, verified).
// genesis/ and upgrades/ are mounted read-only for the unit, so neither
// cosmovisor nor oramad can change a staged binary; cosmovisor/ itself
// stays writable for current, and upgrade-info.json is a link into the
// home (see pkg/cosmovisor).
// p2p, rpc, grpc and the REST API are flags oramad's start command
// registers. CometBFT v0.39's AddNodeFlags does not register an
// instrumentation flag, so the prometheus listen address is recorded here
// and still has to be set in config.toml.
// persistentPeers (id@host:port,...) is passed to oramad as
// --p2p.persistent_peers when set; ValidatePersistentPeers checks it first.
// Before every start, at boot, on Restart=always and on a manual start,
// the unit runs the double-sign guard (GlobalSignFloorCheck) as root.
func RenderGlobalChainUnit(persistentPeers string) string {
	exec := globalCosmovisor + " run " + chainStartArgs()
	if persistentPeers != "" {
		exec += " --p2p.persistent_peers " + persistentPeers
	}
	env := fmt.Sprintf("Environment=DAEMON_NAME=%s\nEnvironment=DAEMON_HOME=%s\nEnvironment=DAEMON_ALLOW_DOWNLOAD_BINARIES=false\nEnvironment=DAEMON_RESTART_AFTER_UPGRADE=true\nReadOnlyPaths=%s\n",
		constants.ChainDaemonName, constants.ChainHome,
		strings.Join(cosmovisor.Layout{Home: constants.ChainHome}.ReadOnlyDirs(), " "))
	return renderGlobalUnit(
		"Orama L1 node (oramad under cosmovisor)",
		globalChainUser,
		constants.ChainHome,
		exec,
		env+"ExecStartPre=+"+GlobalSignFloorCheck+"\n"+chainPrometheusNote(),
	)
}

// GlobalSignFloorCheck is the double-sign guard the chain unit runs,
// as root ("+"), before every start: at boot, on Restart=always, and on a
// manual start, not only through `orama global start`. It is the orama CLI
// that `orama global install` puts in GlobalBinDir beside oramad, and it
// fails when a migrated key's state is behind its floor or the key has moved
// to another host.
const GlobalSignFloorCheck = globalBinDir + "/" + globalOramaCLI + " global validator check-sign-floor"

// needsChain orders a service after the local chain, whose RPC it uses.
func needsChain(unit string) string {
	dep := "After=network-online.target " + constants.ChainServiceUnit + "\nWants=network-online.target " + constants.ChainServiceUnit + "\n"
	return strings.Replace(unit, "After=network-online.target\nWants=network-online.target\n", dep, 1)
}

// needsIPFS orders a service after the public Kubo, whose RPC it pins through.
// It is Wants=, not Requires=: with Kubo down the provider still starts, a
// public slot's pin fails and is retried every step, and the slot is declined
// shortly before its accept window closes.
func needsIPFS(unit string) string {
	return strings.Replace(unit, constants.ChainServiceUnit+"\n", constants.ChainServiceUnit+" "+constants.GlobalIPFSUnit+"\n", 2)
}

// chainStartArgs is oramad's start command with the chain's listeners: p2p
// public, everything else on loopback.
func chainStartArgs() string {
	return fmt.Sprintf("start --home %s --p2p.laddr tcp://0.0.0.0:%d --rpc.laddr tcp://127.0.0.1:%d --grpc.enable=true --grpc.address 127.0.0.1:%d --api.enable=true --api.address tcp://127.0.0.1:%d",
		constants.ChainHome, constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort, constants.ChainAPIPort)
}

func chainPrometheusNote() string {
	return fmt.Sprintf("# CometBFT v0.39 registers no prometheus flag; config.toml prometheus_listen_addr is 127.0.0.1:%d.\n", constants.ChainPrometheusPort)
}

// globalIPFSGCEnvFile is the GC oneshot's environment file in the public
// Kubo home, mode 0600, the Kubo user's. It holds IPFS_API_AUTH, the bearer
// the daemon's RPC requires. The GC unit passes it to ipfs as --api-auth, so it
// is on that one process's command line for the length of a GC run; the token
// allows only the calls in installers.PublicAPIAllowedPaths.
const globalIPFSGCEnvFile = "gc.env"

// ipfsRepoAccess gives a public-Kubo unit the RPC group and lets that group
// traverse the repo directory: the provider, a member of the group, reads the
// RPC token file there. Every file the daemon writes stays 0600 under UMask=0077.
func ipfsRepoAccess(unit string) string {
	unit = strings.Replace(unit, "Group="+globalIPFSUser+"\n", "Group="+globalIPFSRPCGroup+"\n", 1)
	return strings.Replace(unit, "StateDirectoryMode=0700", "StateDirectoryMode=0750", 1)
}

// RenderGlobalIPFSUnit is orama-global-ipfs.service. The API is the public
// Kubo's loopback API, not the cluster daemon on IPFSAPIPort. The repo, its
// addresses, filters and token are written by the installer
// (installers.WritePublicKuboFiles); the unit only runs the daemon on it.
func RenderGlobalIPFSUnit() string {
	exec := fmt.Sprintf("%s/ipfs daemon --repo-dir=%s", globalBinDir, globalIPFSHome)
	unit := ipfsRepoAccess(renderGlobalUnit("Orama public IPFS", globalIPFSUser, globalIPFSHome, exec, ""))
	// libp2p reads the interface and route tables over netlink at startup.
	return strings.Replace(unit, "RestrictAddressFamilies=AF_INET AF_UNIX\n", "RestrictAddressFamilies=AF_INET AF_UNIX AF_NETLINK\n", 1)
}

// RenderGlobalIPFSGCUnit is the oneshot that garbage-collects the public Kubo
// repo through the running daemon's RPC. Its timer is RenderGlobalIPFSGCTimer.
// Neither is PartOf orama-node.
func RenderGlobalIPFSGCUnit() string {
	api := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", constants.GlobalIPFSAPIPort)
	exec := fmt.Sprintf("%s/ipfs --api=%s --api-auth=${IPFS_API_AUTH} repo gc", globalBinDir, api)
	unit := renderGlobalOneshot("Orama public IPFS garbage collection", globalIPFSUser, "orama-global/ipfs", globalIPFSHome, exec)
	unit = strings.Replace(unit, "After=network-online.target\n", "After=network-online.target "+constants.GlobalIPFSUnit+"\n", 1)
	unit = strings.Replace(unit, "ExecStart=", "EnvironmentFile="+globalIPFSHome+"/"+globalIPFSGCEnvFile+"\nExecStart=", 1)
	return ipfsRepoAccess(unit)
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
	exec := fmt.Sprintf("%s/orama-global provider --listen 0.0.0.0:%d --ipfs-api %s --ipfs-token-file %s/%s",
		globalBinDir, constants.GlobalProviderPort, constants.LocalGlobalIPFSAPIURL(), constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile)
	return needsIPFS(needsChain(renderGlobalUnitExtra("Orama storage provider", globalProviderUser, globalProviderUser, globalIPFSRPCGroup,
		"orama-global/provider", constants.GlobalProviderHome, exec, "")))
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
StartLimitIntervalSec=0

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

// RenderGlobalArchiverUnit bundles chain history. It reads blocks from the
// local oramad RPC on loopback and has no access to the chain home.
func RenderGlobalArchiverUnit() string {
	exec := fmt.Sprintf("%s/orama-global archiver", globalBinDir)
	return needsChain(renderGlobalUnit("Orama history archiver", globalArchiverUser, constants.GlobalArchiverHome, exec, ""))
}

// RenderGlobalIndexerUnit is orama-global-indexer.service. It reads blocks
// from the local oramad RPC on loopback, keeps its index in its own state
// directory, and serves the read API on loopback only; the gateway proxies
// /v1/chain/index/ to it. It has no key and no access to the chain home.
func RenderGlobalIndexerUnit() string {
	exec := fmt.Sprintf("%s/orama-global indexer --rpc tcp://127.0.0.1:%d --home %s --listen 127.0.0.1:%d",
		globalBinDir, constants.ChainRPCPort, constants.GlobalIndexerHome, constants.GlobalIndexerPort)
	return needsChain(renderGlobalUnit("Orama chain indexer", globalIndexerUser, constants.GlobalIndexerHome, exec, ""))
}

// RenderGlobalRepairUnit holds repair seeds. A host that runs it does not
// also run the storage provider.
func RenderGlobalRepairUnit() string {
	exec := fmt.Sprintf("%s/orama-global repair", globalBinDir)
	return needsChain(renderGlobalUnit("Orama repair delegate", globalRepairUser, constants.GlobalRepairHome, exec, ""))
}
