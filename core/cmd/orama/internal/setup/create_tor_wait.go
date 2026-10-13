package setup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// torReportEvery is how many polls pass between two repeats of the same wait, so a
	// wait of an hour is not a screenful of identical lines.
	torReportEvery = 10
	// refusalDetailMax bounds the part of a machine's refusal that is repeated to the operator.
	refusalDetailMax = 400
	// torRunningFlag is the consensus flag of a relay the authorities found up: a relay
	// that is listed without it is not used.
	torRunningFlag = "Running"
)

// torReady says whether the machine's role is part of a Tor network that works: its
// unit runs, tor made its identity, and the consensus it holds is valid, signed by a
// majority of the network's authorities, and lists it. after, when set, also asks for a
// consensus newer than that moment (the machine's own clock, the one its consensus is
// judged by): a role restarted at that moment has to be listed again, and the consensus
// it held before says nothing about the restart. It returns the reason when not.
func torReady(st TorState, authorities int, after time.Time) (bool, string) {
	switch {
	case !st.Installed:
		return false, "the role is not installed"
	case !st.Active:
		return false, "its unit is not active"
	case st.Info == nil:
		return false, "tor has not made its data directory yet"
	case st.Info.Fingerprint == "":
		return false, "tor has not made its identity yet"
	case st.Info.Consensus == nil:
		return false, "it holds no consensus yet"
	}
	c, majority := st.Info.Consensus, authorities/2+1
	switch {
	case !c.Valid:
		return false, fmt.Sprintf("its consensus (valid after %s) is no longer valid", c.ValidAfter.Format(time.RFC3339))
	case c.Signatures < majority:
		return false, fmt.Sprintf("its consensus is signed by %d of the %d authorities, and a majority (%d) is needed", c.Signatures, authorities, majority)
	case !after.IsZero() && !c.ValidAfter.After(after):
		return false, fmt.Sprintf("it still holds the consensus valid after %s, from before its restart at %s", c.ValidAfter.Format(time.RFC3339), after.Format(time.RFC3339))
	case !c.Listed:
		return false, "the consensus does not list it"
	case !slices.Contains(c.ListedFlags, torRunningFlag):
		return false, fmt.Sprintf("the consensus lists it without the %s flag (its flags: %s), so no client would use it", torRunningFlag, strings.Join(c.ListedFlags, " "))
	}
	return true, ""
}

