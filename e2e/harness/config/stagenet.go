package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Targets of the runner. The default target is a disposable fleet the run
// provisions; TargetStagenet is the existing stagenet cluster, which the
// runner only tests: it never provisions, destroys or sweeps anything there.
const (
	TargetFleet    = ""
	TargetStagenet = "stagenet"
)

// The stagenet target is pinned to exactly these values: a state that names
// anything else is refused, so a state file cannot aim the suite at devnet,
// testnet or any other cluster.
const (
	StagenetEnv        = "stagenet"
	StagenetBaseDomain = "stagenet.dbrsteting.bid"
	StagenetGatewayURL = "https://stagenet.dbrsteting.bid"
	// StagenetDefaultChainID is the chain id `e2e-fleet target stagenet` writes unless --chain-id says otherwise:
	// the CHAIN_ID chain/scripts/stagenet/deploy.sh deploys by default.
	StagenetDefaultChainID = "orama-stagenet-1"
	// StagenetChainHost is where a node's co-located chain answers (RPC 31001, REST 31003, indexer 31015):
	// inside the orama-global netns, reachable from the host's root namespace.
	StagenetChainHost = "198.18.0.2"
	// StagenetOperatorNamespace is the namespace the operator's wallet owns
	// and signs in to for a run. docs/AUTH.md: /v1/operator/* needs an admin
	// grant as well as the operator list, and a lobby session holds no grant,
	// so the operator's session has to stand in a namespace it owns; a wallet
	// session anywhere also creates namespaces and lists the wallet's own.
	StagenetOperatorNamespace = "stagenetproof"
)

// Paths under the owner's real home that the stagenet target uses.
const (
	// StagenetRWSockRel is the socket of the dev RootWallet headless agent.
	StagenetRWSockRel = "rwdev/agent.sock"
	// StagenetRWReadyRel is the ready file that agent wrote; it holds the wallet address.
	StagenetRWReadyRel = "rwdev/ready.json"
	// StagenetHomeRel is the CLI HOME (it holds ~/.orama/environments.json for the stagenet env).
	StagenetHomeRel = "orama-stagenet-handoff/cli-home"
	// StagenetCAFileRel is the Let's Encrypt staging roots bundle.
	StagenetCAFileRel = "orama-stagenet-handoff/le-staging-roots.pem"
	// StagenetSSHKeyRel is the private key that reaches the stagenet nodes.
	StagenetSSHKeyRel = ".ssh/debros-nodes"
	// StagenetKnownHostsRel is the owner's known_hosts the pinned host keys are cross-checked against.
	StagenetKnownHostsRel = ".ssh/known_hosts"
)

// StagenetNode is one stagenet node. Nameserver says whether it runs the
// zone's CoreDNS; the others are plain cluster nodes.
type StagenetNode struct {
	Name, Label, IP, User, WGIP string
	Nameserver                  bool
}

// StagenetNodes are the five stagenet nodes, in state order (the join order;
// node-1 is the genesis nameserver). WGIP is the overlay address the join
// assigned. The machines differ in ways the tests see:
//
//   - mew and mewtwo: OVH (ASN 16276), Ubuntu 26.04, systemd 259 built with
//     BPF_FRAMEWORK, so SocketBindDeny is enforced and the deployment sandbox
//     test sees a refused bind there.
//   - gengar, magicarp and froakie: Contabo (ASN 51167), Ubuntu 24.04, systemd
//     255 without BPF_FRAMEWORK, so SocketBindDeny is accepted and not
//     enforced and the sandbox test's outcome on these three is the
//     unenforced one.
//
// The first three are nameservers (mew, mewtwo, gengar); magicarp and froakie
// are plain nodes. Gengar, magicarp and froakie log in as root, the others as
// ubuntu.
var StagenetNodes = []StagenetNode{
	{Name: "node-1", Label: "mew", IP: "57.129.166.16", User: "ubuntu", WGIP: "10.0.0.1", Nameserver: true},
	{Name: "node-2", Label: "mewtwo", IP: "57.129.166.17", User: "ubuntu", WGIP: "10.0.0.2", Nameserver: true},
	{Name: "node-3", Label: "gengar", IP: "161.97.184.199", User: "root", WGIP: "10.0.0.3", Nameserver: true},
	{Name: "node-4", Label: "magicarp", IP: "161.97.184.202", User: "root", WGIP: "10.0.0.4"},
	{Name: "node-5", Label: "froakie", IP: "161.97.151.255", User: "root", WGIP: "10.0.0.5"},
}

