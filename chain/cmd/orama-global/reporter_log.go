package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/DeBrosOfficial/network/chain/reporter"
)

// passLog logs the outcome of each reporter pass. A failure is an error on
// every pass it happens. A state a healthy reporter meets (see
// reporter.ExpectedState) is logged once, at the first pass that meets it, and
// not again while later passes meet the same state for the same epoch: with an
// epoch as short as the pass interval, an error line per pass would bury the
// failures that need someone.
type passLog struct {
	l *slog.Logger
	// noted are the states the previous pass logged, so the next pass does not
	// repeat them. Only those still met are carried over, so it stays small.
	noted map[string]struct{}
}

func newPassLog(l *slog.Logger) *passLog {
	return &passLog{l: l, noted: map[string]struct{}{}}
}

// pass logs the error one Step returned.
func (p *passLog) pass(err error) {
	met := map[string]struct{}{}
	for _, leaf := range reporter.Leaves(err) {
		state := reporter.ExpectedState(leaf)
		if state == nil {
			p.l.Error("reporter pass failed", "err", leaf)
			continue
		}
		key := stateKey(state, leaf)
		met[key] = struct{}{}
		if _, again := p.noted[key]; again {
			continue
		}
		if errors.Is(state, reporter.ErrIncompleteArchive) {
			p.l.Info("reporter is waiting for the vote archive of an epoch", "reason", leaf)
			continue
		}
		p.l.Warn("reporter is not reporting an epoch", "reason", leaf)
	}
	p.noted = met
}

// stateKey names a state per epoch, so the same state of the next epoch is
// logged again and the same state of this epoch is not.
func stateKey(state, leaf error) string {
	var ee *reporter.EpochError
	if errors.As(leaf, &ee) {
		return fmt.Sprintf("%d/%s", ee.Epoch, state)
	}
	return leaf.Error()
}
