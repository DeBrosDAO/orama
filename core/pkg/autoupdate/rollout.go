package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// releaseLockBudget bounds freeing the lock after an install, on a context of
// its own: the install's may have been cancelled.
const releaseLockBudget = 30 * time.Second

// installUnderLock installs rel on this node. It takes the cluster-wide lock
// first and judges again once it holds it, since the cluster can have changed
// between the first look and the lock: another node may have failed, or
// finished, or the cluster may have degraded.
func (a *Agent) installUnderLock(ctx context.Context, s Settings, rel Release) (Outcome, error) {
	self, release, err := a.lock(ctx)
	if errors.Is(err, rqlite.ErrClusterLockHeld) {
		return Outcome{Action: OutcomeWait, Reason: "another node holds the rollout lock", Version: rel.Version}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	defer a.unlock(ctx, release)

	d, mine, err := a.assess(ctx, s, rel)
	if err != nil {
		return Outcome{}, err
	}
	if d.Action != ActionUpgrade || !mine {
		return a.act(ctx, s, rel, d, false)
	}
	return a.install(ctx, s, rel, self)
}

// lock takes the rollout lock for this node and returns the node's own member.
func (a *Agent) lock(ctx context.Context) (Member, func(context.Context) error, error) {
	members, err := a.Store.Members(ctx)
	if err != nil {
		return Member{}, nil, err
	}
	self, found := memberAt(members, a.NodeHost)
	if !found {
		return Member{}, nil, fmt.Errorf("this node (%s) is not in the cluster's registry (it is matched by its overlay address)", a.NodeHost)
	}
	release, err := a.Store.Lock(ctx, self.ID)
	return self, release, err
}

// unlock frees the lock on a context of its own.
func (a *Agent) unlock(ctx context.Context, release func(context.Context) error) {
	freeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseLockBudget)
	defer cancel()
	if err := release(freeCtx); err != nil {
		a.Logf("free the rollout lock: %v", err)
	}
}

// install downloads rel, installs it and records the result.
func (a *Agent) install(ctx context.Context, s Settings, rel Release, self Member) (Outcome, error) {
	if err := a.Source.Download(ctx, s.RepoURL, rel); err != nil {
		if isVerification(err) {
			return a.refuse(s, rel.Version, "release "+rel.Version+" did not verify: "+err.Error())
		}
		return Outcome{}, err
	}
	prev := a.Node.Current()
	if prev == "" {
		return Outcome{}, errors.New("this node's installed release cannot be read, so there is nothing to roll back to")
	}
	a.Logf("installing release %s over %s", rel.Version, prev)
	in := Intent{Version: rel.Version, Previous: prev, StartedAt: a.Now().UTC()}
	if err := a.Journal.Begin(in); err != nil {
		return Outcome{}, fmt.Errorf("record that an install is beginning: %w", err)
	}
	res, installErr := Install(ctx, a.Node, a.Journal, in, rel)
	return a.settle(ctx, s, rel.Version, self, res, installErr)
}

// settle records how an install ended and, when the node is in a state no
// later run has to repair, takes the intent off the journal.
func (a *Agent) settle(ctx context.Context, s Settings, version string, self Member, res Result, installErr error) (Outcome, error) {
	var out Outcome
	var err error
	switch {
	case res.Installed:
		if res.StageErr != nil {
			a.Logf("release %s is installed and healthy; %v", version, res.StageErr)
		}
		out, err = a.installed(ctx, version, self)
	case res.ReleaseBad:
		out, err = a.markBad(ctx, s, version, self, installErr)
	default:
		out, err = Outcome{}, installErr
	}
	if res.Settled() {
		err = errors.Join(err, a.Journal.Clear())
	}
	return out, err
}

