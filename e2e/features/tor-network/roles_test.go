//go:build e2e_fleet

package tornetwork

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	dialTimeout = 5 * time.Second
	// publicPage is fetched through an exit; its content is not checked, only
	// that a response came back from outside the network.
	publicPage = "http://example.com/"
)

// TestAuthorities_signAConsensusThatListsEveryRelay: the authorities of the
// network file vote, a majority sign a consensus that is fresh and valid on
// every node that runs a relay or an authority, and the consensus lists each
// of those relays running.
func TestAuthorities_signAConsensusThatListsEveryRelay(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	nodes := r.publishers()
	for _, n := range nodes {
		network := networkOf(t, f, n)
		home := constants.GlobalTorRelayHome
		if slices.ContainsFunc(r.dirauth, func(d fleet.Node) bool { return d.Name == n.Name }) {
			home = constants.GlobalTorDirauthHome
		}
		eventually.Require(t, pollEvery, consensusBudget, n.Name+" to hold a fresh consensus that lists it running", func() (bool, error) {
			info := homeInfo(t, infoOf(t, f, n), n, home)
			c := info.Consensus
			if c == nil || !c.Fresh || !c.Valid || !c.Listed {
				return false, fmt.Errorf("consensus %+v", c)
			}
			if majority := len(network.Authorities)/2 + 1; c.Signatures < majority {
				return false, fmt.Errorf("%d signatures, need a majority of %d authorities", c.Signatures, len(network.Authorities))
			}
			if !slices.Contains(c.ListedFlags, "Running") || !slices.Contains(c.ListedFlags, "Valid") {
				return false, fmt.Errorf("listed with flags %v", c.ListedFlags)
			}
			if c.Relays < len(nodes) {
				return false, fmt.Errorf("%d relays listed, %d nodes run one", c.Relays, len(nodes))
			}
			return true, nil
		})
	}
}

// TestNetwork_consensusVotesTheOnionTimePeriodOfOneSharedRandomRun: every tor
// process of the network (authority, relay and onion service) holds a consensus
// whose hsdir_interval is 24 voting intervals, the length of one shared-random
// run. A service rotates its descriptors at the end of each run, and clients
// look it up by the time period: with Tor's default of 1440 minutes and a
// voting interval shorter than an hour, the service is unreachable from each
// rotation until the period ends (website/src/docs/operator/tor-network.mdx#onion-service-time-periods).
func TestNetwork_consensusVotesTheOnionTimePeriodOfOneSharedRandomRun(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	type holder struct {
		node fleet.Node
		home string
	}
	var holders []holder
	for _, n := range r.publishers() {
		holders = append(holders, holder{n, firstHome(r, n)})
	}
	for _, n := range r.onion {
		holders = append(holders, holder{n, constants.GlobalTorOnionHome})
	}
	for _, h := range holders {
		want := networkOf(t, f, h.node).HSDirIntervalMinutes()
		eventually.Require(t, pollEvery, consensusBudget, h.node.Name+" ("+h.home+") to hold a consensus with hsdir_interval "+strconv.Itoa(want), func() (bool, error) {
			c := homeInfo(t, infoOf(t, f, h.node), h.node, h.home).Consensus
			if c == nil || c.HSDirIntervalMinutes != want {
				return false, fmt.Errorf("consensus %+v", c)
			}
			return true, nil
		})
	}
}

// globalCLI is the orama CLI `orama global install` puts beside the other global binaries.
const globalCLI = constants.GlobalBinDir + "/orama"

