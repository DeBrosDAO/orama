package install

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
)

// Global unit accounts and paths. `orama global install` (InstallGlobal)
// writes the chain (RenderGlobalChainUnit, under cosmovisor), public Kubo and
// its GC timer, provider, archiver, indexer and repair units; the cluster
// install writes none of them, and the sbws renderer here is not installed by
// anything yet. The Tor roles (dirauth, relay, onion) are, with their torrc
// (global_install_tor.go), and so is a directory authority's reporter
// (global_install_reporter.go).
// chain/scripts/stagenet/deploy.sh writes its own orama-global-chain unit for
// the stagenet mesh; this one is the global-role unit, with no WireGuard
// dependency and no cluster secret path.
const (
	globalBinDir = constants.GlobalBinDir
	// globalCosmovisor is where `orama global install` puts the pinned cosmovisor.
	globalCosmovisor = globalBinDir + "/" + constants.CosmovisorBinary
	globalChainUser  = constants.ChainUser
	globalIPFSUser   = "orama-ipfs-pub"

	globalProviderUser = "orama-provider"
	globalSBWSUser     = "orama-sbws"
	globalReporterUser = "orama-reporter"
	globalArchiverUser = "orama-archiver"
	globalRepairUser   = "orama-repair"
	globalIndexerUser  = "orama-indexer"
	// Each Tor role runs as an account of its own, not as the Tor package's
	// debian-tor: an onion service reachable by anyone on the network must not
	// share a uid with an authority's signing key.
	globalTorDirauthUser = "orama-tor-dirauth"
	globalTorRelayUser   = "orama-tor-relay"
	globalTorOnionUser   = "orama-tor-onion"
	globalTxGateUser     = "orama-txgate"

	globalIPFSHome = constants.GlobalIPFSHome
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
	env := fmt.Sprintf("Environment=DAEMON_NAME=%s\nEnvironment=DAEMON_HOME=%s\nEnvironment=DAEMON_ALLOW_DOWNLOAD_BINARIES=false\nEnvironment=DAEMON_RESTART_AFTER_UPGRADE=true\nEnvironment=GOMEMLIMIT=%s\nReadOnlyPaths=%s\n",
		constants.ChainDaemonName, constants.ChainHome, constants.ChainGoMemLimit,
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
func RenderGlobalIPFSGCUnit(apiHost string) string {
	api := fmt.Sprintf("/ip4/%s/tcp/%d", apiHost, constants.GlobalIPFSAPIPort)
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
func RenderGlobalProviderUnit(apiHost string) string {
	exec := fmt.Sprintf("%s/orama-global provider --listen 0.0.0.0:%d --ipfs-api http://%s --ipfs-token-file %s/%s",
		globalBinDir, constants.GlobalProviderPort, net.JoinHostPort(apiHost, strconv.Itoa(constants.GlobalIPFSAPIPort)), constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile)
	return needsIPFS(needsChain(renderGlobalUnitExtra("Orama storage provider", globalProviderUser, globalProviderUser, globalIPFSRPCGroup,
		"orama-global/provider", constants.GlobalProviderHome, exec, "")))
}

func renderGlobalUnit(description, user, home, exec, extra string) string {
	state := strings.TrimPrefix(home, "/var/lib/")
	return renderGlobalUnitExtra(description, user, user, "", state, home, exec, extra)
}

// The global service accounts have no home directory, and ProtectHome hides
// /home anyway, so HOME is the unit's own state directory: Kubo, for one,
// resolves its denylists and caches under it and refuses to start otherwise.
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
Environment=HOME=%s
ExecStart=%s
Restart=always
RestartSec=5
LimitNOFILE=65535
%s
[Install]
WantedBy=multi-user.target
`, description, user, group, supp, state, extra, home, home, home, exec, globalSandboxTail)
}

// renderGlobalOneshot sets HOME as renderGlobalUnitExtra does.
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
Environment=HOME=%s
ExecStart=%s
%s
`, description, user, user, state, home, home, exec, globalSandboxTail)
}

// RenderGlobalTorRelayUnit is the relay of the Orama Tor network, and its exit
// when the torrc says so (the unit is the same). Tor is the distro binary.
// MemoryDenyWriteExecute is safe for tor and is not set on the Go services.
func RenderGlobalTorRelayUnit() string {
	return renderTorUnit("Orama Tor relay", globalTorRelayUser, constants.GlobalTorRelayHome)
}

// RenderGlobalTorDirauthUnit is a directory authority of the Orama Tor
// network. It is a relay as well, so it publishes the ORPort and the DirPort.
func RenderGlobalTorDirauthUnit() string {
	return renderTorUnit("Orama Tor directory authority", globalTorDirauthUser, constants.GlobalTorDirauthHome)
}

// RenderGlobalTorOnionUnit is the validator onion service, a Tor client that
// publishes one hidden service and forwards it to the tx gate. It publishes no
// ORPort and relays nothing.
func RenderGlobalTorOnionUnit() string {
	unit := renderTorUnit("Orama validator tx onion", globalTorOnionUser, constants.GlobalTorOnionHome)
	return strings.Replace(unit, "After=network-online.target\nWants=network-online.target\n",
		"After=network-online.target "+constants.GlobalTxGateUnit+"\nWants=network-online.target "+constants.GlobalTxGateUnit+"\n", 1)
}

// renderTorUnit runs tor from the torrc in its own state directory. Tor does
// not need netlink to publish a configured Address, but its interface
// enumeration does, so the unit allows it as the public Kubo's does. A syscall
// the filter refuses returns EPERM instead of killing the daemon.
func renderTorUnit(description, user, home string) string {
	state := strings.TrimPrefix(home, "/var/lib/")
	exec := "/usr/bin/tor -f " + constants.GlobalTorrcFor(home)
	extra := "MemoryDenyWriteExecute=yes\nSystemCallErrorNumber=EPERM\n"
	unit := renderGlobalUnitExtra(description, user, user, "", state, home, exec, extra)
	unit = strings.Replace(unit, "RestrictAddressFamilies=AF_INET AF_UNIX\n", "RestrictAddressFamilies=AF_INET AF_UNIX AF_NETLINK\n", 1)
	// An exit's own policy keeps it out of the co-located namespace's address
	// range (the chain's RPC and REST API, Kubo's RPC and the indexer listen
	// there); this refuses the same range in the kernel, whatever tor does.
	return strings.Replace(unit, "IPAddressAllow=localhost\n", "IPAddressDeny="+torNamespaceRange+"\nIPAddressAllow=localhost\n", 1)
}

// torNamespaceRange is the benchmarking range the co-located namespace's veth
// pair lives in (198.18.0.0/30) and its services listen on.
const torNamespaceRange = "198.18.0.0/15"

// denyLoopback is a relay or authority unit in the co-located namespace: its
// resolver there is a public one (globalnetns.Resolvers), so it has no use for
// loopback, and loopback in the namespace holds the chain's gRPC and metrics
// listeners. IPAddressAllow would win over a deny, so it is dropped.
func denyLoopback(unit string) (string, error) {
	const allow = "IPAddressAllow=localhost\n"
	if strings.Count(unit, allow) != 1 {
		return "", fmt.Errorf("the unit has no single %q line to replace with a loopback deny", strings.TrimSpace(allow))
	}
	return strings.Replace(unit, allow, "IPAddressDeny=127.0.0.0/8\n", 1), nil
}

// RenderGlobalTxGateUnit is the tx gate behind the validator onion service:
// the one HTTP listener the onion forwards to, on loopback, which passes three
// calls to the chain's REST API (pkg/txgate). It has no key and no access to
// the chain home.
func RenderGlobalTxGateUnit() string {
	exec := fmt.Sprintf("%s/%s global txgate --listen %s --upstream %s", globalBinDir, globalOramaCLI, txGateListen, constants.LocalChainAPIURL())
	return needsChain(renderGlobalUnit("Orama validator tx gate", globalTxGateUser, constants.GlobalTxGateHome, exec, ""))
}

// txGateListen is where the tx gate listens and the onion torrc forwards to.
var txGateListen = net.JoinHostPort("127.0.0.1", strconv.Itoa(constants.GlobalTxGatePort))

// RenderGlobalTorArchiveUnit is the oneshot that copies the directory
// authority's consensus and votes into its archive. Its timer is
// RenderGlobalTorArchiveTimer.
func RenderGlobalTorArchiveUnit() string {
	home := constants.GlobalTorDirauthHome
	exec := fmt.Sprintf("%s/%s global tor archive --data-dir %s --archive-dir %s/%s", globalBinDir, globalOramaCLI, home, home, constants.GlobalTorArchiveDir)
	unit := renderGlobalOneshot("Orama Tor vote archive", globalTorDirauthUser, strings.TrimPrefix(home, "/var/lib/"), home, exec)
	// It copies files and talks to nobody.
	unit = strings.Replace(unit, "RestrictAddressFamilies=AF_INET AF_UNIX\n", "RestrictAddressFamilies=AF_UNIX\n", 1)
	return strings.Replace(unit, "IPAddressAllow=localhost\n", "IPAddressDeny=any\nIPAddressAllow=localhost\n", 1)
}

// RenderGlobalTorArchiveTimer fires the archive. A consensus gains signatures
// for a few minutes after it is cached and the shortest voting interval is five
// minutes, so the archive looks every minute: a period cannot pass unseen.
func RenderGlobalTorArchiveTimer() string {
	return `[Unit]
Description=Schedule the Orama Tor vote archive
# Restarting orama-node must not restart this timer.

[Timer]
OnBootSec=2min
OnUnitActiveSec=1min
AccuracySec=10s
Unit=` + constants.GlobalTorArchiveUnit + `

[Install]
WantedBy=timers.target
`
}

// RenderGlobalTorMonitorUnit is the oneshot that writes the relay's
// monitor.json for the node report, as the relay's own account in its own
// DataDirectory. Its timer is RenderGlobalTorMonitorTimer.
func RenderGlobalTorMonitorUnit() string {
	return renderTorMonitorUnit("Orama Tor relay monitor", globalTorRelayUser, constants.GlobalTorRelayHome)
}

// RenderGlobalTorDirauthMonitorUnit is the same oneshot for a directory
// authority, which is in the consensus as a relay is: it runs as the
// authority's account and writes monitor.json in the authority's DataDirectory.
// A host runs a relay or an authority, never both, so the unit has the same
// name and the same timer as the relay's.
func RenderGlobalTorDirauthMonitorUnit() string {
	return renderTorMonitorUnit("Orama Tor directory authority monitor", globalTorDirauthUser, constants.GlobalTorDirauthHome)
}

func renderTorMonitorUnit(description, user, home string) string {
	exec := fmt.Sprintf("%s/%s global tor monitor --home %s", globalBinDir, globalOramaCLI, home)
	unit := renderGlobalOneshot(description, user, strings.TrimPrefix(home, "/var/lib/"), home, exec)
	// It reads files and talks to nobody.
	unit = strings.Replace(unit, "RestrictAddressFamilies=AF_INET AF_UNIX\n", "RestrictAddressFamilies=AF_UNIX\n", 1)
	return strings.Replace(unit, "IPAddressAllow=localhost\n", "IPAddressDeny=any\nIPAddressAllow=localhost\n", 1)
}

// RenderGlobalTorMonitorTimer fires the monitor every five minutes: the
// consensus changes hourly, and the node report is read less often than that.
func RenderGlobalTorMonitorTimer() string {
	return `[Unit]
Description=Schedule the Orama Tor relay or directory authority monitor
# Restarting orama-node must not restart this timer.

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
AccuracySec=30s
Unit=` + constants.GlobalTorMonitorUnit + `

[Install]
WantedBy=timers.target
`
}

// RenderGlobalSBWSUnit measures relay bandwidth. Dirauth hosts only.
func RenderGlobalSBWSUnit() string {
	return renderGlobalUnit("Orama sbws", globalSBWSUser, "/var/lib/orama-global/sbws", "/usr/bin/sbws generate", "")
}

// RenderGlobalReporterUnit reports each closed epoch's relay bandwidth and
// uptime to x/relay from this authority's votes. Dirauth hosts only. It reads
// the epoch and signs through the local oramad RPC on loopback, so it starts
// after the chain; its home (state, hot key, operator, authority-id and the
// votes directory) is the unit's working directory.
func RenderGlobalReporterUnit() string {
	exec := fmt.Sprintf("%s/orama-global reporter --rpc tcp://127.0.0.1:%d --home %s", globalBinDir, constants.ChainRPCPort, constants.GlobalReporterHome)
	return needsChain(renderGlobalUnit("Orama bandwidth reporter", globalReporterUser, constants.GlobalReporterHome, exec, ""))
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
