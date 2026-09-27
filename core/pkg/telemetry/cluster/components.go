package cluster

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// State is how well a component, or the whole cluster, is serving.
type State string

const (
	// StateOperational means every node that runs the component serves it.
	StateOperational State = "operational"
	// StateDegraded means it still serves, with some nodes failing.
	StateDegraded State = "degraded"
	// StateOutage means it does not serve.
	StateOutage State = "outage"
	// StateUnknown means no node reported on it.
	StateUnknown State = "unknown"
)

// stateRank orders states from best to worst.
var stateRank = map[State]int{StateOperational: 0, StateUnknown: 1, StateDegraded: 2, StateOutage: 3}

// Worse returns the worse of two states.
func Worse(a, b State) State {
	if stateRank[b] > stateRank[a] {
		return b
	}
	return a
}

// wgHandshakeStaleSec is how old a WireGuard handshake may be before the
// tunnel is counted down. WireGuard re-handshakes every two minutes on a
// tunnel carrying traffic; the alert for a stale peer uses the same bound.
const wgHandshakeStaleSec = 180

// chainStallSec is how long the chain may go without a block before a node's
// view of it counts as stalled.
const chainStallSec = 60

// Component is one service's health across the cluster.
type Component struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   State  `json:"state"`
	Healthy int    `json:"healthy"`
	Total   int    `json:"total"`
	// Summary is one plain sentence on the state, for people.
	Summary string `json:"summary"`
}

// probe classifies one node for one component: applies is whether the node
// runs it at all, healthy whether it serves it.
type probe func(r *report.NodeReport) (applies, healthy bool)

// componentDef is a component and how a node is judged for it.
type componentDef struct {
	id, name string
	probe    probe
	// outage, when set, decides whether the component is down from the whole
	// snapshot: for the database that is raft quorum, which a count of healthy
	// nodes cannot see (non-voters count toward it, and a leader's view knows
	// voters that sent no report). Without it, a component is down only when no
	// node serves it.
	outage func(snap *ClusterSnapshot) bool
	// runsOn says whether a node that sent no report runs the component, so
	// its silence counts against it. Nil means every node runs it.
	runsOn func(NodeRef) bool
}

// unknownPlacement is for components a node's role does not predict: a
// silent node is left out of them rather than guessed at.
func unknownPlacement(NodeRef) bool { return false }

// componentDefs is every component, in the order people read them.
var componentDefs = []componentDef{
	{id: "gateway", name: "API Gateway", probe: probeGateway},
	{id: "database", name: "Database (RQLite)", probe: probeDatabase, outage: databaseOutage},
	{id: "cache", name: "Cache (Olric)", probe: probeCache},
	{id: "storage", name: "Storage (IPFS)", probe: probeStorage},
	{id: "vault", name: "Secrets Vault", probe: probeVault},
	{id: "dns", name: "DNS & TLS", probe: probeDNS, runsOn: NodeRef.IsNameserver},
	{id: "mesh", name: "Private Network (WireGuard)", probe: probeMesh},
	{id: "chain", name: "Orama L1 Chain", probe: probeChain, runsOn: unknownPlacement},
}

// Components judges every component from the snapshot. A node that could not
// be collected counts against every component it runs, since it serves none of
// them. A node whose state is unknown — it runs a release that serves no
// telemetry, as during a rolling upgrade — counts neither way. A component no
// node runs is left out.
func Components(snap *ClusterSnapshot) []Component {
	out := make([]Component, 0, len(componentDefs))
	for _, def := range componentDefs {
		c := judge(def, snap)
		if c.Total == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

func judge(def componentDef, snap *ClusterSnapshot) Component {
	c := Component{ID: def.id, Name: def.name}
	for _, n := range snap.Nodes {
		if n.Unknown {
			continue
		}
		if n.Report == nil {
			if def.runsOn == nil || def.runsOn(n.Node) {
				c.Total++
			}
			continue
		}
		applies, healthy := def.probe(n.Report)
		if !applies {
			continue
		}
		c.Total++
		if healthy {
			c.Healthy++
		}
	}
	c.State = componentState(c.Healthy, c.Total)
	if c.Total > 0 && def.outage != nil && def.outage(snap) {
		c.State = StateOutage
	}
	c.Summary = componentSummary(c)
	return c
}

// componentState: all healthy is operational, none is an outage, anything
// between is degraded.
func componentState(healthy, total int) State {
	switch {
	case total == 0:
		return StateUnknown
	case healthy == total:
		return StateOperational
	case healthy == 0:
		return StateOutage
	default:
		return StateDegraded
	}
}

func componentSummary(c Component) string {
	switch c.State {
	case StateOperational:
		if c.Total == 1 {
			return "The node is healthy"
		}
		return fmt.Sprintf("All %d nodes healthy", c.Total)
	case StateDegraded:
		return fmt.Sprintf("%d of %d nodes healthy", c.Healthy, c.Total)
	case StateOutage:
		return fmt.Sprintf("Down: %d of %d nodes healthy", c.Healthy, c.Total)
	default:
		return "No node reported"
	}
}
