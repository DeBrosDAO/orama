package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// Outcomes of one run.
const (
	// OutcomeInstalled: this node installed the release.
	OutcomeInstalled = "installed"
	// OutcomeWait: the release is for this node to install, and it is not yet
	// its turn, or another node holds the lock.
	OutcomeWait = "wait"
	// OutcomeFailed: this node tried, rolled back, and marked the release bad.
	OutcomeFailed = "failed"
)

// Agent is one node's auto-update, run once per timer tick.
type Agent struct {
	Store  Store
	Raft   Raft
	Source Source
	Node   Node
	// Journal keeps an install that has begun, so a run that dies in the middle
	// of one is finished by the next.
	Journal Journal
	// Retries keeps a release this node could not start installing, so that
	// the next ticks leave it alone for a while.
	Retries Retries
	// Role is RoleCluster, or RoleValidator on a machine that runs the chain.
	Role string
	// NodeHost is this node's overlay address, the key of its registry row.
	NodeHost string
	// NoticePath is where the finding is kept for the node report.
	NoticePath string
	Now        func() time.Time
	Logf       func(format string, args ...any)
}

// Outcome is what a run did: an Action* of Decide, or an Outcome*.
type Outcome struct {
	Action  string
	Reason  string
	Version string
}

// Run looks for a newer release of the cluster's channel and acts on it
// according to the cluster's policy.
//
// The caller holds the machine's run lock: no other run is using the work
// directory, so what an earlier run left in it is swept first. An install an
// earlier run began is finished before the policy is read, so a policy that
// does not parse cannot stop a half-installed node from being repaired.
func (a *Agent) Run(ctx context.Context) (Outcome, error) {
	if err := a.Source.SweepStale(); err != nil {
		return Outcome{}, err
	}
	if out, done, err := a.resume(ctx); done {
		return out, err
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if out, done, err := a.nothingToDo(settings); done {
		return out, err
	}
	rel, found, err := a.Source.Newest(ctx, settings.RepoURL, settings.Channel)
	if err != nil {
		return a.refuseUnverified(settings, err)
	}
	if !found {
		return a.none("the " + settings.Channel + " channel lists no release for this machine")
	}
	defer rel.Remove()
	decision, mine, err := a.assess(ctx, settings, rel)
	if err != nil {
		return Outcome{}, err
	}
	return a.act(ctx, settings, rel, decision, mine)
}

func (a *Agent) settings(ctx context.Context) (Settings, error) {
	stored, err := a.Store.Stored(ctx)
	if err != nil {
		return Settings{}, err
	}
	return SettingsFrom(stored, a.Role)
}

// nothingToDo is the cases with no release to look for: updates are off, the
// cluster named no repository, or it adopted no release root.
func (a *Agent) nothingToDo(s Settings) (Outcome, bool, error) {
	var reason string
	switch {
	case s.Mode == ModeOff:
		reason = "auto-update is off"
	case s.RepoURL == "":
		reason = "no release repository is configured (cluster setting release-repo)"
	default:
		if _, err := releaseverify.ReadRoot(a.Source.RootPath); err != nil {
			if !errors.Is(err, releaseverify.ErrNoRoot) {
				return Outcome{}, true, err
			}
			reason = "this node has adopted no release root, so no release can be verified"
		}
	}
	if reason == "" {
		return Outcome{}, false, nil
	}
	out, err := a.none(reason)
	return out, true, err
}

func (a *Agent) none(reason string) (Outcome, error) {
	if err := updatenotice.Clear(a.NoticePath); err != nil {
		return Outcome{}, err
	}
	return Outcome{Action: ActionNone, Reason: reason}, nil
}

// refuseUnverified turns a failure to verify the repository's metadata into a
// refusal, which is reported, and any other failure (the network) into an error.
func (a *Agent) refuseUnverified(s Settings, cause error) (Outcome, error) {
	if !isVerification(cause) {
		return Outcome{}, cause
	}
	d, err := Decide(s, Health{}, a.Now(), a.Node.Current(), Candidate{}, cause)
	if err != nil {
		return Outcome{}, err
	}
	return a.refuse(s, "", d.Reason+": "+cause.Error())
}

// isVerification reports an error that says the repository's metadata or
// archive did not verify, as opposed to one that says it could not be reached.
func isVerification(err error) bool {
	for _, target := range []error{
		releaseverify.ErrRollback, releaseverify.ErrFreeze, releaseverify.ErrThreshold,
		releaseverify.ErrTargetHash,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// assess decides what to do with rel and whether it is this node's turn.
func (a *Agent) assess(ctx context.Context, s Settings, rel Release) (Decision, bool, error) {
	installs, err := a.Store.Installs(ctx, rel.Version)
	if err != nil {
		return Decision{}, false, err
	}
	members, err := a.Store.Members(ctx)
	if err != nil {
		return Decision{}, false, err
	}
	raft, err := a.Raft.View(ctx)
	if err != nil {
		return Decision{}, false, err
	}
	bad := false
	for _, state := range installs {
		bad = bad || state == StateFailed
	}
	d, err := Decide(s, ClusterHealth(members, raft), a.Now(), a.Node.Current(),
		Candidate{Version: rel.Version, Channel: s.Channel, Bad: bad}, nil)
	if err != nil || d.Action != ActionUpgrade {
		return d, false, err
	}
	next, any, err := NextNode(members, raft, installs)
	if err != nil {
		return Decision{}, false, err
	}
	self, found := memberAt(members, a.NodeHost)
	if !found {
		return Decision{}, false, fmt.Errorf("this node (%s) is not in the cluster's registry", a.NodeHost)
	}
	return d, any && next.ID == self.ID, nil
}

func memberAt(members []Member, host string) (Member, bool) {
	for _, m := range members {
		if m.InternalIP == host {
			return m, true
		}
	}
	return Member{}, false
}

func (a *Agent) act(ctx context.Context, s Settings, rel Release, d Decision, mine bool) (Outcome, error) {
	switch d.Action {
	case ActionNone:
		if err := a.recordCurrent(ctx, s, rel); err != nil {
			return Outcome{}, err
		}
		return a.none(d.Reason)
	case ActionRefuse:
		return a.refuse(s, rel.Version, d.Reason)
	case ActionNotify:
		return a.notify(s, rel.Version, d.Reason)
	case ActionSkip:
		return a.skip(ctx, s, rel.Version, d.Reason)
	}
	if !mine {
		if _, err := a.notify(s, rel.Version, "it is another node's turn to install"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: OutcomeWait, Reason: "it is another node's turn to install", Version: rel.Version}, nil
	}
	if out, backedOff, err := a.backedOff(rel.Version); backedOff || err != nil {
		return out, err
	}
	return a.installUnderLock(ctx, s, rel)
}

// skip is a validator on auto: it records the release as skipped, so that the
// rollout does not wait for the node, and refuses it in the notice.
func (a *Agent) skip(ctx context.Context, s Settings, version, reason string) (Outcome, error) {
	members, err := a.Store.Members(ctx)
	if err != nil {
		return Outcome{}, err
	}
	self, found := memberAt(members, a.NodeHost)
	if !found {
		return Outcome{}, fmt.Errorf("this node (%s) is not in the cluster's registry (it is matched by its overlay address)", a.NodeHost)
	}
	installs, err := a.Store.Installs(ctx, version)
	if err != nil {
		return Outcome{}, err
	}
	if installs[self.ID] != StateSkipped {
		if err := a.Store.Record(ctx, version, self.ID, StateSkipped, reason); err != nil {
			return Outcome{}, err
		}
	}
	out, err := a.refuse(s, version, reason)
	out.Action = ActionSkip
	return out, err
}

func (a *Agent) notify(s Settings, version, reason string) (Outcome, error) {
	err := updatenotice.Write(a.NoticePath, updatenotice.Notice{
		State: updatenotice.StateAvailable, Mode: s.Mode, Channel: s.Channel,
		Current: a.Node.Current(), Candidate: version, Reason: reason, CheckedAt: a.Now().UTC(),
	})
	return Outcome{Action: ActionNotify, Reason: reason, Version: version}, err
}

func (a *Agent) refuse(s Settings, version, reason string) (Outcome, error) {
	err := updatenotice.Write(a.NoticePath, updatenotice.Notice{
		State: updatenotice.StateRefused, Mode: s.Mode, Channel: s.Channel,
		Current: a.Node.Current(), Candidate: version, Reason: reason, CheckedAt: a.Now().UTC(),
	})
	return Outcome{Action: ActionRefuse, Reason: reason, Version: version}, err
}