// torWaitReady polls the machines until every one is ready (torReady), or the
// deadline passes. It names what it waits for and, while it waits, which machines
// are not ready and why.
func (r *runner) torWaitReady(ctx context.Context, runs []*torRun, after time.Time, what string) error {
	t := r.d.Timing
	authorities := r.opts.Create.TorAuthorities
	r.d.Report.Linef("waiting for %s (read from the machines' own tor, `orama global tor info`; nothing here is timed)", what)
	var last, reported string
	polls, failed := 0, 0
	err := pollUntil(ctx, t.TorPoll, t.TorConsensusDeadline, what, func(ctx context.Context) (bool, error) {
		var waiting []string
		for _, x := range runs {
			st, err := x.host.TorState(ctx, x.role)
			if err != nil {
				// A poll that cannot be answered is not an answer; several in a row end the wait.
				if failed++; failed >= syncPollErrors {
					return false, fmt.Errorf("machine %s could not be polled %d times in a row: %w", x.ip(), failed, err)
				}
				return false, nil
			}
			failed = 0
			x.state = st
			if ok, why := torReady(st, authorities, after); !ok {
				waiting = append(waiting, fmt.Sprintf("%s (%s): %s", x.ip(), x.role, why))
			}
		}
		if len(waiting) == 0 {
			return true, nil
		}
		last = strings.Join(waiting, "; ")
		if key := waitingKey(waiting); key != reported || polls%torReportEvery == 0 {
			r.d.Report.Linef("  still waiting for %s: %s", what, last)
			reported = key
		}
		polls++
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("%w; where it stands: %s", err, last)
	}
	return nil
}

// waitingKey identifies which machines a wait is still on, so that a change of who is
// waiting is reported at once and a reason that only changes its numbers is not.
func waitingKey(waiting []string) string {
	ips := make([]string, len(waiting))
	for i, w := range waiting {
		ips[i], _, _ = strings.Cut(w, " ")
	}
	return strings.Join(ips, ",")
}

// torLeaveBootstrap makes every machine run the final network file, which does not
// assume reachability: the authorities test the relays themselves from here on.
//
// The authorities go first and one at a time, the machine itself saying when the next
// may be restarted (torRestart), and each must be listed in a consensus made after its
// restart before the next is touched, because the machine judges the others by what
// that consensus says. The relays then go together: a relay that is down for a moment
// costs the network no consensus. The last wait is for every machine to be listed in a
// consensus made after the last restart.
func (r *runner) torLeaveBootstrap(ctx context.Context, runs []*torRun, files torFiles) error {
	var auths, relays []*torRun
	for _, x := range runs {
		switch {
		case x.state.NetworkSHA256 == files.finalSHA && !x.state.RestartPending:
			r.emit(x.ip(), StepTor, StateSkipped, "already runs the network file without the bootstrap mode")
		case x.role == TorDirauth:
			auths = append(auths, x)
		default:
			relays = append(relays, x)
		}
	}
	if len(auths)+len(relays) == 0 {
		return nil
	}
	r.d.Report.Linef("leaving the bootstrap mode: %d authority(ies), one at a time, then %d relay(s) together. An authority cannot be restarted until the others have run for %s, "+
		"so the authorities take about that long each after the first.", len(auths), len(relays), tornet.AuthorityLearnWindow)
	// restartedAt is the latest restart of any machine, authorities included: the last wait asks every
	// machine for a consensus made after it, also when there are no relays.
	var restartedAt time.Time
	for _, x := range auths {
		at, err := r.torSwitch(ctx, x, files)
		if err != nil {
			return err
		}
		restartedAt = latest(restartedAt, at)
		if err := r.torWaitReady(ctx, []*torRun{x}, at, "the consensus that lists "+x.ip()+" again"); err != nil {
			r.emit(x.ip(), StepTor, StateFailed, err.Error())
			return fmt.Errorf("machine %s: %w", x.ip(), err)
		}
	}
	var mu sync.Mutex
	err := r.torEach(ctx, relays, func(ctx context.Context, x *torRun) error {
		at, err := r.torSwitch(ctx, x, files)
		mu.Lock()
		defer mu.Unlock()
		restartedAt = latest(restartedAt, at)
		return err
	})
	if err != nil {
		return err
	}
	if err := r.torWaitReady(ctx, runs, restartedAt, "every machine to be listed in a consensus of the final Tor network file"); err != nil {
		return err
	}
	for _, x := range append(auths, relays...) {
		r.emit(x.ip(), StepTor, StateDone, fmt.Sprintf("the %s runs without the bootstrap mode and is listed", x.role))
	}
	return nil
}

// latest is the later of two times.
func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// torSwitch installs the final network file on x if it has not got it and restarts x.
// It returns the machine's clock just after the restart.
func (r *runner) torSwitch(ctx context.Context, x *torRun, files torFiles) (time.Time, error) {
	ip := x.ip()
	if x.state.NetworkSHA256 != files.finalSHA {
		if err := r.torInstall(ctx, x, files.final); err != nil {
			return time.Time{}, err
		}
		x.state.NetworkSHA256 = files.finalSHA
	}
	if err := r.torRestart(ctx, x); err != nil {
		return time.Time{}, err
	}
	st, err := x.host.TorState(ctx, x.role)
	if err != nil {
		r.emit(ip, StepTor, StateFailed, err.Error())
		return time.Time{}, fmt.Errorf("machine %s: read its Tor %s after the restart: %w", ip, x.role, err)
	}
	x.state = st
	return st.Now, nil
}

// torRestart restarts x's role. An authority is refused by its own machine while another
// has started less than tornet.AuthorityLearnWindow ago (website/src/docs/operator/tor-network.mdx,
// "Restarting authorities"): that refusal, and no other error, is waited out by asking again,
// because it changes nothing on the machine and is the rule's own answer to "not yet".
func (r *runner) torRestart(ctx context.Context, x *torRun) error {
	ip := x.ip()
	r.emit(ip, StepTor, StateRunning, fmt.Sprintf("restarting the %s with the final network file", x.role))
	t, polls := r.d.Timing, 0
	err := pollUntil(ctx, t.TorPoll, t.TorRollDeadline, "the machine to allow the restart of its authority", func(ctx context.Context) (bool, error) {
		err := x.host.RestartTor(ctx, x.role)
		switch {
		case err == nil:
			return true, nil
		case x.role == TorDirauth && restartNotYet(err):
			if polls%torReportEvery == 0 {
				r.d.Report.Linef("  waiting to restart the authority on %s: the machine refuses while another authority has started less than %s ago: %s", ip, tornet.AuthorityLearnWindow, refusalDetail(err))
			}
			polls++
			return false, nil
		}
		return false, err
	})
	if err != nil {
		r.emit(ip, StepTor, StateFailed, err.Error())
		return fmt.Errorf("machine %s: restart the Tor %s: %w", ip, x.role, err)
	}
	return nil
}

// restartNotYet says the machine refused the restart of its authority with the exit code
// of the roll refusal (clierr.CodeNotYet: `orama global restart dirauth` while another
// authority has started less than tornet.AuthorityLearnWindow ago, which changes nothing).
// The code is the command's own, carried by the *exec.ExitError the SSH session returns;
// no text is matched, so a log line that quotes the refusal is not mistaken for it.
func restartNotYet(err error) bool {
	var coded interface{ ExitCode() int }
	return errors.As(err, &coded) && coded.ExitCode() == clierr.CodeNotYet
}

// refusalDetail is what a machine's refusal says after its fixed opening, on one line.
func refusalDetail(err error) string {
	_, detail, found := strings.Cut(err.Error(), tornet.AuthorityRollRefusal)
	if !found {
		detail = err.Error()
	}
	detail = oneLine(strings.TrimSpace(detail))
	if len(detail) > refusalDetailMax {
		detail = detail[:refusalDetailMax] + "..."
	}
	return detail
}
