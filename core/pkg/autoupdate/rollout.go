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
	members, err := a.Store.Members(ctx)
	if err != nil {
		return Outcome{}, err
	}
	self, found := memberAt(members, a.NodeHost)
	if !found {
		return Outcome{}, fmt.Errorf("this node (%s) is not in the cluster's registry", a.NodeHost)
	}
	release, err := a.Store.Lock(ctx, self.ID)
	if errors.Is(err, rqlite.ErrClusterLockHeld) {
		return Outcome{Action: OutcomeWait, Reason: "another node holds the rollout lock", Version: rel.Version}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	defer func() {
		freeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseLockBudget)
		defer cancel()
		if freeErr := release(freeCtx); freeErr != nil {
			a.Logf("free the rollout lock: %v", freeErr)
		}
	}()

	d, mine, err := a.assess(ctx, s, rel)
	if err != nil {
		return Outcome{}, err
	}
	if d.Action != ActionUpgrade || !mine {
		return a.act(ctx, s, rel, d, false)
	}
	return a.install(ctx, s, rel, self)
}

// install downloads rel, installs it and records the result.
func (a *Agent) install(ctx context.Context, s Settings, rel Release, self Member) (Outcome, error) {
	if err := a.Source.Download(ctx, s.RepoURL, rel); err != nil {
		if isVerification(err) {
			return a.refuse(s, rel.Version, "release "+rel.Version+" did not verify: "+err.Error())
		}
		return Outcome{}, err
	}
	a.Logf("installing release %s over %s", rel.Version, a.Node.Current())
	res, installErr := Install(ctx, a.Node, rel)
	switch {
	case res.Installed:
		if err := a.Store.Record(ctx, rel.Version, self.ID, StateInstalled, ""); err != nil {
			return Outcome{}, err
		}
		if err := updatenotice.Clear(a.NoticePath); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: OutcomeInstalled, Version: rel.Version}, nil
	case res.ReleaseBad:
		return a.markBad(ctx, s, rel, self, installErr)
	}
	return Outcome{}, installErr
}

// markBad records that rel failed on this node, which stops every other node
// from installing it, and reports it.
func (a *Agent) markBad(ctx context.Context, s Settings, rel Release, self Member, installErr error) (Outcome, error) {
	recordErr := a.Store.Record(ctx, rel.Version, self.ID, StateFailed, installErr.Error())
	noticeErr := updatenotice.Write(a.NoticePath, updatenotice.Notice{
		State: updatenotice.StateFailed, Mode: s.Mode, Channel: s.Channel,
		Current: a.Node.Current(), Candidate: rel.Version, Reason: installErr.Error(), CheckedAt: a.Now().UTC(),
	})
	return Outcome{Action: OutcomeFailed, Reason: installErr.Error(), Version: rel.Version},
		errors.Join(installErr, recordErr, noticeErr)
}
