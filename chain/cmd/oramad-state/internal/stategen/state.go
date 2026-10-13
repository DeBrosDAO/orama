package stategen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
)

// stateFileName is the generator's record in the home: the plan it runs, the genesis time its block
// times count from and the genesis validators, which only InitChain returns.
const stateFileName = "stategen.json"

const stateFileMode = 0o600

// validator is a genesis validator as the state file keeps it.
type validator struct {
	PubKey []byte `json:"pub_key"`
	Power  int64  `json:"power"`
}

// stateFile is what a resume needs besides the chain state itself.
type stateFile struct {
	Plan        Plan        `json:"plan"`
	Fingerprint string      `json:"fingerprint"`
	GenesisTime time.Time   `json:"genesis_time"`
	ChainID     string      `json:"chain_id"`
	Validators  []validator `json:"validators"`
}

func statePath(home string) string { return filepath.Join(home, stateFileName) }

// loadState reads the state file; ok is false when the home has none, which is a first run.
func loadState(home string) (st stateFile, ok bool, err error) {
	raw, err := os.ReadFile(statePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return st, false, nil
	}
	if err != nil {
		return st, false, fmt.Errorf("failed to read %s: %w", statePath(home), err)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, false, fmt.Errorf("failed to parse %s: %w", statePath(home), err)
	}
	return st, true, nil
}

// saveState writes the state file whole, through a rename, so a stop never leaves half of it.
func saveState(home string, st stateFile) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the generator state: %w", err)
	}
	tmp := statePath(home) + ".tmp"
	if err := os.WriteFile(tmp, raw, stateFileMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, statePath(home)); err != nil {
		return fmt.Errorf("failed to replace %s: %w", statePath(home), err)
	}
	return nil
}

// genesisValidators turns the recorded validators into CometBFT's genesis validators.
func genesisValidators(vals []validator) []cmttypes.GenesisValidator {
	out := make([]cmttypes.GenesisValidator, 0, len(vals))
	for _, v := range vals {
		pub := cmted25519.PubKey(v.PubKey)
		out = append(out, cmttypes.GenesisValidator{Address: pub.Address(), PubKey: pub, Power: v.Power, Name: ""})
	}
	return out
}