// TestPublishers_monitorFileSaysTheyAreInTheConsensus: a relay and a directory
// authority each run the monitor timer, and the oneshot it fires, run now as
// the role's own account, writes the monitor.json in the role's home that the
// node report reads: in_consensus true for a node the consensus lists.
func TestPublishers_monitorFileSaysTheyAreInTheConsensus(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	for _, n := range r.publishers() {
		home := firstHome(r, n)
		if got := f.Unit(t, n, constants.GlobalTorMonitorTimer); got != infra.UnitActive {
			t.Errorf("%s: %s is %q", n.Name, constants.GlobalTorMonitorTimer, got)
		}
		eventually.Require(t, pollEvery, consensusBudget, n.Name+" to hold a consensus that lists it", func() (bool, error) {
			c := homeInfo(t, infoOf(t, f, n), n, home).Consensus
			return c != nil && c.Valid && c.Listed, nil
		})
		owner := strings.TrimSpace(f.MustExec(t, n, "stat -c %U "+fleet.ShellQuote(home)).Stdout)
		f.MustExec(t, n, "sudo -u "+owner+" "+globalCLI+" global tor monitor --home "+fleet.ShellQuote(home))
		file := home + "/" + constants.GlobalMonitorFile
		var mon struct {
			InConsensus *bool `json:"in_consensus"`
		}
		if err := json.Unmarshal([]byte(f.MustExec(t, n, "sudo cat "+fleet.ShellQuote(file)).Stdout), &mon); err != nil {
			t.Errorf("%s: %s: %v", n.Name, file, err)
			continue
		}
		if mon.InConsensus == nil || !*mon.InConsensus {
			t.Errorf("%s: %s says in_consensus %v for a node the consensus lists", n.Name, file, mon.InConsensus)
		}
	}
}

// TestAuthorities_archiveMatchesItsManifest: every authority archives its
// consensus, the votes that made it and its manifest, and each file hashes to
// the digest in the manifest, whose root is the digest of the digests.
func TestAuthorities_archiveMatchesItsManifest(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	if len(r.dirauth) == 0 {
		harness.SkipNotApplicable(t, "no node of this target is a directory authority (--services dirauth)")
	}
	for _, n := range r.dirauth {
		archive := constants.GlobalTorDirauthHome + "/" + constants.GlobalTorArchiveDir
		// Run the archive now rather than waiting for the timer.
		infra.ExpectNodeExit(t, n.Name+" archive", infra.OnNode(t, f, n, "maint", "global", "tor", "archive", "--data-dir", constants.GlobalTorDirauthHome, "--archive-dir", archive), infra.ExitOK, "valid-after")
		latest := strings.TrimSpace(f.MustExec(t, n, "ls -1 "+archive+" | tail -n 1").Stdout)
		if latest == "" {
			t.Errorf("%s: the archive is empty", n.Name)
			continue
		}
		dir := archive + "/" + latest
		var m tornet.Manifest
		if err := json.Unmarshal(f.ReadFile(t, n, dir+"/"+tornet.ArchiveManifestFile), &m); err != nil {
			t.Errorf("%s: manifest: %v", n.Name, err)
			continue
		}
		if _, ok := m.Files[tornet.ArchiveConsensusFile]; !ok || m.Root != tornet.ManifestRoot(m.Files) {
			t.Errorf("%s: manifest %+v", n.Name, m)
		}
		for name, want := range m.Files {
			got := strings.Fields(f.MustExec(t, n, "sha256sum "+dir+"/"+name).Stdout)
			if len(got) == 0 || got[0] != want {
				t.Errorf("%s: %s hashes to %v, manifest says %s", n.Name, name, got, want)
			}
		}
	}
}

