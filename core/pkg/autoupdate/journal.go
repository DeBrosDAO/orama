package autoupdate

import "time"

// Intent is an install that has begun: the release being installed and the one
// it replaces. It is written before the release is staged and removed once the
// node is settled (installed, rolled back, or never touched). One that is still
// there when a run starts is an install its predecessor did not finish: the run
// was killed, timed out or lost power between staging and the end of the
// upgrade, or in the middle of rolling it back.
type Intent struct {
	Version   string    `json:"version"`
	Previous  string    `json:"previous"`
	StartedAt time.Time `json:"started_at"`
	// RollingBack: the install failed and the previous release is being put
	// back. Blame says the release ran and failed (as opposed to a check that
	// refused before anything stopped), which marks it bad for the cluster.
	RollingBack bool `json:"rolling_back,omitempty"`
	Blame       bool `json:"blame,omitempty"`
	// RolledBack: the node is back on the previous release and healthy, and
	// the failure of the release is not yet in the cluster's registry. The
	// intent is kept until it is, so the next run records it instead of
	// installing the same release again.
	RolledBack bool `json:"rolled_back,omitempty"`
	// Mode and Channel are the policy the install began under, for the notice
	// a run that resumes it writes without reading the policy.
	Mode    string `json:"mode,omitempty"`
	Channel string `json:"channel,omitempty"`
	// Reason is why the install failed, once it has.
	Reason string `json:"reason,omitempty"`
}

// Journal keeps the Intent on this machine, not in the cluster: it describes
// what this node's /opt/orama holds.
type Journal interface {
	// Begin records the intent. An intent already there is an unfinished
	// install and Begin refuses.
	Begin(Intent) error
	// Replace records the intent over the one there, as the install moves on.
	Replace(Intent) error
	// Pending is the intent a previous run left, nil when there is none.
	Pending() (*Intent, error)
	// Clear removes the intent; there being none is not an error.
	Clear() error
}
