// Package globalhealth turns a node's chain and global-service report into alerts.
// The monitor and the inspector both call Evaluate, so the thresholds live once.
package globalhealth

import (
	"fmt"
	"sort"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

const (
	// Critical and Warning are the severities Evaluate reports.
	Critical = "critical"
	Warning  = "warning"

	// ChainStallSec is how long a synced node may go without a block.
	// The cluster component probe uses the same bound.
	ChainStallSec = 60
	// ChainHeightLagBlocks is how far a synced node may sit behind the
	// median responsive height before it is lagging. The median is the same
	// choice as the public chain view: one node cannot set the height.
	ChainHeightLagBlocks = 20
)

// Node is one machine's chain and global sections. Either may be nil.
type Node struct {
	Host   string
	Chain  *report.ChainReport
	Global *report.GlobalReport
}

// Issue is one alert. Code is stable; Message is the sentence the operator sees.
type Issue struct {
	Host      string
	Subsystem string
	Severity  string
	Code      string
	Message   string
}

// Evaluate returns the chain and global alerts for these nodes, sorted by
// host and then code. A nil chain or global section adds nothing for that part.
func Evaluate(nodes []Node) []Issue {
	median, hasMedian := medianHeight(nodes)
	var issues []Issue
	for _, n := range nodes {
		issues = append(issues, chainIssues(n, median, hasMedian)...)
		issues = append(issues, globalIssues(n)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Host != issues[j].Host {
			return issues[i].Host < issues[j].Host
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

func medianHeight(nodes []Node) (height int64, ok bool) {
	var heights []int64
	for _, n := range nodes {
		c := n.Chain
		if c != nil && c.Responsive {
			heights = append(heights, c.LatestHeight)
		}
	}
	if len(heights) == 0 {
		return 0, false
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	return heights[(len(heights)-1)/2], true
}

func chainIssues(n Node, median int64, hasMedian bool) []Issue {
	c := n.Chain
	if c == nil {
		return nil
	}
	var out []Issue
	add := func(sev, code, msg string) {
		out = append(out, Issue{n.Host, "chain", sev, code, msg})
	}
	if !c.ServiceActive {
		sev := Warning
		if c.UnitState == "failed" {
			sev = Critical
		}
		add(sev, "chain.down", fmt.Sprintf("chain unit is %s", blank(c.UnitState, "inactive")))
		return out
	}
	if !c.Responsive {
		add(Critical, "chain.rpc", "chain RPC is not answering")
		return out
	}
	if hasMedian && !c.CatchingUp && median-c.LatestHeight > ChainHeightLagBlocks {
		add(Warning, "chain.lag", fmt.Sprintf("chain height %d is %d blocks behind the median %d",
			c.LatestHeight, median-c.LatestHeight, median))
	}
	if !c.CatchingUp && c.LatestHeight > 0 && c.BlockAgeSec >= ChainStallSec {
		add(Warning, "chain.stalled", fmt.Sprintf("chain has not produced a block for %.0f seconds", c.BlockAgeSec))
	}
	if validatorCount(c) > 1 && c.Peers < 1 {
		add(Warning, "chain.peers", fmt.Sprintf("chain has no peers while the validator set has %d members", validatorCount(c)))
	}
	if c.Jailed != nil && *c.Jailed {
		add(Critical, "chain.jailed", "validator is jailed")
	}
	if c.Tombstoned != nil && *c.Tombstoned {
		add(Critical, "chain.tombstoned", "validator is tombstoned")
	}
	if issue, ok := missedBlocks(c); ok {
		out = append(out, Issue{n.Host, "chain", issue.sev, "chain.missed", issue.msg})
	}
	if c.SigningError != "" && c.Jailed == nil && c.MissedBlockRatio == nil {
		add(Warning, "chain.signing", c.SigningError)
	}
	return out
}

type missed struct {
	sev string
	msg string
}

func missedBlocks(c *report.ChainReport) (missed, bool) {
	if c.MissedBlockRatio == nil || c.MinSignedPerWindow == nil {
		return missed{}, false
	}
	ratio := *c.MissedBlockRatio
	minSigned := *c.MinSignedPerWindow
	if ratio <= 0 || minSigned < 0 || minSigned > 1 {
		return missed{}, false
	}
	threshold := 1 - minSigned
	if ratio >= threshold {
		return missed{Critical, fmt.Sprintf("missed-block ratio %.4f has reached the downtime threshold %.4f", ratio, threshold)}, true
	}
	if threshold > 0 && ratio >= threshold/2 {
		return missed{Warning, fmt.Sprintf("missed-block ratio %.4f is at least half the downtime threshold %.4f", ratio, threshold)}, true
	}
	return missed{}, false
}

func validatorCount(c *report.ChainReport) int {
	if c.ValidatorCount > 0 {
		return c.ValidatorCount
	}
	return len(c.Validators)
}

func globalIssues(n Node) []Issue {
	g := n.Global
	if g == nil {
		return nil
	}
	var out []Issue
	add := func(sev, code, msg string) {
		out = append(out, Issue{n.Host, "global", sev, code, msg})
	}
	if g.Error != "" {
		add(Warning, "global.read", g.Error)
		return out
	}
	addUnit := func(name, code, label string) string {
		state, ok := unitState(g, name)
		if !ok {
			return ""
		}
		if state == "active" {
			return state
		}
		sev := Warning
		if state == "failed" {
			sev = Critical
		}
		add(sev, code, fmt.Sprintf("%s is %s", label, blank(state, "inactive")))
		return state
	}
	if addUnit(constants.GlobalIPFSUnit, "global.ipfs.down", "public Kubo") == "active" {
		switch {
		case g.PublicIPFS == nil || g.PublicIPFS.Error != "":
			msg := "public Kubo RPC did not answer"
			if g.PublicIPFS != nil && g.PublicIPFS.Error != "" {
				msg = g.PublicIPFS.Error
			}
			add(Warning, "global.ipfs.api", msg)
		case g.PublicIPFS.StorageMaxBytes > 0 && g.PublicIPFS.RepoBytes > g.PublicIPFS.StorageMaxBytes:
			add(Warning, "global.ipfs.disk", fmt.Sprintf("public Kubo repo is over StorageMax (%d of %d bytes)",
				g.PublicIPFS.RepoBytes, g.PublicIPFS.StorageMaxBytes))
		}
	}
	if addUnit(constants.GlobalProviderUnit, "global.provider.down", "storage provider") == "active" && g.Provider != nil && g.Provider.Error == "" {
		if g.Provider.HotKeyBalanceNorama != nil && *g.Provider.HotKeyBalanceNorama == 0 {
			add(Critical, "global.provider.balance", "storage provider hot key balance is 0 norama, so it cannot pay for a proof")
		}
		if g.Provider.ProofMisses != nil && *g.Provider.ProofMisses > 0 {
			add(Warning, "global.provider.proofs", fmt.Sprintf("storage provider reports %d unsettled proof misses", *g.Provider.ProofMisses))
		}
		if g.Provider.DiskBytes != nil && g.Provider.StorageMaxBytes != nil &&
			*g.Provider.StorageMaxBytes > 0 && *g.Provider.DiskBytes > *g.Provider.StorageMaxBytes {
			add(Warning, "global.provider.disk", fmt.Sprintf("provider disk is over the declared maximum (%d of %d bytes)",
				*g.Provider.DiskBytes, *g.Provider.StorageMaxBytes))
		}
	}
	// A host runs a relay or a directory authority; each is in the consensus
	// and reports whether the consensus it holds lists it.
	for _, role := range []struct{ unit, code, label string }{
		{constants.GlobalTorRelayUnit, "relay", "relay"},
		{constants.GlobalTorDirauthUnit, "dirauth", "directory authority"},
	} {
		if addUnit(role.unit, "global."+role.code+".down", role.label) == "active" && g.Relay != nil && g.Relay.Error == "" {
			if g.Relay.InConsensus != nil && !*g.Relay.InConsensus {
				add(Warning, "global."+role.code+".consensus", role.label+" reports it is not in the relay set")
			}
		}
	}
	return out
}

func unitState(g *report.GlobalReport, name string) (string, bool) {
	for _, u := range g.Units {
		if u.Name == name {
			return u.State, true
		}
	}
	return "", false
}

func blank(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