// TestRelays_orPortIsReachableAndTheGateIsNot: from outside the node the
// ORPort (and an authority's DirPort) accept connections, and neither the tx
// gate's port nor any other port of the global block that is not an edge port
// does.
func TestRelays_orPortIsReachableAndTheGateIsNot(t *testing.T) {
	t.Parallel()
	_, r := requireRoles(t)
	reach := func(n fleet.Node, port int) bool {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(n.PublicIP, strconv.Itoa(port)), dialTimeout)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	for _, n := range r.publishers() {
		if !reach(n, constants.GlobalTorORPort) {
			t.Errorf("%s: ORPort %d is not reachable", n.Name, constants.GlobalTorORPort)
		}
	}
	for _, n := range r.dirauth {
		if !reach(n, constants.GlobalTorDirPort) {
			t.Errorf("%s: DirPort %d is not reachable", n.Name, constants.GlobalTorDirPort)
		}
	}
	for _, n := range append(r.publishers(), r.onion...) {
		for _, port := range []int{constants.GlobalTxGatePort, constants.ChainAPIPort, constants.ChainRPCPort} {
			if reach(n, port) {
				t.Errorf("%s: port %d answers from outside", n.Name, port)
			}
		}
	}
	for _, n := range r.relay {
		if reach(n, constants.GlobalTorDirPort) {
			t.Errorf("%s: a relay that is not an authority serves a DirPort", n.Name)
		}
	}
}

// TestRelays_onlyAnInstalledExitHasTheExitFlag: the Exit flag in the consensus
// belongs to exactly the relays installed as exits (their torrc says ExitRelay
// 1), and a relay that is not one has the policy reject *:* in its torrc.
func TestRelays_onlyAnInstalledExitHasTheExitFlag(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	exits := 0
	for _, n := range r.publishers() {
		home := constants.GlobalTorRelayHome
		if slices.ContainsFunc(r.dirauth, func(d fleet.Node) bool { return d.Name == n.Name }) {
			home = constants.GlobalTorDirauthHome
		}
		torrc := string(f.ReadFile(t, n, constants.GlobalTorrcFor(home)))
		isExit := strings.Contains(torrc, "\nExitRelay 1\n")
		if !isExit && !strings.Contains(torrc, "\nExitPolicy reject *:*\n") {
			t.Errorf("%s: a relay that is not an exit lacks `ExitPolicy reject *:*`", n.Name)
		}
		if isExit {
			exits++
			if !slices.Contains(r.relay, n) {
				t.Errorf("%s: a directory authority exits", n.Name)
			}
		}
		info := homeInfo(t, infoOf(t, f, n), n, home)
		if info.Consensus == nil || !info.Consensus.Listed {
			continue
		}
		hasFlag := slices.Contains(info.Consensus.ListedFlags, "Exit")
		if hasFlag && !isExit {
			t.Errorf("%s: the consensus gives the Exit flag to a relay installed without the exit role", n.Name)
		}
	}
	n := r.publishers()[0]
	if c := homeInfo(t, infoOf(t, f, n), n, firstHome(r, n)).Consensus; c != nil {
		if c.Exits > exits {
			t.Errorf("the consensus has %d exits, but %d nodes were installed as exits", c.Exits, exits)
		}
		if c.ExitsWithoutPorts > 0 {
			t.Errorf("%d of the consensus's %d exits are summarised as accepting no port (`p reject 1-65535`), so no client uses them: check the exit policy's refusals cover no more than two /8 blocks (orama.network/docs/operator/tor-network)", c.ExitsWithoutPorts, c.Exits)
		}
	}
}

func firstHome(r roles, n fleet.Node) string {
	if slices.ContainsFunc(r.dirauth, func(d fleet.Node) bool { return d.Name == n.Name }) {
		return constants.GlobalTorDirauthHome
	}
	return constants.GlobalTorRelayHome
}

