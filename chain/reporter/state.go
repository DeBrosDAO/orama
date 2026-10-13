package reporter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	stateFile   = "state.json"
	monitorFile = "monitor.json"
	fileMode    = 0o640
)

// State is <home>/state.json: the epoch the reporter last saw in progress and
// the last epoch it reported. The chain exposes only the current epoch's
// start, so the span of the epoch that just closed is the span between two
// observations of the boundary; the reporter keeps the earlier one here.
type State struct {
	// Seen is the epoch that was in progress at the last pass, 0 before the
	// first pass.
	Seen uint64 `json:"seen_epoch"`
	// SeenStartUnixNano is when that epoch started, in BFT time.
	SeenStartUnixNano int64 `json:"seen_epoch_start_unix_nano"`
	// Due are the closed epochs the chain has not yet taken a report for, oldest
	// first, each with its span.
	Due []Due `json:"due,omitempty"`
	// Reported is the last epoch whose report the chain took in full.
	Reported uint64 `json:"reported_epoch"`
}

// Due is a closed epoch still owed a report and the span it lasted.
type Due struct {
	Epoch        uint64 `json:"epoch"`
	FromUnixNano int64  `json:"from_unix_nano"`
	ToUnixNano   int64  `json:"to_unix_nano"`
}

// Monitor is <home>/monitor.json: what the last pass did, for the node report.
type Monitor struct {
	Epoch uint64 `json:"epoch"`
	// Chunks is the number of messages submitted for Epoch.
	Chunks int `json:"chunks"`
	// Relays is the number of relays reported for Epoch.
	Relays int `json:"relays"`
	// Unregistered counts relays in the votes that x/relay has no record of.
	Unregistered int `json:"unregistered"`
	// KeyMismatch counts relays whose vote ed25519 identity differs from the
	// registered one; they are left out of the report.
	KeyMismatch int `json:"key_mismatch"`
	// OwnOperator counts relays left out because they belong to this
	// reporter's own operator.
	OwnOperator int `json:"own_operator"`
	// NoEd25519 counts relays whose votes carry no ed25519 identity.
	NoEd25519 int `json:"no_ed25519"`
}

func loadState(home string) (State, error) {
	body, err := os.ReadFile(filepath.Join(home, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read reporter state: %w", err)
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return State{}, fmt.Errorf("reporter state %s is not valid JSON: %w", filepath.Join(home, stateFile), err)
	}
	return s, nil
}

func saveState(home string, s State) error {
	return writeJSON(filepath.Join(home, stateFile), s)
}

func saveMonitor(home string, m Monitor) error {
	return writeJSON(filepath.Join(home, monitorFile), m)
}

// writeJSON replaces path atomically and durably: the data is synced before the
// rename, so a crash or power loss leaves the old file or the new one and never
// an empty or half-written one.
func writeJSON(path string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
