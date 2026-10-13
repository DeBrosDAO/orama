package app

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"cosmossdk.io/log/v2"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchardproc"
)

// mempoolRequestTimeout is how long CheckTx waits for the mempool's verifier child. A valid bundle
// verifies in well under a second; CometBFT's local ABCI client serialises CheckTx with
// FinalizeBlock, so whatever a CheckTx waits is a delay of the next block, and a bundle that hangs
// the child must cost no more than this, not the block verifier's 30 s watchdog.
const mempoolRequestTimeout = 3 * time.Second

// warmBackoff is the least time between two attempts to start the mempool's child after one failed.
const warmBackoff = 10 * time.Second

// mempoolVerifier is the out-of-process verifier CheckTx, simulations and proposal checks use. It
// never starts the child on a request: starting builds the Halo 2 verifying key, seconds of work that
// would be done while CometBFT's ABCI lock is held. It starts the child in the background and answers
// verify.ErrVerifierStarting until the child is ready, and again after anything ends the child
// (orchardproc stops it after a fault or a panic, and it can exit while idle).
type mempoolVerifier struct {
	proc   *orchardproc.Verifier
	logger log.Logger
	now    func() time.Time
	// role names the verifier in the log: "mempool" (CheckTx, ReCheck, simulations) or "proposal".
	role string

	ready atomic.Bool
	// configured is false on a node with no verifier binary (a localnet or a scripted devnet): then
	// the request goes straight to proc, which answers "not linked" without starting anything.
	configured bool

	mu       sync.Mutex
	warming  bool
	closed   bool
	failed   bool      // a start has failed before: repeats are logged below Error
	nextWarm time.Time // no start before this, after a failure
	attempts int       // starts attempted, for tests
}

var _ verify.Verifier = (*mempoolVerifier)(nil)

func newMempoolVerifier(proc *orchardproc.Verifier, logger log.Logger) *mempoolVerifier {
	return &mempoolVerifier{proc: proc, logger: logger, now: time.Now, role: "mempool"}
}

// ID is the process verifier's identity, so verify.Check counts it as the second verifier.
func (m *mempoolVerifier) ID() string { return m.proc.ID() }

// Ready reports whether a request would be asked of a running child (keeper.readier). Asking is
// also what starts the child again after a failed start: a verifier that is not ready starts in the
// background, at most once per back-off, so the gate in front of Verify cannot keep it down.
func (m *mempoolVerifier) Ready() bool {
	if !m.configured || m.ready.Load() {
		return true
	}
	m.warm()
	return false
}

// setReady marks the child ready unless the verifier was closed meanwhile.
func (m *mempoolVerifier) setReady(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready.Store(v && !m.closed)
}

// Close stops the child. It never waits for a start under way: the start closes the child it
// finished after Close was called.
func (m *mempoolVerifier) Close() error {
	m.mu.Lock()
	m.closed = true
	starting := m.warming
	m.ready.Store(false)
	m.mu.Unlock()
	if starting {
		return nil
	}
	return m.proc.Close()
}

// warm starts the child in the background. A call while a start is under way, after Close, or inside
// the back-off after a failed start does nothing.
func (m *mempoolVerifier) warm() {
	m.mu.Lock()
	if m.closed || m.warming || m.now().Before(m.nextWarm) {
		m.mu.Unlock()
		return
	}
	m.warming = true
	m.attempts++
	m.mu.Unlock()
	go func() {
		err := m.proc.Warm()
		m.mu.Lock()
		m.warming = false
		closed := m.closed
		first := !m.failed
		if err != nil {
			m.failed = true
			m.nextWarm = m.now().Add(warmBackoff)
		} else {
			m.failed = false
			m.ready.Store(!closed)
		}
		m.mu.Unlock()
		switch {
		case closed:
			_ = m.proc.Close() // started after the app closed: do not leave the child behind
		case err == nil:
		case first:
			m.logger.Error("the shielded verifier process did not start; shielded bundles it would check are refused until it does", "role", m.role, "err", err)
		default:
			m.logger.Debug("the shielded verifier process did not start again", "role", m.role, "err", err)
		}
	}()
}

// Verify implements verify.Verifier. What a caller outside the node learns is only that the verifier
// is starting; the detail is in the node's log.
func (m *mempoolVerifier) Verify(bundle, binding []byte) error {
	return m.verifyWithin(bundle, binding, 0)
}

// VerifyWithin implements verify.DeadlineVerifier: the request, wait included, takes at most d.
func (m *mempoolVerifier) VerifyWithin(bundle, binding []byte, d time.Duration) error {
	return m.verifyWithin(bundle, binding, d)
}

// verifyWithin asks the running child; d is zero for the child's own timeouts.
func (m *mempoolVerifier) verifyWithin(bundle, binding []byte, d time.Duration) error {
	if !m.configured {
		return m.proc.Verify(bundle, binding)
	}
	if !m.ready.Load() {
		m.warm()
		return verify.ErrVerifierStarting
	}
	var err error
	if d > 0 {
		err = m.proc.VerifyRunningWithin(bundle, binding, d)
	} else {
		err = m.proc.VerifyRunning(bundle, binding)
	}
	switch {
	case errors.Is(err, orchardproc.ErrNotRunning):
		m.logger.Debug("the shielded verifier process is gone; starting another in the background", "role", m.role)
		m.setReady(false) // the child exited while idle: start the next one off the request path
		m.warm()
		return verify.ErrVerifierStarting
	case errors.Is(err, orchardproc.ErrBusy):
		// A request or a start holds the process, which says nothing about it being gone: it stays ready.
		m.logger.Debug("the shielded verifier process is busy", "role", m.role)
		return verify.ErrVerifierStarting
	case errors.Is(err, verify.ErrVerifierFault), errors.Is(err, verify.ErrVerifierPanic):
		m.setReady(false) // orchardproc ended the child
		m.warm()
	}
	return err
}
