package provision

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

// Cluster shape and fixed names.
const (
	// nodeCount is the core cluster: three nameservers, three RQLite voters.
	nodeCount = 3
	// sshUser is the login on Hetzner images.
	sshUser = "root"
	// acmeCA is where run nodes get certificates.
	acmeCA = "letsencrypt-staging"
	// namePrefix starts every server name, environment name and subdomain.
	namePrefix = "e2e-"
	// probeName is the fleet name of the probe server.
	probeName = "probe-1"
	// defaultServerLimit is the project quota Up checks against.
	defaultServerLimit = hetzner.DefaultProjectServerLimit
	// chainIDPrefix makes every run chain a devnet chain.
	chainIDPrefix = "orama-devnet-e2e-"
	// chainRPC is where each node's oramad serves RPC: on its loopback,
	// reached from the runner through an SSH tunnel to any node.
	chainRPC = "http://127.0.0.1:31001"
	// nodeMinCores, nodeMinMemoryGB and nodeMinDiskGB are the installer's
	// floors (core/pkg/install/checks.go); nodeArch matches the amd64 archive.
	nodeMinCores    = 2
	nodeMinMemoryGB = 2
	nodeMinDiskGB   = 10
	nodeArch        = "x86"
)

// Files and directories of a run.
const (
	binDir         = "bin"
	oramaBinName   = "orama"
	oramaPrevName  = "orama-prev"
	archiveDir     = "archives"
	headArchive    = "orama-head-linux-amd64.tar.gz"
	prevArchive    = "orama-prev-linux-amd64.tar.gz"
	prevSourceDir  = "prev-src"
	tmpDirName     = "tmp"
	stateFileName  = "state.json"
	caFileName     = "letsencrypt-staging-roots.pem"
	sshDirName     = "e2e-ssh"
	sshKeyName     = "id_ed25519"
	knownHostsName = "known_hosts"
	chainScript    = "e2e/scripts/chain-deploy.sh"
)

// StatePath is where Up writes the run's state.
func StatePath(workDir string) string { return filepath.Join(workDir, stateFileName) }

func envName(runID string) string { return namePrefix + runID }

// serverName is the Hetzner name of a fleet member, e2e-<id>-<suffix>.
func serverName(runID, suffix string) string { return namePrefix + runID + "-" + suffix }

func nodeName(i int) string { return fmt.Sprintf("node-%d", i+1) }

func nodeSuffix(i int) string { return fmt.Sprintf("n%d", i+1) }

func chainID(runID string) string { return chainIDPrefix + runID }

// runLabels marks a resource as the run's, with its time to live.
func runLabels(runID string, ttl time.Duration) map[string]string {
	return map[string]string{hetzner.LabelRun: runID, hetzner.LabelTTL: ttl.String()}
}

func runSelector(runID string) string { return hetzner.LabelRun + "=" + runID }

// nodeRequirements is the least a node server must offer.
var nodeRequirements = hetzner.Requirements{
	MinCores: nodeMinCores, MinMemoryGB: nodeMinMemoryGB, MinDiskGB: nodeMinDiskGB, Architecture: nodeArch,
}

// firewallRules mirror the ports the installer opens with ufw
// (core/pkg/install/firewall.go): a firewall in front of the VPS must allow
// the same, or it blocks what ufw allows. SSH is the exception: only the
// runner (sshCIDRs) needs it, since every node-to-node path is WireGuard.
func firewallRules(sshCIDRs []string) []hetzner.FirewallRule {
	anywhere := splitList(anywhereCIDRs)
	rule := func(proto, port, desc string) hetzner.FirewallRule {
		return hetzner.FirewallRule{Direction: "in", Protocol: proto, Port: port, SourceIPs: anywhere, Description: desc}
	}
	ssh := rule("tcp", "22", "SSH from the e2e runner")
	ssh.SourceIPs = sshCIDRs
	return []hetzner.FirewallRule{
		ssh, rule("tcp", "53", "DNS"), rule("udp", "53", "DNS"),
		rule("tcp", "80", "HTTP / ACME"), rule("tcp", "443", "HTTPS"), rule("udp", "51820", "WireGuard"),
		rule("udp", "3478", "TURN"), rule("tcp", "3478", "TURN"), rule("tcp", "5349", "TURNS"),
		rule("udp", "49152-65535", "TURN relay"),
	}
}

// target is how the harness reaches a fleet member over SSH.
func target(st *fleet.State, n fleet.Node) sshTarget {
	return sshTarget{Host: n.PublicIP, User: n.SSHUser, KeyFile: st.SSHKeyFile, KnownHostsFile: st.KnownHostsFile}
}
