package relupgrade

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// Action is what `orama upgrade` does with one node.
type Action string

const (
	// ActionUpgrade: the node runs an older release than the channel's.
	ActionUpgrade Action = "upgrade"
	// ActionReinstall: the node runs the channel's release and --reinstall
	// asked for it to be put in place again.
	ActionReinstall Action = "reinstall"
	// ActionCurrent: the node already runs the channel's release.
	ActionCurrent Action = "current"
	// ActionAhead: the node runs a release newer than the channel's. An upgrade
	// never goes back, so it is left alone.
	ActionAhead Action = "ahead"
)

// Runs reports whether the action restarts the node.
func (a Action) Runs() bool { return a == ActionUpgrade || a == ActionReinstall }

// NodeState is what the cluster's telemetry says about one node.
type NodeState struct {
	// Version is the release the node runs; empty when its report has none.
	Version string
	// Global is true when the node runs units of the global layer.
	Global bool
}

// Row is one node of the plan.
type Row struct {
	Host string
	// Raft is the node's place in the rollout: leader, follower, nameserver.
	Role    string
	Current string
	Action  Action
	Global  bool
}

// Plan is what `orama upgrade` shows before it changes anything.
type Plan struct {
	Network string
	Channel string
	Version string
	// Rows are every node, in rollout order.
	Rows []Row
}

// Steps are the rollout steps of the nodes the plan restarts, in order.
func (p *Plan) Steps(all []rollout.Step) []rollout.Step {
	runs := map[string]bool{}
	for _, r := range p.Rows {
		runs[r.Host] = r.Action.Runs()
	}
	var out []rollout.Step
	for _, s := range all {
		if runs[s.Node.Host] {
			out = append(out, s)
		}
	}
	return out
}

// Runs counts the nodes the plan restarts.
func (p *Plan) Runs() int {
	n := 0
	for _, r := range p.Rows {
		if r.Action.Runs() {
			n++
		}
	}
	return n
}

// Classify decides what to do with a node on current when the channel's
// release is target. A version this code cannot order (a development build) is
// treated as older: the channel's release replaces it.
func Classify(current, target string, reinstall bool) Action {
	if current == "" {
		return ActionUpgrade
	}
	cmp, err := autoupdate.Compare(current, target)
	switch {
	case err != nil || cmp < 0:
		return ActionUpgrade
	case cmp > 0:
		return ActionAhead
	case reinstall:
		return ActionReinstall
	default:
		return ActionCurrent
	}
}

// BuildPlan joins the rollout order with what each node runs. The order is the
// rollout's: followers first, nameservers spread, the leader last.
func BuildPlan(t Target, version string, order []rollout.Step, states map[string]NodeState, reinstall bool) *Plan {
	p := &Plan{Network: t.Network, Channel: t.Manifest.Channel, Version: version}
	for _, s := range order {
		st := states[s.Node.Host]
		role := string(s.Role)
		if s.IsNameserver {
			role += ", nameserver"
		}
		p.Rows = append(p.Rows, Row{
			Host: s.Node.Host, Role: role, Current: st.Version,
			Action: Classify(st.Version, version, reinstall), Global: st.Global,
		})
	}
	return p
}

// Render prints the plan: the release, then one row per node.
func (p *Plan) Render(w io.Writer) {
	fmt.Fprintf(w, "Network %s, channel %s: newest release %s\n\n", p.Network, p.Channel, p.Version)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tROLE\tRUNNING\tTARGET\tACTION\tGLOBAL LAYER")
	for _, r := range p.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Host, r.Role, orUnknown(r.Current), p.Version, r.Action, globalWord(r))
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	for _, r := range p.Rows {
		if r.Action == ActionAhead {
			fmt.Fprintf(w, "  %s runs %s, newer than the channel's %s: left alone.\n", r.Host, r.Current, p.Version)
		}
	}
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func globalWord(r Row) string {
	switch {
	case !r.Global:
		return "-"
	case r.Action.Runs():
		return "refreshed after the node"
	default:
		return "unchanged"
	}
}

// Summary is one line on what happens next.
func (p *Plan) Summary() string {
	n := p.Runs()
	if n == 0 {
		return fmt.Sprintf("Every node already runs %s; nothing to do (pass --reinstall to put it in place again).", p.Version)
	}
	return fmt.Sprintf("%d of %d nodes will be restarted one at a time, with a health and quorum check between nodes.", n, len(p.Rows))
}

// hosts lists the hosts of the rows that run.
func (p *Plan) hosts() string {
	var hs []string
	for _, r := range p.Rows {
		if r.Action.Runs() {
			hs = append(hs, r.Host)
		}
	}
	return strings.Join(hs, ", ")
}