var (
	stagenetChainID = regexp.MustCompile(`^orama-stagenet-[0-9]+$`)
	// stagenetRunID is stagenet-<yyyymmdd>-<hhmmss>.
	stagenetRunID = regexp.MustCompile(`^stagenet-[0-9]{8}-[0-9]{6}$`)
)

// StagenetRunID reports whether id has the shape of a stagenet run id.
func StagenetRunID(id string) bool { return stagenetRunID.MatchString(id) }

// StagenetPath is rel under realHome.
func StagenetPath(realHome, rel string) string { return filepath.Join(realHome, rel) }

// StagenetIPs are the pinned public addresses, sorted.
func StagenetIPs() []string {
	out := make([]string, 0, len(StagenetNodes))
	for _, n := range StagenetNodes {
		out = append(out, n.IP)
	}
	sort.Strings(out)
	return out
}

// CheckTarget refuses a target name that is neither the default nor stagenet.
func CheckTarget(target string) error {
	if target != TargetFleet && target != TargetStagenet {
		return fmt.Errorf("target %q is unknown (use %q or the default fleet target)", target, TargetStagenet)
	}
	return nil
}

// StagenetPins are the values of a state the stagenet target pins exactly.
type StagenetPins struct {
	RunID, Env, BaseDomain, GatewayURL, ChainID string
	// OperatorNamespace is the namespace the operator signs in to. A state
	// written before it was recorded holds none, and signing in to "" is the
	// lobby, where no operator route answers: every stage then failed.
	OperatorNamespace string
	// NodeIPs are the public addresses of every node, extra and probe of the state.
	NodeIPs []string
}

// CheckStagenet requires every pinned value to match exactly. Every mismatch is reported.
func CheckStagenet(p StagenetPins) error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf("stagenet target: "+format, args...)) }
	if !stagenetRunID.MatchString(p.RunID) {
		fail("run id %q is not stagenet-<yyyymmdd>-<hhmmss>", p.RunID)
	}
	if p.Env != StagenetEnv {
		fail("environment %q is not %q", p.Env, StagenetEnv)
	}
	if p.BaseDomain != StagenetBaseDomain {
		fail("base domain %q is not %q", p.BaseDomain, StagenetBaseDomain)
	}
	if p.GatewayURL != StagenetGatewayURL {
		fail("gateway url %q is not %q", p.GatewayURL, StagenetGatewayURL)
	}
	if !stagenetChainID.MatchString(p.ChainID) {
		fail("chain id %q does not match %s", p.ChainID, stagenetChainID)
	}
	if p.OperatorNamespace != StagenetOperatorNamespace {
		fail("operator namespace %q is not %q (re-run `e2e-fleet target stagenet`)", p.OperatorNamespace, StagenetOperatorNamespace)
	}
	got := append([]string{}, p.NodeIPs...)
	sort.Strings(got)
	if want := StagenetIPs(); strings.Join(got, ",") != strings.Join(want, ",") {
		fail("node public addresses [%s] are not exactly [%s]", strings.Join(got, ", "), strings.Join(want, ", "))
	}
	return errors.Join(errs...)
}

// CheckStagenetChainID requires id to be a stagenet chain id.
func CheckStagenetChainID(id string) error {
	if !stagenetChainID.MatchString(id) {
		return fmt.Errorf("chain id %q does not match %s", id, stagenetChainID)
	}
	return nil
}
