// Package globalnode is the node-side operation of the global role: starting
// and stopping the orama-global-* units in order, and the validator key
// operations (encrypted export, migration between hosts, the double-sign
// guard). It reads CometBFT's key and state files as JSON and never imports
// the chain module.
package globalnode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
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

// RestoreFloor is the floor a restored backup starts from, given the
// network's latest committed height: the next height, round 0, step 0 (no
// step signed). CometBFT then signs nothing at or below the latest committed
// height, in any round. It may sign at the next height, so a vote the lost
// host cast there is only excluded if it stopped before that height began.
func RestoreFloor(latestCommitted int64) (SignState, error) {
	if latestCommitted <= 0 || latestCommitted == math.MaxInt64 {
		return SignState{}, fmt.Errorf("the floor height must be the network's latest committed height, above 0 and below %d", int64(math.MaxInt64))
	}
	return SignState{Height: latestCommitted + 1}, nil
}

// encodeSignState writes s as CometBFT's priv_validator_state.json, with no
// signature: CometBFT refuses to sign at or below it.
func encodeSignState(s SignState) ([]byte, error) {
	data, err := json.Marshal(map[string]any{"height": strconv.FormatInt(s.Height, 10), "round": s.Round, "step": s.Step})
	if err != nil {
		return nil, fmt.Errorf("encode the sign state: %w", err)
	}
	return data, nil
}

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
// and returns the public key CometBFT signs as, in canonical base64.
// CometBFT ignores the stored pub_key and derives the public key from
// priv_key (the last 32 of its 64 bytes), so that is what is returned, and a
// file whose pub_key names another key is refused. Returning the derived
// bytes re-encoded means a differently spelled base64 cannot make one key look
// like two.
func ValidatorKeyPubKey(data []byte) (string, error) {
	var doc struct {
		PubKey  typedValue `json:"pub_key"`
		PrivKey typedValue `json:"priv_key"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("priv_validator_key.json is not JSON: %w", err)
	}
	stated, err := doc.PubKey.decode("tendermint/PubKeyEd25519", ed25519PublicSize)
	if err != nil {
		return "", fmt.Errorf("priv_validator_key.json pub_key: %w", err)
	}
	priv, err := doc.PrivKey.decode("tendermint/PrivKeyEd25519", ed25519PrivateSize)
	if err != nil {
		return "", fmt.Errorf("priv_validator_key.json priv_key: %w", err)
	}
	derived := priv[ed25519PrivateSize-ed25519PublicSize:]
	if !bytes.Equal(stated, derived) {
		return "", fmt.Errorf("priv_validator_key.json pub_key is not the public key of its priv_key")
	}
	return base64.StdEncoding.EncodeToString(derived), nil
}

type typedValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (v typedValue) decode(wantType string, size int) ([]byte, error) {
	if v.Type != wantType {
		return nil, fmt.Errorf("type %q, want %q", v.Type, wantType)
	}
	raw, err := base64.StdEncoding.DecodeString(v.Value)
	if err != nil || len(raw) != size {
		return nil, fmt.Errorf("value is not %d bytes of base64", size)
	}
	return raw, nil
}
