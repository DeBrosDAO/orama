package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// torRun is a machine of the Tor step.
type torRun struct {
	run  *nodeRun
	host TorHost
	role TorRole
	// index is the machine's position among the authorities; unused for a relay.
	index int
	// keys is the authority's public identity, once its keys exist.
	keys tornet.AuthorityKeys
	// state is what the machine has, read when the step starts and kept up to date by it.
	state TorState
}

func (x *torRun) ip() string { return x.run.plan.IP }

// torFiles are the two network files of the creation. The network starts with the
// first, whose bootstrap mode lets the authorities make their first consensus; the
// manifest pins the final one, which every joiner and every node ends up with.
type torFiles struct {
	first, final       []byte
	firstSHA, finalSHA string
	// sharedSubnets: the file allows a circuit two relays of one /16 (allow_shared_subnets).
	sharedSubnets bool
}

// torRuns are the machines that run a Tor role, authorities first, and checks each
// can be one.
func (r *runner) torRuns() ([]*torRun, error) {
	var runs []*torRun
	for _, n := range r.fullRuns() {
		if n.plan.Tor == "" {
			continue
		}
		h, ok := n.m.(TorHost)
		if !ok {
			return nil, fmt.Errorf("machine %s cannot run a Tor role: its connection (%T) does not make authority keys or install one", n.plan.IP, n.m)
		}
		x := &torRun{run: n, host: h, role: n.plan.Tor, index: len(runs)}
		runs = append(runs, x)
		r.create.tor[n] = x
	}
	return runs, nil
}

// torPhase makes the network's own Tor network once the chains run, or does nothing
// when the run makes none. Every part asks the machines what they already have:
//
//  1. each authority's keys are made on its own machine (kept if they exist);
//  2. the network file is built from the authorities' public identities, and the
//     one the manifest pins is written into the published directory;
//  3. the roles are installed and started: the authorities within minutes of each
//     other, then the relays;
//  4. the run waits until the machines hold a consensus signed by a majority of the
//     authorities that lists them: no authority votes Running for 30 minutes after it
//     starts, so that takes about an hour;
//  5. the nodes leave the bootstrap mode one at a time, authorities first, an
//     authority only once the machine itself allows it.
func (r *runner) torPhase(ctx context.Context) error {
	runs := r.create.torRuns
	if len(runs) == 0 {
		return nil
	}
	if err := r.torMakeKeys(ctx, runs); err != nil {
		return err
	}
	files, err := r.torNetworkFiles(runs)
	if err != nil {
		return err
	}
	// What the machines run is read, and a machine that runs a file this run did not make
	// is refused, before the manifest pins this run's file: a pin of a file no machine
	// runs would mislead every joiner.
	if err := r.torReadStates(ctx, runs); err != nil {
		return err
	}
	if err := r.torRefuseForeign(runs, files); err != nil {
		return err
	}
	if err := r.publishWith(r.genesis, files.final); err != nil {
		return err
	}
	r.res.Created.TorSharedSubnets = files.sharedSubnets
	if err := r.torInstallAll(ctx, runs, files); err != nil {
		return err
	}
	if err := r.torStartAll(ctx, runs); err != nil {
		return err
	}
	r.d.Report.Linef("the first consensus needs a majority of the authorities to vote, and a directory authority casts no Running vote for %s after it starts: "+
		"expect it about an hour after the authorities were started", tornet.AuthorityLearnWindow)
	if err := r.torWaitReady(ctx, runs, time.Time{}, "the network's first Tor consensus"); err != nil {
		return err
	}
	if err := r.torLeaveBootstrap(ctx, runs, files); err != nil {
		return err
	}
	r.d.Report.Linef("the Tor network %s is up: %d directory authorities and %d relays hold a consensus signed by a majority of the authorities; its network file is pinned in the manifest (sha256 %s)",
		r.net.Manifest.Name, r.opts.Create.TorAuthorities, len(runs)-r.opts.Create.TorAuthorities, files.finalSHA)
	return nil
}

// authorityRuns are the runs that are authorities.
func authorityRuns(runs []*torRun) []*torRun {
	var out []*torRun
	for _, x := range runs {
		if x.role == TorDirauth {
			out = append(out, x)
		}
	}
	return out
}

// torEach runs fn on the machines in parallel, as far as the cluster can spare them.
func (r *runner) torEach(ctx context.Context, runs []*torRun, fn func(context.Context, *torRun) error) error {
	nodes := make([]*nodeRun, len(runs))
	for i, x := range runs {
		nodes[i] = x.run
	}
	return r.parallel(ctx, nodes, func(ctx context.Context, n *nodeRun) error { return fn(ctx, r.create.tor[n]) })
}

