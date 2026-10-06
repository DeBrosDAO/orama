package cluster

import "fmt"

// Verdict is the one-line answer to "is everything fine?".
type Verdict struct {
	State    State  `json:"state"`
	Headline string `json:"headline"`
	Critical int    `json:"critical"`
	Warning  int    `json:"warning"`
	Info     int    `json:"info"`
	// NodesHealthy counts nodes whose report came back, NodesTotal the nodes
	// whose state is known, and NodesUnknown those on a release without
	// telemetry, mid-rollout.
	NodesHealthy int `json:"nodes_healthy"`
	NodesTotal   int `json:"nodes_total"`
	NodesUnknown int `json:"nodes_unknown,omitempty"`
}

// Summarize gives the cluster's verdict: the worst component state, made
// worse by any critical alert, and a headline a person can act on.
func Summarize(snap *ClusterSnapshot, components []Component) Verdict {
	v := serviceVerdict(snap, components)
	if v.State == StateUnknown {
		return v
	}
	for _, a := range snap.Alerts {
		switch a.Severity {
		case AlertCritical:
			v.Critical++
		case AlertWarning:
			v.Warning++
		case AlertInfo:
			v.Info++
		}
	}
	if v.Critical > 0 {
		v.State = Worse(v.State, StateDegraded)
	}
	v.Headline = headline(v, components)
	return v
}

// serviceVerdict is the verdict from service states only, without alerts.
func serviceVerdict(snap *ClusterSnapshot, components []Component) Verdict {
	unknown := snap.UnknownCount()
	v := Verdict{State: StateOperational, NodesTotal: snap.TotalCount() - unknown,
		NodesHealthy: snap.HealthyCount(), NodesUnknown: unknown}
	if snap.TotalCount() == 0 {
		return Verdict{State: StateUnknown, Headline: "No nodes to report on"}
	}
	for _, c := range components {
		v.State = Worse(v.State, c.State)
	}
	v.Headline = headline(v, components)
	return v
}

func headline(v Verdict, components []Component) string {
	switch v.State {
	case StateOperational:
		if v.Warning > 0 {
			return fmt.Sprintf("All systems operational · %s to look at", plural(v.Warning, "warning"))
		}
		return "All systems operational"
	case StateOutage:
		return "Outage: " + joinNames(components, StateOutage)
	case StateDegraded:
		if names := joinNames(components, StateDegraded); names != "" {
			return "Degraded: " + names
		}
		return fmt.Sprintf("Degraded: %s", plural(v.Critical, "critical problem"))
	default:
		return "Status unknown"
	}
}

func joinNames(components []Component, s State) string {
	out := ""
	for _, c := range components {
		if c.State != s {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += c.Name
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