// TestNetwork_circuitsAndOnionServicesWorkThroughOurAuthorities: a throwaway
// client of the Orama network, which knows only the network file's
// authorities, bootstraps from them and builds circuits through the relays; it
// reaches each validator's onion service, where the allowed account read gets
// the chain's answer and a path outside the wallet calls gets the gate's 404.
func TestNetwork_circuitsAndOnionServicesWorkThroughOurAuthorities(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	if len(r.onion) == 0 {
		harness.SkipNotApplicable(t, "no node of this target runs the validator onion service (--services chain,onion)")
	}
	n := r.onion[0]
	network := networkOf(t, f, n)
	c := startClient(t, f, n, network)
	for _, o := range r.onion {
		info := homeInfo(t, infoOf(t, f, o), o, constants.GlobalTorOnionHome)
		if !strings.HasSuffix(info.Onion, ".onion") {
			t.Fatalf("%s: onion address %q", o.Name, info.Onion)
		}
		base := "http://" + info.Onion
		eventually.Require(t, pollEvery, bootstrapBudget, "the onion service of "+o.Name+" to answer", func() (bool, error) {
			code, body := c.fetch(t, base+"/cosmos/auth/v1beta1/accounts/"+account)
			if code != 200 && code != 404 {
				return false, fmt.Errorf("status %d %.120s", code, body)
			}
			if strings.Contains(body, "not served over the onion service") {
				return false, fmt.Errorf("the gate refused the account read: %s", body)
			}
			return true, nil
		})
		if code, body := c.fetch(t, base+"/cosmos/staking/v1beta1/validators"); code != 404 || !strings.Contains(body, "not served over the onion service") {
			t.Errorf("%s: a path outside the wallet calls = %d %.120s, want the gate's 404", o.Name, code, body)
		}
		if code, _ := c.fetch(t, base+"/status"); code != 404 {
			t.Errorf("%s: /status over the onion = %d", o.Name, code)
		}
	}
}

// TestExit_leavesFromTheNodeAndRefusesWhatItShould: through a client pinned to
// one exit, a public page loads, the connection leaves from that node's
// address, and the exit policy refuses the destinations it is documented to
// refuse: a private range, the namespace's own host address and a mail port.
func TestExit_leavesFromTheNodeAndRefusesWhatItShould(t *testing.T) {
	t.Parallel()
	f, r := requireRoles(t)
	var exit *fleet.Node
	for _, n := range r.relay {
		if strings.Contains(string(f.ReadFile(t, n, constants.GlobalTorrcFor(constants.GlobalTorRelayHome))), "\nExitRelay 1\n") {
			exit = &n
			break
		}
	}
	if exit == nil {
		harness.SkipNotApplicable(t, "no relay of this target is installed as an exit (--services relay,exit)")
	}
	info := homeInfo(t, infoOf(t, f, *exit), *exit, constants.GlobalTorRelayHome)
	// After the authorities made no consensus for an hour (website/src/docs/operator/tor-network.mdx#directory-authorities)
	// the exit holds an expired one until it fetches the next: test it once it is valid again.
	eventually.Require(t, pollEvery, consensusBudget, "the exit "+exit.Name+" to hold a valid consensus", func() (bool, error) {
		cons := homeInfo(t, infoOf(t, f, *exit), *exit, constants.GlobalTorRelayHome).Consensus
		return cons != nil && cons.Valid, nil
	})
	c := startClient(t, f, *exit, networkOf(t, f, *exit), "ExitNodes $"+info.Fingerprint, "StrictNodes 1")
	eventually.Require(t, pollEvery, consensusBudget, "a page to load through the exit "+exit.Name, func() (bool, error) {
		code, _ := c.fetch(t, publicPage)
		if code != 200 {
			return false, fmt.Errorf("status %d", code)
		}
		return true, nil
	})
	code, body := c.fetch(t, "https://api.ipify.org")
	if code != 200 || strings.TrimSpace(body) != exit.PublicIP {
		t.Errorf("the exit's traffic left from %q (status %d), want %s", strings.TrimSpace(body), code, exit.PublicIP)
	}
	for name, url := range map[string]string{
		"a private range":         "http://10.0.0.1/",
		"the namespace host":      "http://" + constants.GlobalNetnsHostAddr + "/",
		"the carrier-grade range": "http://100.64.0.1/",
		"a mail submission port":  "telnet://example.com:25",
		"the exit's own loopback": "http://127.0.0.1:" + strconv.Itoa(constants.ChainRPCPort) + "/status",
	} {
		if code, body := c.fetch(t, url, "--connect-timeout 30"); code == 200 {
			t.Errorf("%s: the exit let %s through: %.120s", name, url, body)
		}
	}
}