// torMakeKeys has each authority's machine make its keys, or say it has them.
func (r *runner) torMakeKeys(ctx context.Context, runs []*torRun) error {
	return r.torEach(ctx, authorityRuns(runs), func(ctx context.Context, x *torRun) error {
		ip, want := x.ip(), AuthorityKeysInput{Nickname: torAuthorityNickname(x.index), Address: x.ip()}
		r.emit(ip, StepTor, StateRunning, "making the directory authority's keys on the machine")
		keys, err := x.host.AuthorityKeys(ctx, want)
		if err != nil {
			r.emit(ip, StepTor, StateFailed, err.Error())
			return fmt.Errorf("machine %s: make the directory authority's keys: %w", ip, err)
		}
		if err := checkAuthorityKeys(keys, want, time.Now()); err != nil {
			err = fmt.Errorf("machine %s: %w", ip, err)
			r.emit(ip, StepTor, StateFailed, err.Error())
			return err
		}
		x.keys = keys
		if keys.Created {
			r.emit(ip, StepTor, StateDone, "made the authority's keys: the identity key stays on the machine")
		} else {
			r.emit(ip, StepTor, StateSkipped, "the authority's keys exist; they are kept")
		}
		return nil
	})
}

// checkAuthorityKeys checks what a machine says about its authority's keys, which goes into
// a network file every node trusts and into an install command: it is the authority that was
// asked for, on the ports the global firewall opens, with its bundle where the command puts
// it, and a certificate that does not expire within torMinCertValidity.
func checkAuthorityKeys(keys tornet.AuthorityKeys, want AuthorityKeysInput, now time.Time) error {
	a := keys.Authority
	wantBundle := filepath.Join(constants.GlobalTorAuthorityKeysDir, tornet.CeremonyDeployDir, want.Nickname)
	switch {
	case a.Nickname != want.Nickname || a.Address != want.Address:
		return fmt.Errorf("answered with the authority %s at %s, not %s at %s", a.Nickname, a.Address, want.Nickname, want.Address)
	case a.ORPort != constants.GlobalTorORPort || a.DirPort != constants.GlobalTorDirPort:
		return fmt.Errorf("authority %s is on ports %d/%d, but the global firewall opens %d/%d", a.Nickname, a.ORPort, a.DirPort, constants.GlobalTorORPort, constants.GlobalTorDirPort)
	case keys.Bundle != wantBundle:
		return fmt.Errorf("authority %s says its key bundle is %q, but the command makes it at %q", a.Nickname, keys.Bundle, wantBundle)
	case keys.Expires.Before(now.Add(torMinCertValidity)):
		return fmt.Errorf("the signing certificate of authority %s expires on %s, less than %s from now: the machine's keys are not ones to start a network with",
			a.Nickname, keys.Expires.Format(time.DateOnly), torMinCertValidity)
	}
	return nil
}

// circuitHops are the addresses of the machines circuits are built over: the relays. The
// authorities run the network but are not counted for path selection; a network with no
// relay machine is built over its authorities, which are then the only relays there are.
func circuitHops(runs []*torRun) []string {
	var relays, all []string
	for _, x := range runs {
		all = append(all, x.ip())
		if x.role == TorRelay {
			relays = append(relays, x.ip())
		}
	}
	if len(relays) == 0 {
		return all
	}
	return relays
}

// torNetworkFiles builds the network file from the authorities' identities, and
// shows the operator the identities to read against the machines' own output.
func (r *runner) torNetworkFiles(runs []*torRun) (torFiles, error) {
	var auths []tornet.Authority
	for _, x := range authorityRuns(runs) {
		auths = append(auths, x.keys.Authority)
	}
	n := tornet.Network{
		Name: r.net.Manifest.Name, Private: true, Bootstrap: true, AllowExit: r.opts.Exit,
		AllowSharedSubnets:    distinctSubnets(circuitHops(runs)) < minDistinctSubnets,
		HSDirMinUptimeHours:   torHSDirMinUptimeHours,
		VotingIntervalMinutes: torVotingIntervalMinutes, VoteDelaySeconds: torDelaySeconds, DistDelaySeconds: torDelaySeconds,
		Authorities: auths,
	}
	first, err := n.Marshal()
	if err != nil {
		return torFiles{}, fmt.Errorf("build the Tor network file: %w", err)
	}
	n.Bootstrap = false
	final, err := n.Marshal()
	if err != nil {
		return torFiles{}, fmt.Errorf("build the Tor network file: %w", err)
	}
	if n.AllowSharedSubnets {
		r.d.Report.Linef("the machines circuits are built over (the relays; the authorities when there are none) are in fewer than %d /16 networks, so the Tor network allows a circuit to use "+
			"two relays of one /16 (allow_shared_subnets): it weakens path selection, and no circuit can be built otherwise. The published tor-network.json says so, and so does `orama network add`", minDistinctSubnets)
	}
	for _, line := range strings.Split(torSeatLines(auths), "\n") {
		r.d.Report.Linef("  Tor authority %s", line)
	}
	return torFiles{first: first, final: final, firstSHA: netregistry.Digest(first), finalSHA: netregistry.Digest(final), sharedSubnets: n.AllowSharedSubnets}, nil
}

