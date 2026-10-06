package inspector

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Subsystem names a collection failure is filed under. They are the names the
// checkers register under, so a failed collection is reported in the subsystem
// whose checks it replaced.
const (
	SubsystemRQLite    = "rqlite"
	SubsystemOlric     = "olric"
	SubsystemIPFS      = "ipfs"
	SubsystemDNS       = "dns"
	SubsystemWireGuard = "wireguard"
	SubsystemSystem    = "system"
	SubsystemNetwork   = "network"
	SubsystemTor       = "tor"
	SubsystemGlobal    = "global"
	SubsystemNamespace = "namespace"
)

// Check IDs for a node, or one of its subsystems, that could not be collected.
const (
	CheckNodeReachable = "node.reachable"
	checkCollectedSfx  = ".collected"
)

const (
	// nodeSubsystem is the subsystem node.reachable is filed under.
	nodeSubsystem = "node"
	// sectionSep separates the sections a collection script prints.
	sectionSep = "===INSPECTOR_SEP==="
	// probeCommand is the cheapest command that proves a session works.
	probeCommand = "true"
	// systemMinSections and networkMinSections are the fewest sections a system
	// and a network script print before the first field the collector reads.
	systemMinSections  = 2
	networkMinSections = 5
	// stderrExcerptMax caps the stderr quoted in a collection error.
	stderrExcerptMax = 200
)

// sshOutputError returns nil when the session produced output to parse, and
// otherwise why it did not. A connection reset, a timeout and a script that
// printed nothing all land here: none of them says anything about the node's
// services, so none may be read as zero values.
func sshOutputError(res SSHResult) error {
	if strings.TrimSpace(res.Stdout) != "" {
		return nil
	}
	switch {
	case res.Err != nil:
		return fmt.Errorf("ssh failed after %d retries: %w%s", res.Retries, res.Err, stderrSuffix(res))
	case res.ExitCode != 0:
		return fmt.Errorf("ssh command exited %d with no output%s", res.ExitCode, stderrSuffix(res))
	default:
		return fmt.Errorf("ssh command returned no output%s", stderrSuffix(res))
	}
}

func stderrSuffix(res SSHResult) string {
	e := strings.TrimSpace(res.Stderr)
	if e == "" {
		return ""
	}
	if len(e) > stderrExcerptMax {
		e = e[:stderrExcerptMax] + "..."
	}
	return " (stderr: " + e + ")"
}

// splitSections splits a collection script's output into sections, and fails when
// the output is empty or cut short of the first minSections sections.
func splitSections(res SSHResult, minSections int) ([]string, error) {
	if err := sshOutputError(res); err != nil {
		return nil, err
	}
	parts := strings.Split(res.Stdout, sectionSep)
	if len(parts) < minSections {
		return nil, fmt.Errorf("truncated output: got %d of %d sections%s", len(parts), minSections, stderrSuffix(res))
	}
	return parts, nil
}

// probeNode runs the cheapest command on the node to see whether a session works at all.
func probeNode(ctx context.Context, node Node) error {
	res := RunSSH(ctx, node, probeCommand)
	if res.OK() {
		return nil
	}
	if res.Err != nil {
		return fmt.Errorf("ssh failed after %d retries: %w%s", res.Retries, res.Err, stderrSuffix(res))
	}
	return fmt.Errorf("ssh command exited %d%s", res.ExitCode, stderrSuffix(res))
}

func (nd *NodeData) markUnreachable(err error) {
	nd.Unreachable = err.Error()
	nd.Errors = append(nd.Errors, "could not collect from node: "+err.Error())
}

// recordFailure files err (when non-nil) as why subsystem has no data.
func (nd *NodeData) recordFailure(subsystem string, err error) {
	if err == nil {
		return
	}
	if nd.Failed == nil {
		nd.Failed = make(map[string]string)
	}
	nd.Failed[subsystem] = err.Error()
	nd.Errors = append(nd.Errors, fmt.Sprintf("%s: %s", subsystem, err.Error()))
}

// collectionResults returns the explicit results for what could not be collected
// on every node: one node.reachable per unreachable node, and one
// <subsystem>.collected per failed subsystem among those selected. Checks read
// nil data as "not collected" and skip, so these are the only results those
// nodes produce for the data.
func collectionResults(data *ClusterData, selected func(string) bool) []CheckResult {
	var out []CheckResult
	hosts := make([]string, 0, len(data.Nodes))
	for h := range data.Nodes {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	for _, h := range hosts {
		nd := data.Nodes[h]
		name := nd.Node.Name()
		if nd.Unreachable != "" {
			out = append(out, Fail(CheckNodeReachable, "Node reachable over SSH", nodeSubsystem, name,
				"could not collect from "+name+": "+nd.Unreachable+"; every check on this node was skipped", Critical))
			continue
		}
		subs := make([]string, 0, len(nd.Failed))
		for s := range nd.Failed {
			subs = append(subs, s)
		}
		sort.Strings(subs)
		for _, s := range subs {
			for _, reported := range reportedSubsystems(s) {
				if !selected(reported) {
					continue
				}
				out = append(out, Fail(reported+checkCollectedSfx, "Subsystem data collected", reported, name,
					"could not collect "+s+" data from "+name+": "+nd.Failed[s]+"; its checks on this node were skipped", Critical))
			}
		}
	}
	return out
}

// reportedSubsystems lists the check subsystems that read what subsystem collected.
func reportedSubsystems(subsystem string) []string {
	if subsystem == SubsystemNamespace {
		return []string{SubsystemNamespace, "webrtc"}
	}
	return []string{subsystem}
}
