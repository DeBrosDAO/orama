// Package autoupdate keeps a cluster on its release channel.
//
// Every node of a cluster runs an agent on a timer (Agent.Run). The agent
// reads the cluster's policy (updatepolicy), fetches the channel's metadata
// from the cluster's release repository and verifies it against the release
// root the cluster adopted (releaseverify), and asks Decide what to do with
// the newest release: nothing, tell `orama status` (notify, the default), or
// install it (auto).
//
// Install is one node at a time. A node installs only when it holds the
// cluster-wide lock and it is its turn in the rollout plan (followers first,
// the leader last, nameservers spaced: pkg/rollout), and never while the
// cluster is degraded or below a quorum of healthy voters. The install is
// `orama maint node stage-archive --release-only` keeping the release it replaces,
// then `orama node upgrade` of the new release, then the health gate
// (pkg/nodehealth). A failure puts the previous release back, upgrades onto it
// again, and records the release as failed for the cluster: no other node then
// installs it. A validator is never auto: on that mode it writes a notice that
// its upgrades are by hand and records the release as skipped, which the rollout
// counts as done.
package autoupdate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

const (
	// ModeOff does not look for a release.
	ModeOff = updatepolicy.ModeOff
	// ModeNotify reports a newer release and does not install it. This is the
	// default. A validator on auto is told to upgrade by hand (ActionSkip).
	ModeNotify = updatepolicy.ModeNotify
	// ModeAuto installs, one node at a time, inside the maintenance window.
	ModeAuto = updatepolicy.ModeAuto

	ActionNone    = "none"
	ActionNotify  = "notify"
	ActionUpgrade = "upgrade"
	ActionRefuse  = "refuse"
	// ActionSkip: a validator is on auto, which it never obeys. It says so and
	// leaves the upgrade to its operator.
	ActionSkip = "skip"

	// RoleCluster is a private-cluster node. It may run auto.
	RoleCluster = "cluster"
	// RoleValidator is a global chain validator. It is never auto: its
	// operator stages each chain upgrade explicitly.
	RoleValidator = "validator"
)

// Settings is the cluster's auto-update policy. MaxParallel is fixed at 1:
// a second node upgrading at the same time is how a rollout loses quorum.
type Settings struct {
	Mode        string
	Channel     string
	MaxParallel int
	// WindowStart and WindowEnd are hours in [0, 24), UTC. The window is
	// [start, end). A start equal to the end means the window is unset and
	// auto may run at any hour. A start after the end wraps midnight.
	WindowStart int
	WindowEnd   int
	// Role is this node's role: RoleCluster or RoleValidator.
	Role string
	// RepoURL is the cluster's release repository; empty means none.
	RepoURL string
}

// DefaultSettings is what a cluster does before an operator chooses.
func DefaultSettings() Settings {
	return Settings{Mode: updatepolicy.DefaultMode, Channel: updatepolicy.DefaultChannel, MaxParallel: 1, Role: RoleCluster}
}

// Health is what Decide needs to know about the cluster. Voters is the raft
// voter count. HealthyVoters is how many of those are up.
type Health struct {
	Degraded      bool
	Voters        int
	HealthyVoters int
}

// Candidate is a release the node is considering.
type Candidate struct {
	Version string
	Channel string
	// Bad is a release a previous health-gate failure marked bad.
	Bad bool
}

// Decision is the one thing the node should do.
type Decision struct {
	Action string
	Reason string
}

