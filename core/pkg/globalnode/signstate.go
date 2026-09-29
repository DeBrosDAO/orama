// Package globalnode is the node-side operation of the global role: starting
// and stopping the orama-global-* units in order, and the validator key
// operations (encrypted export, migration between hosts, the double-sign
// guard). It reads CometBFT's key and state files as JSON and never imports
// the chain module.
package globalnode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
)

// SignState is the last height, round and step a validator key signed, from
// priv_validator_state.json. CometBFT refuses to sign at or below it, which
// is what keeps one key from signing two different votes for the same step.
type SignState struct {
	Height int64
	Round  int32
	Step   int8
}

// ParseSignState reads priv_validator_state.json. CometBFT writes the height
// as a decimal string and the round and step as numbers.
func ParseSignState(data []byte) (SignState, error) {
	var doc struct {
		Height *string `json:"height"`
		Round  *int32  `json:"round"`
		Step   *int8   `json:"step"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return SignState{}, fmt.Errorf("priv_validator_state.json is not JSON: %w", err)
	}
	if doc.Height == nil || doc.Round == nil || doc.Step == nil {
		return SignState{}, fmt.Errorf("priv_validator_state.json lacks height, round or step")
	}
	height, err := strconv.ParseInt(*doc.Height, 10, 64)
	if err != nil || height < 0 {
		return SignState{}, fmt.Errorf("priv_validator_state.json height %q is not a non-negative integer", *doc.Height)
	}
	if *doc.Round < 0 || *doc.Step < 0 {
		return SignState{}, fmt.Errorf("priv_validator_state.json round or step is negative")
	}
	return SignState{Height: height, Round: *doc.Round, Step: *doc.Step}, nil
}

// Behind reports whether s is strictly before o in (height, round, step).
func (s SignState) Behind(o SignState) bool {
	if s.Height != o.Height {
		return s.Height < o.Height
	}
	if s.Round != o.Round {
		return s.Round < o.Round
	}
	return s.Step < o.Step
}

func (s SignState) String() string {
	return fmt.Sprintf("height %d round %d step %d", s.Height, s.Round, s.Step)
}

// emptySignState is the state file of a key that has never signed, as
// `oramad init` writes it.
var emptySignState = []byte(`{"height":"0","round":0,"step":0}`)

// CheckNotBehind is the double-sign guard: the state a key starts with on
// this host must not be behind the last state it signed on the host it came
// from. A state behind it would let CometBFT sign a height the old host has
// already signed, with a different vote, which the chain punishes by
// slashing and tombstoning the validator.
func CheckNotBehind(target, source SignState) error {
	if target.Behind(source) {
		return fmt.Errorf("priv_validator_state.json is at %s, behind the %s the key last signed on its previous host; starting would risk a double sign", target, source)
	}
	return nil
}

// ed25519 sizes in priv_validator_key.json.
const (
	ed25519PublicSize  = 32
	ed25519PrivateSize = 64
)

// ValidatorKeyPubKey checks priv_validator_key.json is an ed25519 CometBFT key
// and returns its public key, base64, as the file writes it.
func ValidatorKeyPubKey(data []byte) (string, error) {
	var doc struct {
		PubKey  typedValue `json:"pub_key"`
		PrivKey typedValue `json:"priv_key"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("priv_validator_key.json is not JSON: %w", err)
	}
	if err := doc.PubKey.check("tendermint/PubKeyEd25519", ed25519PublicSize); err != nil {
		return "", fmt.Errorf("priv_validator_key.json pub_key: %w", err)
	}
	if err := doc.PrivKey.check("tendermint/PrivKeyEd25519", ed25519PrivateSize); err != nil {
		return "", fmt.Errorf("priv_validator_key.json priv_key: %w", err)
	}
	return doc.PubKey.Value, nil
}

type typedValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (v typedValue) check(wantType string, size int) error {
	if v.Type != wantType {
		return fmt.Errorf("type %q, want %q", v.Type, wantType)
	}
	raw, err := base64.StdEncoding.DecodeString(v.Value)
	if err != nil || len(raw) != size {
		return fmt.Errorf("value is not %d bytes of base64", size)
	}
	return nil
}
