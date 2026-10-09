package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// savedReport is <home>/report-<epoch>.json: the entries chosen for an epoch,
// written before the first chunk is sent and removed once the chain has all of
// them. A retry sends these entries again instead of choosing afresh, because
// the registry can change between passes and a chunk already on chain is only
// completed by messages with the same inputs_root.
type savedReport struct {
	Entries []relaytypes.RelayObservation `json:"entries"`
	Monitor Monitor                       `json:"monitor"`
}

func reportPath(home string, epoch uint64) string {
	return filepath.Join(home, fmt.Sprintf("report-%d.json", epoch))
}

// reportDue tries every closed epoch still owed. An epoch the chain took is
// removed from st.Due, and so is one that can never be reported (settled, past
// its report window, or shorter than a voting interval); any other failure
// leaves it owed. Each epoch's failure is an *EpochError. cur is the epoch in
// progress. st is saved when it changed.
func (r *Runner) reportDue(ctx context.Context, st *State, cur Epoch) ([]uint64, error) {
	var done []uint64
	var errs []error
	var owed []Due
	for _, d := range st.Due {
		err := r.reportEpoch(ctx, d, cur)
		switch {
		case err == nil:
			done = append(done, d.Epoch)
			st.Reported = max(st.Reported, d.Epoch)
			continue
		case errors.Is(err, ErrEpochSettled), errors.Is(err, ErrWindowClosed), errors.Is(err, ErrEpochTooShort):
		default:
			owed = append(owed, d)
		}
		errs = append(errs, &EpochError{Epoch: d.Epoch, Err: err})
	}
	if len(owed) == len(st.Due) {
		return done, errors.Join(errs...)
	}
	st.Due = owed
	if err := saveState(r.cfg.Home, *st); err != nil {
		errs = append(errs, err)
	}
	return done, errors.Join(errs...)
}

// closedEpoch is ErrEpochSettled or ErrWindowClosed when the chain takes no
// more reports for the epoch given the epoch in progress, nil when it does. The
// report saved for such an epoch is removed: nothing will send it. A report
// that cannot be removed is an ordinary failure, which leaves the epoch owed so
// the removal is tried again.
func (r *Runner) closedEpoch(ctx context.Context, epoch uint64, cur Epoch) error {
	settled, err := r.chain.EpochSettled(ctx, epoch)
	if err != nil {
		return fmt.Errorf("check whether the epoch is settled: %w", err)
	}
	var closed error
	switch {
	case settled:
		closed = ErrEpochSettled
	case cur.Number > epoch && cur.Number-epoch > relaytypes.ReportWindowEpochs:
		closed = fmt.Errorf("%w: it was open to reports until epoch %d began and the chain is at epoch %d", ErrWindowClosed, epoch+relaytypes.ReportWindowEpochs+1, cur.Number)
	default:
		return nil
	}
	if rerr := os.Remove(reportPath(r.cfg.Home, epoch)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return fmt.Errorf("remove the report of the closed epoch: %w", rerr)
	}
	return closed
}

func (r *Runner) reportEpoch(ctx context.Context, d Due, cur Epoch) error {
	if err := r.closedEpoch(ctx, d.Epoch, cur); err != nil {
		return err
	}
	rep, err := r.chosen(ctx, d)
	if err != nil {
		return err
	}
	msgs, err := Messages(r.cfg.Reporter, d.Epoch, rep.Entries, r.cfg.ChunkEntries)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if err := r.chain.Submit(ctx, m); err != nil {
			return errors.Join(r.refusedSubmit(ctx, d.Epoch), fmt.Errorf("submit chunk %d of %d: %w", m.ChunkIndex+1, m.ChunkCount, err))
		}
	}
	rep.Monitor.Epoch, rep.Monitor.Chunks, rep.Monitor.Relays = d.Epoch, len(msgs), len(rep.Entries)
	if err := saveMonitor(r.cfg.Home, rep.Monitor); err != nil {
		return err
	}
	if err := os.Remove(reportPath(r.cfg.Home, d.Epoch)); err != nil {
		return fmt.Errorf("remove the sent report: %w", err)
	}
	return nil
}

// refusedSubmit says why the chain may have refused a chunk: the epoch rolled
// over or was settled between the check before the first chunk and this one.
// It reads the chain again rather than the refusal's text. The result is
// ErrEpochSettled or ErrWindowClosed when the chain takes no more reports for
// the epoch, an error when the chain could not be read, and nil when it still
// takes them, so the refusal is an ordinary failure and the epoch stays owed.
func (r *Runner) refusedSubmit(ctx context.Context, epoch uint64) error {
	cur, err := r.chain.CurrentEpoch(ctx)
	if err != nil {
		return fmt.Errorf("read the current epoch after the refusal: %w", err)
	}
	return r.closedEpoch(ctx, epoch, cur)
}

// chosen is the report for the epoch: the one saved by an earlier attempt, or
// one computed from the votes and the registry now and saved.
func (r *Runner) chosen(ctx context.Context, d Due) (savedReport, error) {
	path := reportPath(r.cfg.Home, d.Epoch)
	body, err := os.ReadFile(path)
	if err == nil {
		var rep savedReport
		if err := json.Unmarshal(body, &rep); err != nil {
			return savedReport{}, fmt.Errorf("the saved report %s is not valid JSON; remove it to compute the epoch again: %w", path, err)
		}
		return rep, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return savedReport{}, fmt.Errorf("read %s: %w", path, err)
	}
	w := Window{From: time.Unix(0, d.FromUnixNano).UTC(), To: time.Unix(0, d.ToUnixNano).UTC()}
	if _, err := expectedVotes(w, r.cfg.VoteInterval); err != nil {
		return savedReport{}, err
	}
	votes, err := LoadVotes(r.cfg.VotesDir, r.cfg.Authority, w)
	if err != nil {
		return savedReport{}, err
	}
	obs, err := Observe(votes, w, r.cfg.VoteInterval)
	if err != nil {
		return savedReport{}, err
	}
	entries, mon, err := r.entries(ctx, obs)
	if err != nil {
		return savedReport{}, err
	}
	rep := savedReport{Entries: entries, Monitor: mon}
	if err := writeJSON(path, rep); err != nil {
		return savedReport{}, err
	}
	return rep, nil
}

// entries keeps the observations x/relay can take from this reporter: relays
// that are registered, whose registered ed25519 identity is the one the votes
// carry, and that this reporter's operator does not run. x/relay refuses a whole
// chunk holding an unregistered relay or a wrong identity, so these are left
// out here and counted in the monitor file.
func (r *Runner) entries(ctx context.Context, obs []Observation) ([]relaytypes.RelayObservation, Monitor, error) {
	var mon Monitor
	out := make([]relaytypes.RelayObservation, 0, len(obs))
	for _, o := range obs {
		if len(o.Ed25519) == 0 {
			mon.NoEd25519++
			continue
		}
		reg, found, err := r.chain.Relay(ctx, o.Fingerprint[:])
		if err != nil {
			return nil, mon, fmt.Errorf("look up relay %x: %w", o.Fingerprint, err)
		}
		switch {
		case !found:
			mon.Unregistered++
		case reg.Operator == r.cfg.Operator:
			mon.OwnOperator++
		case !bytes.Equal(reg.Ed25519Id, o.Ed25519):
			mon.KeyMismatch++
		default:
			out = append(out, o.Entry())
		}
	}
	return out, mon, nil
}
