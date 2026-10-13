// Package bundle reads the parts of a canonical Ironwood (Zcash v6) Orchard bundle the chain
// needs for its own rules: nullifiers, note commitments, value balance and anchor. It does not
// check a proof or a signature; that is verify.Check. A bundle this package accepts has exactly
// the framing the Rust verifiers accept, so the two agree on what the bundle contains.
package bundle

import (
	"encoding/binary"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

const (
	// MaxActions is the protocol ceiling on the actions of one bundle, and so on the
	// max_actions_per_bundle parameter (types.Params.Validate refuses a larger value). The default
	// is 16; the ceiling leaves governance room to double it. Everything that bounds the size of a
	// bundle or of a transaction carrying one is derived from this: MaxBytes here, the onion
	// transaction gate in core/pkg/txgate.
	MaxActions = 32
	// MaxBytes bounds a bundle before any parsing or proving work: the exact size of a canonical
	// bundle of MaxActions actions, Len(MaxActions) (103,823 bytes), since no bundle the parameter
	// allows is larger. One of 16 actions, the default, is 53,325 bytes.
	MaxBytes = maxCountLen + MaxActions*ActionLen + HeaderLen + maxProofLenLen + ProofBase + ProofPerAction*MaxActions + (MaxActions+1)*SigLen
	// maxCountLen and maxProofLenLen are the CompactSize lengths of MaxActions (below 253: 1 byte)
	// and of the proof size of MaxActions actions (75,424, from 2^16: 5 bytes). TestMaxBytes checks
	// them against Len.
	maxCountLen    = 1
	maxProofLenLen = 5

	// ActionLen is one serialized action: cv, nf, rk, cmx, epk (32 each), enc (580), out (80).
	ActionLen = 5*32 + 580 + 80
	// HeaderLen is flags (1) + value balance (8) + anchor (32).
	HeaderLen = 1 + 8 + 32
	// ProofBase and ProofPerAction give the canonical proof size, 2720 + 2272 x actions
	// (orchard Proof::expected_proof_size).
	ProofBase      = 2720
	ProofPerAction = 2272
	// SigLen is one spend-authorization or binding signature.
	SigLen = 64
	// NodeLen is a nullifier, a note commitment or an anchor.
	NodeLen = 32

	// EncCompactLen is the prefix of an action's encrypted note that a wallet trial-decrypts
	// (ZIP-307 compact actions): the 52 bytes of version, diversifier, value and rseed that come
	// before the memo.
	EncCompactLen = 52
	// OutLen is an action's outCiphertext.
	OutLen = 80

	nullifierAt = 32
	cmxAt       = 96
	epkAt       = 128
	encAt       = 160
	outAt       = encAt + 580
)

// Bundle is a parsed bundle. The slices alias the input; copy before keeping them.
type Bundle struct {
	Actions      int
	Nullifiers   [][NodeLen]byte
	Commitments  [][NodeLen]byte
	Flags        byte
	ValueBalance int64
	Anchor       [NodeLen]byte
}

// ReadCompactSize reads a canonical Bitcoin-style CompactSize from the front of b and returns
// the value and the bytes it used.
func ReadCompactSize(b []byte) (value uint64, used int, ok bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	switch first := b[0]; {
	case first < 253:
		return uint64(first), 1, true
	case first == 253:
		if len(b) < 3 {
			return 0, 0, false
		}
		v := uint64(binary.LittleEndian.Uint16(b[1:]))
		return v, 3, v >= 253
	case first == 254:
		if len(b) < 5 {
			return 0, 0, false
		}
		v := uint64(binary.LittleEndian.Uint32(b[1:]))
		return v, 5, v >= 1<<16
	default:
		if len(b) < 9 {
			return 0, 0, false
		}
		v := binary.LittleEndian.Uint64(b[1:])
		return v, 9, v >= 1<<32
	}
}

// ProofLen is the canonical proof size for an action count.
func ProofLen(actions int) int { return ProofBase + ProofPerAction*actions }

// EffectingData returns the contiguous prefix of a bundle that the signatures cover: the action
// count, every action, the flags, the value balance and the anchor.
func EffectingData(b []byte) ([]byte, error) {
	n, used, ok := ReadCompactSize(b)
	if !ok || n == 0 || n > MaxBytes/ActionLen {
		return nil, fmt.Errorf("%w: action count", verify.ErrMalformed)
	}
	end := used + int(n)*ActionLen + HeaderLen
	if end > len(b) {
		return nil, fmt.Errorf("%w: truncated before the proof", verify.ErrMalformed)
	}
	return b[:end], nil
}

// Parse reads a whole bundle, refusing any framing the verifiers would refuse: a bad action
// count, a proof length that is not canonical for the count, a short body or trailing bytes.
func Parse(b []byte) (*Bundle, error) {
	if len(b) == 0 || len(b) > MaxBytes {
		return nil, fmt.Errorf("%w: %d bytes", verify.ErrMalformed, len(b))
	}
	prefix, err := EffectingData(b)
	if err != nil {
		return nil, err
	}
	n, used, _ := ReadCompactSize(b)
	actions := int(n)

	proofSizeAt := len(prefix)
	proofLen, sizeUsed, ok := ReadCompactSize(b[proofSizeAt:])
	if !ok {
		return nil, fmt.Errorf("%w: proof length", verify.ErrMalformed)
	}
	if proofLen != uint64(ProofLen(actions)) {
		return nil, verify.ErrProofLength
	}
	want := proofSizeAt + sizeUsed + ProofLen(actions) + actions*SigLen + SigLen
	if len(b) != want {
		return nil, fmt.Errorf("%w: body is %d bytes, want %d", verify.ErrMalformed, len(b), want)
	}

	out := &Bundle{
		Actions:     actions,
		Nullifiers:  make([][NodeLen]byte, actions),
		Commitments: make([][NodeLen]byte, actions),
	}
	for i := 0; i < actions; i++ {
		base := used + i*ActionLen
		copy(out.Nullifiers[i][:], b[base+nullifierAt:])
		copy(out.Commitments[i][:], b[base+cmxAt:])
	}
	header := used + actions*ActionLen
	out.Flags = b[header]
	out.ValueBalance = int64(binary.LittleEndian.Uint64(b[header+1:]))
	copy(out.Anchor[:], b[header+9:])
	return out, nil
}

// CompactAction is what a wallet needs of one action to find the notes sent to it: the nullifier
// (to see which of its notes were spent), the note commitment, the ephemeral key, the first
// EncCompactLen bytes of the encrypted note and the outCiphertext.
type CompactAction struct {
	Nullifier    [NodeLen]byte
	Cmx          [NodeLen]byte
	EphemeralKey [NodeLen]byte
	EncCompact   [EncCompactLen]byte
	OutCipher    [OutLen]byte
}

// CompactActions parses a whole bundle (Parse's framing rules apply) and returns each action's
// compact form, in bundle order.
func CompactActions(b []byte) ([]CompactAction, error) {
	parsed, err := Parse(b)
	if err != nil {
		return nil, err
	}
	_, used, _ := ReadCompactSize(b)
	out := make([]CompactAction, parsed.Actions)
	for i := range out {
		base := used + i*ActionLen
		copy(out[i].Nullifier[:], b[base+nullifierAt:])
		copy(out[i].Cmx[:], b[base+cmxAt:])
		copy(out[i].EphemeralKey[:], b[base+epkAt:])
		copy(out[i].EncCompact[:], b[base+encAt:])
		copy(out[i].OutCipher[:], b[base+outAt:])
	}
	return out, nil
}

// Len is the exact size of a canonical bundle of the given number of actions: the action count,
// the actions, the header, the proof length, the proof, one spend-authorization signature per
// action and the binding signature. Every canonical bundle of that many actions is this long.
func Len(actions int) int {
	return compactSizeLen(uint64(actions)) + actions*ActionLen + HeaderLen +
		compactSizeLen(uint64(ProofLen(actions))) + ProofLen(actions) + (actions+1)*SigLen
}

// compactSizeLen is the length of the canonical CompactSize encoding of v.
func compactSizeLen(v uint64) int {
	switch {
	case v < 253:
		return 1
	case v < 1<<16:
		return 3
	case v < 1<<32:
		return 5
	}
	return 9
}
