package autoupdate

import "time"

// Intent is an install that has begun: the release being installed and the one
// it replaces. It is written before the release is staged and removed once the
// node is settled (installed, rolled back, or never touched). One that is still
// there when a run starts is an install its predecessor did not finish: the run
// was killed, timed out or lost power between staging and the end of the
// upgrade.
type Intent struct {
	Version   string    `json:"version"`
	Previous  string    `json:"previous"`
	StartedAt time.Time `json:"started_at"`
}

// Journal keeps the Intent on this machine, not in the cluster: it describes
// what this node's /opt/orama holds.
type Journal interface {
	// Begin records the intent, replacing none: an intent already there is an
	// unfinished install and Begin refuses.
	Begin(Intent) error
	// Pending is the intent a previous run left, nil when there is none.
	Pending() (*Intent, error)
	// Clear removes the intent; there being none is not an error.
	Clear() error
}
