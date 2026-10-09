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
// removed from st.Due, and so is one the chain settled without it; any other
// failure leaves it owed. st is saved when it changed.
func (r *Runner) reportDue(ctx context.Context, st *State) ([]uint64, error) {
	var done []uint64
	var errs []error
	var owed []Due
	for _, d := range st.Due {
		err := r.reportEpoch(ctx, d)
		switch {
		case err == nil:
			done = append(done, d.Epoch)
			st.Reported = max(st.Reported, d.Epoch)
		case errors.Is(err, ErrEpochSettled):
			errs = append(errs, err)
		default:
			owed = append(owed, d)
			errs = append(errs, err)
		}
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

func (r *Runner) reportEpoch(ctx context.Context, d Due) error {
	settled, err := r.chain.EpochSettled(ctx, d.Epoch)
	if err != nil {
		return fmt.Errorf("check whether epoch %d is settled: %w", d.Epoch, err)
	}
	if settled {
		err := fmt.Errorf("%w: epoch %d", ErrEpochSettled, d.Epoch)
		if rerr := os.Remove(reportPath(r.cfg.Home, d.Epoch)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove the report of the settled epoch: %w", rerr))
		}
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
			return fmt.Errorf("submit chunk %d of %d for epoch %d: %w", m.ChunkIndex+1, m.ChunkCount, d.Epoch, err)
		}
	}
	rep.Monitor.Epoch, rep.Monitor.Chunks, rep.Monitor.Relays = d.Epoch, len(msgs), len(rep.Entries)
	if err := saveMonitor(r.cfg.Home, rep.Monitor); err != nil {
		return err
	}
	if err := os.Remove(reportPath(r.cfg.Home, d.Epoch)); err != nil {
		return fmt.Errorf("remove the sent report of epoch %d: %w", d.Epoch, err)
	}
	return nil
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
	votes, err := LoadVotes(r.cfg.VotesDir, r.cfg.Authority, w)
	if err != nil {
		return savedReport{}, err
	}
	obs, err := Observe(votes, w, r.cfg.VoteInterval)
	if err != nil {
		return savedReport{}, fmt.Errorf("epoch %d: %w", d.Epoch, err)
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
