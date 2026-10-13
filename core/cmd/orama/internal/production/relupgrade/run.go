package relupgrade

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/upgrade"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// Options are the flags of `orama upgrade`.
type Options struct {
	// Env is the environment to upgrade; empty is the active one.
	Env string
	// Node limits the upgrade to one node of the environment (its public IP).
	Node string
	// Yes skips the confirmation. DryRun prints the plan and stops.
	Yes, DryRun bool
	// Reinstall puts the release in place on nodes that already run it.
	Reinstall bool
	// SSH reads what the nodes run over SSH instead of the gateway's telemetry.
	SSH bool
	// Delay is how many seconds a node has to rejoin before the rollout stops.
	Delay int
	// In answers the confirmation; Out receives the plan. Nil are os.Stdin and os.Stdout.
	In  io.Reader
	Out io.Writer
}

// roller is the rolling upgrade (production/upgrade.RemoteUpgrader).
type roller interface {
	Connect() ([]inspector.Node, func(), error)
	Plan(nodes []inspector.Node) (*rollout.Plan, error)
	Roll(plan *rollout.Plan, afterUpgrade func(inspector.Node) error) error
}

// seams are the parts of `orama upgrade` that reach the network or a node; a
// test replaces them.
type seams struct {
	arch   func(node inspector.Node) (string, error)
	fetch  func(ctx context.Context, p releasefetch.Params) (*releasefetch.Release, error)
	states func(ctx context.Context, env string, ssh bool) (map[string]NodeState, error)
	stage  func(node inspector.Node, rel push.ReleaseFiles) (string, error)
	global func(node inspector.Node) error
}

// runner is one `orama upgrade`.
type runner struct {
	opts   Options
	env    string
	target Target
	home   string
	now    time.Time
	roller roller
	seams  seams
}

// Run is `orama upgrade`.
func Run(ctx context.Context, opts Options) error {
	env, err := resolveEnvironment(opts.Env)
	if err != nil {
		return err
	}
	registry, err := cli.LoadNetworks()
	if err != nil {
		return clierr.Failure("load the network registry: %v", err)
	}
	target, err := ResolveTarget(env, registry)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	home, err := config.ConfigDir()
	if err != nil {
		return clierr.Failure("%v", err)
	}
	flags := &upgrade.Flags{Env: env.Name, NodeFilter: opts.Node, Yes: true, Delay: opts.Delay}
	r := &runner{
		opts: opts, env: env.Name, target: target, home: home, now: time.Now(),
		roller: realRoller{upgrade.NewRemoteUpgrader(flags)}, seams: defaultSeams(),
	}
	return r.run(ctx)
}

func resolveEnvironment(name string) (*cli.Environment, error) {
	if name == "" {
		env, err := cli.GetActiveEnvironment()
		if err != nil {
			return nil, clierr.Usage("no --env given and no active network: %v", err)
		}
		return env, nil
	}
	env, err := cli.GetEnvironmentByName(name)
	if err != nil {
		return nil, clierr.NotFound("%v", err)
	}
	return env, nil
}

func (r *runner) run(ctx context.Context) error {
	out := r.opts.Out
	if out == nil {
		out = os.Stdout
	}
	nodes, cleanup, err := r.roller.Connect()
	if err != nil {
		return err
	}
	defer cleanup()
	order, err := r.roller.Plan(nodes)
	if err != nil {
		return clierr.Conflict("%v", err)
	}
	releases, err := r.fetchReleases(ctx, order.Steps, out)
	if err != nil {
		return err
	}
	defer releases.remove()
	states, err := r.seams.states(ctx, r.env, r.opts.SSH)
	if err != nil {
		return clierr.Unavailable("read what the nodes run: %v\n  Sign in with `orama auth login`, or pass --ssh to read every node over SSH instead", err)
	}
	plan := BuildPlan(r.target, releases.version, order.Steps, states, r.opts.Reinstall)
	plan.Render(out)
	fmt.Fprintln(out, plan.Summary())
	if r.opts.DryRun || plan.Runs() == 0 {
		return nil
	}
	if !r.opts.Yes {
		fmt.Fprintf(out, "\nUpgrade %s to %s. Type 'yes' to confirm: ", plan.hosts(), plan.Version)
		if err := clierr.Confirm(r.input(), "yes"); err != nil {
			fmt.Fprintln(out, "Aborted.")
			return err
		}
	}
	steps := plan.Steps(order.Steps)
	if err := r.stageAll(steps, releases, out); err != nil {
		return err
	}
	rolling := &rollout.Plan{Steps: steps, Nameservers: order.Nameservers}
	return r.roller.Roll(rolling, r.afterUpgrade)
}

func (r *runner) input() io.Reader {
	if r.opts.In != nil {
		return r.opts.In
	}
	return os.Stdin
}

// afterUpgrade refreshes the global layer of a node once its cluster services
// are upgraded. The node says whether it has one: the telemetry in the plan is
// only what is shown.
func (r *runner) afterUpgrade(node inspector.Node) error {
	if err := r.seams.global(node); err != nil {
		return fmt.Errorf("the global layer of %s was not refreshed: %w", node.Host, err)
	}
	return nil
}

// realRoller adapts the existing rolling upgrade.
type realRoller struct{ up *upgrade.RemoteUpgrader }

func (r realRoller) Connect() ([]inspector.Node, func(), error) { return r.up.Connect() }

func (r realRoller) Plan(nodes []inspector.Node) (*rollout.Plan, error) { return r.up.Plan(nodes) }

func (r realRoller) Roll(plan *rollout.Plan, after func(inspector.Node) error) error {
	r.up.AfterUpgrade = after
	return r.up.Roll(plan)
}

func defaultSeams() seams {
	return seams{
		arch:   nodeArch,
		fetch:  releasefetch.Fetch,
		states: snapshotStates,
		stage:  push.ReleaseToNode,
		global: refreshGlobalLayer,
	}
}

const (
	// globalRefreshArgs are the staged CLI's arguments for the global layer.
	globalRefreshArgs = "maint global refresh"
	// globalUnitsProbe prints yes when the node has the global layer's units.
	globalUnitsProbe = "ls /etc/systemd/system/orama-global-*.service >/dev/null 2>&1 && echo yes || echo no"
)

// refreshGlobalLayer runs `orama maint global refresh` with the node's staged
// CLI, behind the rolling upgrade's guard, on a node that has the global
// layer; a cluster-only node has nothing to refresh.
func refreshGlobalLayer(node inspector.Node) error {
	out, err := remotessh.RunSSHOutput(node, globalUnitsProbe)
	if err != nil {
		return fmt.Errorf("check whether %s has the global layer: %w", node.Host, err)
	}
	if strings.TrimSpace(out) != "yes" {
		return nil
	}
	fmt.Printf("  Refreshing the global layer on %s...\n", node.Host)
	return remotessh.RunSSHStreaming(node, upgrade.StagedCLICommand(remotessh.SudoPrefix(node), globalRefreshArgs))
}
