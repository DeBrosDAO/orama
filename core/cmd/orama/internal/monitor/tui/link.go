package tui

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
)

// Link label limits.
const (
	// maxLinkErrChars bounds the error shown beside the connection state.
	maxLinkErrChars = 90
	// staleIntervals is how many refresh intervals may pass without a new
	// snapshot before the one on screen is marked stale.
	staleIntervals = 3
)

// linkStatus is the live view's connection to its source.
type linkStatus struct {
	state   monitor.LinkState
	err     error
	retryIn time.Duration
	attempt int
	// since is when the state was entered, for the reconnect countdown.
	since time.Time
}

// fromUpdate is the link status an update reports.
func fromUpdate(u monitor.Update, now time.Time) linkStatus {
	return linkStatus{state: u.State, err: u.Err, retryIn: u.RetryIn, attempt: u.Attempt, since: now}
}

// linkLabel says how the view is connected, in a word and a color: live data,
// a reconnect in progress (with its countdown and cause), or a source that has
// given up.
func linkLabel(t view.Theme, mode monitor.Mode, l linkStatus, now time.Time) string {
	switch l.state {
	case monitor.LinkLive:
		if mode == monitor.ModeSSH {
			return t.OK.Render("● live (ssh)")
		}
		return t.OK.Render("● live")
	case monitor.LinkReconnecting:
		left := max(l.retryIn-now.Sub(l.since), 0).Round(time.Second)
		return t.Warn.Render(fmt.Sprintf("↻ reconnecting in %s (attempt %d): %s", left, l.attempt, errText(l.err)))
	case monitor.LinkFailed:
		return t.Crit.Render("✗ stopped: " + errText(l.err))
	default:
		if l.attempt > 0 {
			return t.Muted.Render(fmt.Sprintf("◌ connecting (attempt %d)…", l.attempt+1))
		}
		return t.Muted.Render("◌ connecting…")
	}
}

// errText is an error as the view shows it: one line, bounded. The source
// already cleans its errors; cleaning again here keeps any other error (a
// refresh's, say) from reaching the terminal with control characters in it.
func errText(err error) string {
	if err == nil {
		return "no reason given"
	}
	return view.Truncate(monitor.CleanText(err.Error()), maxLinkErrChars)
}

// isStale reports whether the snapshot on screen may no longer be the
// cluster's state: the source has given up, or the snapshot is older than
// staleIntervals refreshes (plus, over SSH, the time a collection may take).
// A reconnect in progress is judged by the age alone, so the gateway ending a
// stream on schedule does not flag seconds-old data; a real outage makes the
// data old soon enough.
func isStale(age, interval time.Duration, mode monitor.Mode, state monitor.LinkState) bool {
	if state == monitor.LinkFailed {
		return true
	}
	limit := staleIntervals * interval
	if mode == monitor.ModeSSH {
		limit += monitor.DefaultSSHTimeout
	}
	return age > limit
}