// Decide reports what to do with candidate relative to current.
//
// verifyErr is the TUF client's error, or nil when the metadata and the
// archive verified. Any verification failure refuses, including a rollback,
// a freeze, a below-threshold signature set, and a hash mismatch.
func Decide(settings Settings, health Health, now time.Time, current string, candidate Candidate, verifyErr error) (Decision, error) {
	if err := settings.validate(); err != nil {
		return Decision{}, err
	}
	if verifyErr != nil {
		return Decision{Action: ActionRefuse, Reason: verifyReason(verifyErr)}, nil
	}
	if candidate.Bad {
		return Decision{Action: ActionRefuse, Reason: "release " + candidate.Version + " is marked bad"}, nil
	}
	if candidate.Channel != "" && candidate.Channel != settings.Channel {
		return Decision{Action: ActionRefuse, Reason: "release is on channel " + candidate.Channel + ", cluster follows " + settings.Channel}, nil
	}
	cmp, err := Compare(candidate.Version, current)
	if err != nil {
		return Decision{}, err
	}
	if cmp < 0 {
		return Decision{Action: ActionRefuse, Reason: "release " + candidate.Version + " is older than " + current}, nil
	}
	if cmp == 0 || settings.Mode == ModeOff {
		return Decision{Action: ActionNone, Reason: "nothing to install"}, nil
	}
	if settings.Role == RoleValidator && settings.Mode == ModeAuto {
		return Decision{Action: ActionSkip, Reason: "release " + candidate.Version + " is not installed here: this machine is a validator, " +
			"upgrade it by hand ('orama maint global stage-oramad')"}, nil
	}
	if health.Degraded || !quorum(health) {
		return Decision{Action: ActionRefuse, Reason: "cluster is degraded or below quorum"}, nil
	}
	if settings.Mode == ModeNotify {
		return Decision{Action: ActionNotify, Reason: "newer release " + candidate.Version + " (notify)"}, nil
	}
	if !inWindow(settings, now) {
		return Decision{Action: ActionNotify, Reason: "newer release " + candidate.Version + " is outside the maintenance window"}, nil
	}
	return Decision{Action: ActionUpgrade, Reason: "newer release " + candidate.Version}, nil
}

func (s Settings) validate() error {
	switch s.Mode {
	case ModeOff, ModeNotify, ModeAuto:
	default:
		return fmt.Errorf("auto-update mode %q is not off, notify, or auto", s.Mode)
	}
	switch s.Role {
	case RoleCluster, RoleValidator:
	default:
		return fmt.Errorf("node role %q is not %s or %s", s.Role, RoleCluster, RoleValidator)
	}
	if s.MaxParallel != 1 {
		return fmt.Errorf("max_parallel is %d; only 1 is allowed", s.MaxParallel)
	}
	if s.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	if s.WindowStart < 0 || s.WindowStart > 23 || s.WindowEnd < 0 || s.WindowEnd > 23 {
		return fmt.Errorf("maintenance window hours must be 0 to 23")
	}
	return nil
}

func quorum(h Health) bool {
	if h.Voters < 1 || h.HealthyVoters < 0 || h.HealthyVoters > h.Voters {
		return false
	}
	return h.HealthyVoters*2 > h.Voters
}

func inWindow(s Settings, now time.Time) bool {
	if s.WindowStart == s.WindowEnd {
		return true
	}
	hour := now.UTC().Hour()
	if s.WindowStart < s.WindowEnd {
		return hour >= s.WindowStart && hour < s.WindowEnd
	}
	return hour >= s.WindowStart || hour < s.WindowEnd
}

func verifyReason(err error) string {
	switch {
	case errors.Is(err, releaseverify.ErrRollback):
		return "rolled-back release metadata"
	case errors.Is(err, releaseverify.ErrFreeze):
		return "frozen release metadata"
	case errors.Is(err, releaseverify.ErrThreshold):
		return "release signature set is below threshold"
	case errors.Is(err, releaseverify.ErrTargetHash):
		return "release archive does not match its targets metadata"
	default:
		return "release did not verify"
	}
}

// Compare reports the order of two dotted numeric versions.
// A positive result means a is newer than b. A leading v is ignored.
// A non-numeric segment is an error, so a version this code cannot order
// is not treated as newer.
func Compare(a, b string) (int, error) {
	as, err := segments(a)
	if err != nil {
		return 0, err
	}
	bs, err := segments(b)
	if err != nil {
		return 0, err
	}
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av > bv {
				return 1, nil
			}
			return -1, nil
		}
	}
	return 0, nil
}

func segments(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, fmt.Errorf("version is empty")
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || (len(part) > 1 && part[0] == '0') {
			return nil, fmt.Errorf("version %q has a non-numeric segment %q", v, part)
		}
		out[i] = n
	}
	return out, nil
}
