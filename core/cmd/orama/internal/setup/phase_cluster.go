package setup

import (
	"context"
	"fmt"
	"sort"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// stageReleases puts the verified release of the network's channel on every
// machine that still has something to install, fetching it once per
// architecture. A machine that already runs exactly that build is left alone.
func (r *runner) stageReleases(ctx context.Context) error {
	byArch := map[string][]*nodeRun{}
	for _, n := range r.runs {
		if n.needsInstall() {
			byArch[n.facts.Arch] = append(byArch[n.facts.Arch], n)
		} else {
			r.skip(n.plan.IP, StepRelease, "already installed")
		}
	}
	arches := make([]string, 0, len(byArch))
	for a := range byArch {
		arches = append(arches, a)
	}
	sort.Strings(arches)
	for _, arch := range arches {
		if err := r.stageArch(ctx, arch, byArch[arch]); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) stageArch(ctx context.Context, arch string, nodes []*nodeRun) error {
	r.d.Report.Linef("fetching the %s release for linux/%s and verifying it against the network's release root", r.net.Manifest.Channel, arch)
	rel, err := r.d.Releases.Fetch(ctx, r.net, arch)
	if err != nil {
		return fmt.Errorf("release of the %s channel for linux/%s: %w", r.net.Manifest.Channel, arch, err)
	}
	defer func() {
		if rel.Remove != nil {
			_ = rel.Remove()
		}
	}()
	for _, n := range nodes {
		if n.facts.ManifestSHA256 == rel.ManifestSHA256 {
			r.skip(n.plan.IP, StepRelease, "already runs "+rel.Version)
			continue
		}
		r.emit(n.plan.IP, StepRelease, StateRunning, "uploading "+rel.Version)
		if err := n.m.StageRelease(ctx, rel); err != nil {
			r.emit(n.plan.IP, StepRelease, StateFailed, err.Error())
			return fmt.Errorf("machine %s: stage release %s: %w", n.plan.IP, rel.Version, err)
		}
		r.emit(n.plan.IP, StepRelease, StateDone, rel.Version)
	}
	return nil
}

// clusterPhase installs the cluster node on each machine in turn, so a join
// always has the cluster it joins: the first machine creates it (or joins the
// environment's existing one), the others join through a node that is in.
func (r *runner) clusterPhase(ctx context.Context) error {
	if err := r.openVia(ctx); err != nil {
		return err
	}
	r.clusterSize = r.countCluster()
	var done []RecordedNode
	for _, n := range r.runs {
		if err := r.installCluster(ctx, n); err != nil {
			return err
		}
		if r.via == nil {
			r.via = n.m
		}
		done = append(done, RecordedNode{Host: n.plan.IP, User: r.opts.User, Role: r.clusterRole()})
		if err := r.recordCluster(done); err != nil {
			return err
		}
	}
	return nil
}

// clusterRole is what the environment records for the machines: nameservers on
// a domain of the operator's own, nodes otherwise.
func (r *runner) clusterRole() string {
	if r.opts.Domain != "" {
		return "nameserver"
	}
	return "node"
}

// openVia reaches a node of a cluster that already exists, to mint invites on.
// A cluster created by this run has none until its first machine is installed.
func (r *runner) openVia(ctx context.Context) error {
	if !r.plan.JoinsExisting {
		return nil
	}
	existing := r.d.Record.Hosts(r.plan.Env)
	for _, n := range r.runs {
		for _, e := range existing {
			if e.Host == n.plan.IP && n.facts.ClusterInstalled {
				r.via = n.m
				return nil
			}
		}
	}
	e := existing[0]
	m, err := r.d.Enroll.Enroll(ctx, MachineRequest{IP: e.Host, User: e.User, Env: r.plan.Env})
	if err != nil {
		return fmt.Errorf("reach %s, a node of the cluster %q, to mint invites on it: %w", e.Host, r.plan.Env, err)
	}
	r.closers = append(r.closers, m.Close)
	r.via = m
	return nil
}

func (r *runner) installCluster(ctx context.Context, n *nodeRun) error {
	ip := n.plan.IP
	if n.facts.ClusterInstalled {
		r.skip(ip, StepCluster, "orama-node is installed")
		return nil
	}
	r.emit(ip, StepCluster, StateRunning, string(n.plan.Cluster))
	in := ClusterInstall{
		Create: n.plan.Cluster == ClusterCreate, Name: n.plan.Name, IP: ip, User: r.opts.User, Env: r.plan.Env,
		Domain: r.opts.Domain, ACMECA: r.opts.ACMECA, Wallet: r.evm,
	}
	if !in.Create {
		if r.via == nil {
			return clierr.Failure("machine %s joins a cluster, and no node of it is reachable to mint the invite", ip)
		}
		var err error
		if in.Invite, in.Signers, err = r.via.MintInvite(ctx); err != nil {
			return fmt.Errorf("mint an invite for %s on %s: %w", ip, r.via.Host(), err)
		}
	}
	if err := n.m.InstallCluster(ctx, in); err != nil {
		r.emit(ip, StepCluster, StateFailed, err.Error())
		return fmt.Errorf("machine %s: install the cluster node: %w", ip, err)
	}
	if err := n.m.WaitNode(ctx, r.d.Timing.ReadyBudget); err != nil {
		r.emit(ip, StepCluster, StateFailed, err.Error())
		return fmt.Errorf("machine %s: the cluster node did not come up: %w", ip, err)
	}
	r.emit(ip, StepCluster, StateDone, "")
	return nil
}

// recordCluster remembers the cluster environment and every machine installed so
// far, after each one: a run that stops half way leaves a CLI that knows the
// nodes it did install, and the next run joins through them.
func (r *runner) recordCluster(nodes []RecordedNode) error {
	all := append(r.d.Record.Hosts(r.plan.Env), nodes...)
	seen := map[string]bool{}
	uniq := all[:0:0]
	for _, n := range all {
		if !seen[n.Host] {
			seen[n.Host] = true
			uniq = append(uniq, n)
		}
	}
	// An environment that exists keeps its gateway; a new one is reached at the
	// domain, or at the first machine's address while it has none.
	gateway := ""
	switch {
	case r.plan.JoinsExisting:
	case r.opts.Domain != "":
		gateway = "https://" + r.opts.Domain
	default:
		gateway = "https://" + r.runs[0].plan.IP
	}
	if err := r.d.Record.Cluster(r.plan.Env, r.plan.Network, gateway, uniq); err != nil {
		return fmt.Errorf("record the cluster %q in the CLI config: %w", r.plan.Env, err)
	}
	return nil
}

// recordOperator remembers the operator account on the environment, so
// `orama status` shows it.
func (r *runner) recordOperator() error {
	if r.oper == "" {
		return nil
	}
	if err := r.d.Record.Operator(r.plan.Env, r.oper); err != nil {
		return fmt.Errorf("record the operator %s on %q: %w", r.oper, r.plan.Env, err)
	}
	return nil
}

// countCluster is the number of nodes the cluster has when the run is done: the
// ones the environment records and the machines of this run, counted once.
func (r *runner) countCluster() int {
	hosts := map[string]bool{}
	for _, h := range r.d.Record.Hosts(r.plan.Env) {
		hosts[h.Host] = true
	}
	for _, n := range r.runs {
		hosts[n.plan.IP] = true
	}
	return len(hosts)
}
