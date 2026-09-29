package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// pendingSlot is a slot assigned to this node that it has not answered yet.
// Root is the hex piece root, filled once the slot has been read.
type pendingSlot struct {
	DealID uint64 `json:"deal_id"`
	Slot   uint32 `json:"slot"`
	Root   string `json:"root,omitempty"`
}

type runnerState struct {
	Height  int64         `json:"height"`
	Pending []pendingSlot `json:"pending"`
	// Gone is the first epoch a bound slot was seen no longer held, by slotKey.
	Gone map[string]uint64 `json:"gone,omitempty"`
}

func loadState(path string, start int64) (runnerState, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return runnerState{Height: start - 1}, nil
	}
	if err != nil {
		return runnerState{}, fmt.Errorf("read provider state %s: %w", path, err)
	}
	var st runnerState
	if err := json.Unmarshal(body, &st); err != nil {
		return runnerState{}, fmt.Errorf("provider state %s is not valid: %w", path, err)
	}
	if st.Height < 0 {
		return runnerState{}, fmt.Errorf("provider state %s has a negative height", path)
	}
	return st, nil
}

func (r *Runner) save() error {
	r.mu.Lock()
	body, err := json.Marshal(r.state)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(r.statePath, body)
}

func writeFileAtomic(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func (st *runnerState) addPending(dealID uint64, slot uint32) bool {
	for _, p := range st.Pending {
		if p.DealID == dealID && p.Slot == slot {
			return false
		}
	}
	st.Pending = append(st.Pending, pendingSlot{DealID: dealID, Slot: slot})
	return true
}

func (r *Runner) pendingCopy() []pendingSlot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pendingSlot(nil), r.state.Pending...)
}

func (r *Runner) dropPending(dealID uint64, slot uint32) error {
	r.mu.Lock()
	kept := r.state.Pending[:0]
	for _, p := range r.state.Pending {
		if p.DealID != dealID || p.Slot != slot {
			kept = append(kept, p)
		}
	}
	r.state.Pending = kept
	r.mu.Unlock()
	return r.save()
}

func (r *Runner) notePendingRoot(dealID uint64, slot uint32, root string) error {
	r.mu.Lock()
	changed := false
	for i := range r.state.Pending {
		p := &r.state.Pending[i]
		if p.DealID == dealID && p.Slot == slot && p.Root != root {
			p.Root = root
			changed = true
		}
	}
	r.mu.Unlock()
	if !changed {
		return nil
	}
	return r.save()
}

// markGone records the first epoch a slot was seen no longer held and returns it.
func (r *Runner) markGone(dealID uint64, slot uint32, epoch uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Gone == nil {
		r.state.Gone = map[string]uint64{}
	}
	key := slotKey(dealID, slot)
	if first, ok := r.state.Gone[key]; ok {
		return first
	}
	r.state.Gone[key] = epoch
	return epoch
}

func (r *Runner) clearGone(dealID uint64, slot uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.state.Gone, slotKey(dealID, slot))
}

// monitorFile is the provider half of core's report MonitorFile. The field
// names must stay the ones core/pkg/telemetry/report parses.
type monitorFile struct {
	HotKeyBalanceNorama int64 `json:"hot_key_balance_norama"`
	ProofMisses         int   `json:"proof_misses"`
	DiskBytes           int64 `json:"disk_bytes"`
}

func (r *Runner) writeMonitor(ctx context.Context, misses int) error {
	if r.monitorPath == "" {
		return nil
	}
	bal, err := r.chain.Balance(ctx, r.signer)
	if err != nil {
		return err
	}
	if !bal.IsInt64() {
		return fmt.Errorf("hot key balance %s does not fit the monitor file", bal)
	}
	used, err := r.store.UsedBytes()
	if err != nil {
		return err
	}
	body, err := json.Marshal(monitorFile{HotKeyBalanceNorama: bal.Int64(), ProofMisses: misses, DiskBytes: used})
	if err != nil {
		return err
	}
	return writeFileAtomic(r.monitorPath, body)
}
