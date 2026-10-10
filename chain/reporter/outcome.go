package reporter

import (
	"errors"
	"fmt"
)

// EpochError is a pass's failure to report one epoch.
type EpochError struct {
	Epoch uint64
	Err   error
}

func (e *EpochError) Error() string { return fmt.Sprintf("epoch %d: %v", e.Epoch, e.Err) }

func (e *EpochError) Unwrap() error { return e.Err }

// expectedStates are the conditions a healthy reporter meets: a report window
// that closed, an epoch settled without this reporter, an epoch too short to
// measure, an archive still filling in, and epochs lost to a gap between
// passes. None is a fault of the reporter, so a caller logs them once, not as
// failures on every pass.
var expectedStates = []error{ErrWindowClosed, ErrEpochSettled, ErrEpochTooShort, ErrIncompleteArchive, ErrEpochMissed}

// ExpectedState is the expected condition err stands for, or nil when err is a
// failure.
func ExpectedState(err error) error {
	for _, s := range expectedStates {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

// Leaves are the errors a pass's joined error is made of, each of which is one
// epoch's outcome or one pass-wide failure.
func Leaves(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			out = append(out, Leaves(e)...)
		}
		return out
	}
	return []error{err}
}
