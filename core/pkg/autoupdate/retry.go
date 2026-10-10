package autoupdate

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

const (
	// retryBase is the wait after a release first fails to start installing:
	// one timer interval, so the next tick tries again.
	retryBase = 15 * time.Minute
	// retryMax caps the wait. A node that cannot start an install (no disk, a
	// check that refuses) is fixed by a person, and should try again within
	// the half day it takes them to look.
	retryMax = 12 * time.Hour
)

// Retry is a release this node could not start installing: a check refused
// before the services stopped, or staging failed with the node unchanged. The
// agent does not fetch it again before Until, and waits twice as long each time
// it fails again. The release is not marked bad: it did not run here.
type Retry struct {
	Version  string    `json:"version"`
	Attempts int       `json:"attempts"`
	Until    time.Time `json:"until"`
	Reason   string    `json:"reason"`
}

// Retries keeps the Retry on this machine: like the Journal it describes this
// node, not the cluster, and must outlive the run.
type Retries interface {
	// Current is the Retry a previous run left, nil when there is none.
	Current() (*Retry, error)
	// Set records the Retry over the one there.
	Set(Retry) error
	// Clear removes it; there being none is not an error.
	Clear() error
}

// retryDelay is the wait after the attempts-th failure in a row.
func retryDelay(attempts int) time.Duration {
	d := retryBase
	for i := 1; i < attempts && d < retryMax; i++ {
		d *= 2
	}
	return min(d, retryMax)
}

// deferRetry records that the install of in.Version could not start, and says
// so in the notice, so that the next ticks do not download it again.
func (a *Agent) deferRetry(in Intent, cause error) error {
	prev, err := a.Retries.Current()
	if err != nil {
		return err
	}
	attempts := 1
	if prev != nil && prev.Version == in.Version {
		attempts = prev.Attempts + 1
	}
	now := a.Now().UTC()
	r := Retry{Version: in.Version, Attempts: attempts, Until: now.Add(retryDelay(attempts)), Reason: cause.Error()}
	if err := a.Retries.Set(r); err != nil {
		return err
	}
	return updatenotice.Write(a.NoticePath, updatenotice.Notice{
		State: updatenotice.StateFailed, Mode: in.Mode, Channel: in.Channel, Current: a.Node.Current(), Candidate: in.Version,
		Reason:    fmt.Sprintf("release %s could not be installed here (attempt %d), next try after %s: %s", in.Version, attempts, r.Until.Format(time.RFC3339), r.Reason),
		CheckedAt: now,
	})
}

// backedOff is the outcome of a tick that falls inside the wait deferRetry
// set for version: nothing is fetched, locked or touched.
func (a *Agent) backedOff(version string) (Outcome, bool, error) {
	r, err := a.Retries.Current()
	if err != nil || r == nil || r.Version != version {
		return Outcome{}, false, err
	}
	// A wait is never longer than retryMax. One that ends later than that was
	// recorded by a clock that has since gone back (a clock set wrong at boot
	// and corrected), and would hold the release off for as long as the jump:
	// it counts as over.
	if now := a.Now(); !now.Before(r.Until) || r.Until.After(now.Add(retryMax)) {
		return Outcome{}, false, nil
	}
	return Outcome{
		Action:  OutcomeWait,
		Reason:  fmt.Sprintf("release %s could not be installed here (attempt %d); not tried again before %s", version, r.Attempts, r.Until.Format(time.RFC3339)),
		Version: version,
	}, true, nil
}
