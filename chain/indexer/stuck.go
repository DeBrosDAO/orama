package indexer

import (
	"log/slog"
	"time"
)

// Stuck is why the follower cannot advance, shown by the status route while it lasts. The
// follower retries the same block each pass, so a block that fails for a reason that does not pass
// (the node pruned the state it needs, the chain disagrees with the indexer's copy of a rule)
// keeps the unit active with a cursor that does not move: this is how an operator sees it.
type Stuck struct {
	// Height is the block the follower cannot index.
	Height int64 `json:"height"`
	// Reason is the error of the last pass, served on operator/status only.
	Reason string `json:"reason,omitempty"`
	// StatePruned is true when the reason is that the node no longer keeps the state of a height
	// the block needs, which no retry changes.
	StatePruned bool `json:"state_pruned"`
	// Since is when the first of the failing passes began, and Failures how many have failed.
	Since    time.Time `json:"since"`
	Failures int       `json:"failures"`
}

// markStuck records that the pass failed at height and logs it. A pass that fails at the same
// height as the last keeps its Since.
func (s *Store) markStuck(height int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stuck{Height: height, Reason: err.Error(), StatePruned: isStatePruned(err), Since: time.Now().UTC(), Failures: 1}
	if s.stuck != nil && s.stuck.Height == height {
		st.Since, st.Failures = s.stuck.Since, s.stuck.Failures+1
	}
	s.stuck = &st
	slog.Error("indexer pass failed; the cursor does not advance until it works",
		"height", st.Height, "state_pruned", st.StatePruned, "failures", st.Failures, "since", st.Since, "err", err)
}

// clearStuck records that a pass worked.
func (s *Store) clearStuck() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stuck = nil
}

// currentStuck returns a copy of the follower's stuck record, or nil.
func (s *Store) currentStuck() *Stuck {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stuck == nil {
		return nil
	}
	c := *s.stuck
	return &c
}