// torReadStates reads what each machine has of its role.
func (r *runner) torReadStates(ctx context.Context, runs []*torRun) error {
	return r.torEach(ctx, runs, func(ctx context.Context, x *torRun) error {
		st, err := x.host.TorState(ctx, x.role)
		if err != nil {
			r.emit(x.ip(), StepTor, StateFailed, err.Error())
			return fmt.Errorf("machine %s: read its Tor %s: %w", x.ip(), x.role, err)
		}
		x.state = st
		return nil
	})
}

// torRefuseForeign refuses a machine whose installed role runs a network file other than
// this run's two, because it is not what this run made. Nothing has been changed yet.
func (r *runner) torRefuseForeign(runs []*torRun, files torFiles) error {
	for _, x := range runs {
		if x.state.Installed && x.state.NetworkSHA256 != files.firstSHA && x.state.NetworkSHA256 != files.finalSHA {
			err := fmt.Errorf("machine %s runs a Tor network file (sha256 %q) that this run did not make (sha256 %s, or %s once the bootstrap mode is left): "+
				"the machine was set up for another network or with other flags; this run does not change it and has pinned nothing", x.ip(), x.state.NetworkSHA256, files.firstSHA, files.finalSHA)
			r.emit(x.ip(), StepTor, StateFailed, err.Error())
			return err
		}
	}
	return nil
}

// torInstallAll installs the roles that are not installed, with the bootstrap file. A
// machine that already has the role (with either of this run's files, torRefuseForeign)
// is left as it is.
func (r *runner) torInstallAll(ctx context.Context, runs []*torRun, files torFiles) error {
	var todo []*torRun
	for _, x := range runs {
		if x.state.Installed {
			r.emit(x.ip(), StepTor, StateSkipped, fmt.Sprintf("the %s is installed", x.role))
			continue
		}
		todo = append(todo, x)
	}
	return r.torEach(ctx, todo, func(ctx context.Context, x *torRun) error {
		if err := r.torInstall(ctx, x, files.first); err != nil {
			return err
		}
		x.state.Installed, x.state.NetworkSHA256 = true, files.firstSHA
		return nil
	})
}

// torInstall installs x's role with the network file.
func (r *runner) torInstall(ctx context.Context, x *torRun, network []byte) error {
	ip := x.ip()
	in := TorInstall{Role: x.role, IP: ip, Contact: r.contact(), Network: network, Exit: x.run.plan.Exit}
	switch x.role {
	case TorDirauth:
		in.Bundle = x.keys.Bundle
	default:
		in.NodeID = x.run.plan.Name
	}
	r.emit(ip, StepTor, StateRunning, fmt.Sprintf("installing the %s", x.role))
	if err := x.host.InstallTor(ctx, in); err != nil {
		r.emit(ip, StepTor, StateFailed, err.Error())
		return fmt.Errorf("machine %s: install the Tor %s: %w", ip, x.role, err)
	}
	r.emit(ip, StepTor, StateDone, fmt.Sprintf("the %s is installed", x.role))
	return nil
}

// torStartAll starts the roles that do not run. The authorities go one after the
// other without waiting for anything: they must be up to vote together.
func (r *runner) torStartAll(ctx context.Context, runs []*torRun) error {
	for _, x := range runs {
		if x.state.Active {
			continue
		}
		r.emit(x.ip(), StepTor, StateRunning, fmt.Sprintf("starting the %s", x.role))
		if err := x.host.StartTor(ctx, x.role); err != nil {
			r.emit(x.ip(), StepTor, StateFailed, err.Error())
			return fmt.Errorf("machine %s: %w", x.ip(), err)
		}
		x.state.Active = true
		r.emit(x.ip(), StepTor, StateDone, fmt.Sprintf("the %s is started", x.role))
	}
	return nil
}