func (a *Agent) installed(ctx context.Context, version string, self Member) (Outcome, error) {
	if err := a.Store.Record(ctx, version, self.ID, StateInstalled, ""); err != nil {
		return Outcome{}, err
	}
	if err := updatenotice.Clear(a.NoticePath); err != nil {
		return Outcome{}, err
	}
	return Outcome{Action: OutcomeInstalled, Version: version}, nil
}

// markBad records that the release failed on this node, which stops every
// other node from installing it, and reports it.
func (a *Agent) markBad(ctx context.Context, s Settings, version string, self Member, installErr error) (Outcome, error) {
	recordErr := a.Store.Record(ctx, version, self.ID, StateFailed, installErr.Error())
	noticeErr := updatenotice.Write(a.NoticePath, updatenotice.Notice{
		State: updatenotice.StateFailed, Mode: s.Mode, Channel: s.Channel,
		Current: a.Node.Current(), Candidate: version, Reason: installErr.Error(), CheckedAt: a.Now().UTC(),
	})
	return Outcome{Action: OutcomeFailed, Reason: installErr.Error(), Version: version},
		errors.Join(installErr, recordErr, noticeErr)
}

// recordCurrent records that this node runs rel when auto-update is on and
// nothing was recorded. A node that is on a release already (pushed by hand,
// installed at that version) would otherwise stay the first node of the rollout
// plan without an install, and every other node would wait for it.
func (a *Agent) recordCurrent(ctx context.Context, s Settings, rel Release) error {
	if s.Mode != ModeAuto {
		return nil
	}
	if cmp, err := Compare(a.Node.Current(), rel.Version); err != nil || cmp != 0 {
		return err
	}
	members, err := a.Store.Members(ctx)
	if err != nil {
		return err
	}
	self, found := memberAt(members, a.NodeHost)
	if !found {
		return fmt.Errorf("this node (%s) is not in the cluster's registry (it is matched by its overlay address)", a.NodeHost)
	}
	installs, err := a.Store.Installs(ctx, rel.Version)
	if err != nil || installs[self.ID] == StateInstalled {
		return err
	}
	return a.Store.Record(ctx, rel.Version, self.ID, StateInstalled, "already running it")
}

// resume finishes an install a previous run began and did not end. A swap of
// /opt/orama the run left half-done is recovered first, so the installed release
// can be read. The release is in place if the node's installed release is the
// intent's; otherwise staging never completed, or was undone, and the intent is
// stale. An intent that says a rollback had begun is finished as a rollback.
// It finishes whatever the policy now says: a half-installed node is not left
// half-installed because the cluster turned updates off meanwhile.
func (a *Agent) resume(ctx context.Context, s Settings) (Outcome, bool, error) {
	intent, err := a.Journal.Pending()
	if err != nil || intent == nil {
		return Outcome{}, false, err
	}
	if err := a.Node.Recover(ctx); err != nil {
		return Outcome{}, true, fmt.Errorf("recover the release a previous run left half-swapped: %w", err)
	}
	current := a.Node.Current()
	if current == "" {
		return Outcome{}, true, errors.New("this node's installed release cannot be read, so the install a previous run began cannot be finished")
	}
	if current != intent.Version && !intent.RollingBack {
		return Outcome{}, false, a.Journal.Clear()
	}
	a.Logf("finishing the install of release %s that a previous run began", intent.Version)
	self, release, err := a.lock(ctx)
	if errors.Is(err, rqlite.ErrClusterLockHeld) {
		return Outcome{Action: OutcomeWait, Reason: "another node holds the rollout lock", Version: intent.Version}, true, nil
	}
	if err != nil {
		return Outcome{}, true, err
	}
	defer a.unlock(ctx, release)
	var res Result
	var finishErr error
	if intent.RollingBack {
		res, finishErr = RollBack(ctx, a.Node, *intent, nil)
	} else {
		res, finishErr = Finish(ctx, a.Node, a.Journal, *intent)
	}
	out, err := a.settle(ctx, s, intent.Version, self, res, finishErr)
	return out, true, err
}
