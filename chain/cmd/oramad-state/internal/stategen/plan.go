// Package stategen writes a large application state into a node home without producing the blocks
// that would normally build it: N spent nullifiers, M decompressed cNFT assets and filler accounts
// that stand for the rest of an aged chain's IAVL keys. The writes go through the chain's own
// keepers and stores (x/shielded's nullifier database and accumulator, x/cnft's collections,
// x/auth's account keeper), so the layout on disk is the layout the chain writes. The state is
// committed in steps, one committed height each, so a run can stop and resume, and the result is
// deterministic in the plan's seed.
package stategen

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

const (
	// StepNullifiers, StepAssets and StepAccounts cap what one committed step writes of each kind. A
	// step is held in memory until its commit, so these bound the generator's footprint; the
	// number of steps is whatever the largest category needs at its cap.
	StepNullifiers = 100_000
	StepAssets     = 50_000
	StepAccounts   = 100_000

	// KeysPerAccount is how many IAVL keys one x/auth account writes: the account record and the
	// account-number index entry (TestKeysPerAccount_matchesTheStore counts them in the store).
	KeysPerAccount = 2

	// MaxTotal bounds every total of a plan, so the step arithmetic cannot overflow.
	MaxTotal = uint64(1) << 40
)

// Plan is what a run writes. The same plan always writes the same bytes.
type Plan struct {
	// Seed makes the derived identifiers (nullifiers, asset ids, addresses) differ between plans.
	Seed uint64 `json:"seed"`
	// Nullifiers is the number of spent nullifiers recorded in the nullifier database.
	Nullifiers uint64 `json:"nullifiers"`
	// Assets is the number of decompressed cNFT assets, the only per-asset state x/cnft keeps.
	Assets uint64 `json:"assets"`
	// Keys is the target number of filler IAVL keys, written as x/auth accounts (KeysPerAccount each).
	Keys uint64 `json:"keys"`
	// Caps overrides how much one step writes; zero fields keep the defaults. Tests use it to get
	// several steps out of a small plan.
	Caps Caps `json:"caps,omitempty"`
}

// Caps bound what one committed step writes of each kind.
type Caps struct {
	Nullifiers uint64 `json:"nullifiers,omitempty"`
	Assets     uint64 `json:"assets,omitempty"`
	Accounts   uint64 `json:"accounts,omitempty"`
}

func orDefault(v, def uint64) uint64 {
	if v == 0 {
		return def
	}
	return v
}

// Validate refuses a plan that writes nothing or that is too large to plan.
func (p Plan) Validate() error {
	if p.Nullifiers == 0 && p.Assets == 0 && p.Keys == 0 {
		return errors.New("the plan writes nothing: give at least one of nullifiers, assets and keys")
	}
	for name, total := range map[string]uint64{"nullifiers": p.Nullifiers, "assets": p.Assets, "keys": p.Keys} {
		if total > MaxTotal {
			return fmt.Errorf("%s is %d, more than the %d the step arithmetic allows", name, total, MaxTotal)
		}
	}
	return nil
}

// Accounts is the number of filler accounts that make up Keys.
func (p Plan) Accounts() uint64 { return p.Keys / KeysPerAccount }

// Steps is the number of committed steps the plan needs: at least one.
func (p Plan) Steps() uint64 {
	steps := max(
		ceilDiv(p.Nullifiers, orDefault(p.Caps.Nullifiers, StepNullifiers)),
		ceilDiv(p.Assets, orDefault(p.Caps.Assets, StepAssets)),
		ceilDiv(p.Accounts(), orDefault(p.Caps.Accounts, StepAccounts)))
	return max(steps, 1)
}

func ceilDiv(total, per uint64) uint64 { return (total + per - 1) / per }

// Share is the half-open range [from, to) of a category of size total that step writes. The ranges
// of all steps are contiguous and cover [0, total) exactly.
func (p Plan) Share(total, step uint64) (from, to uint64) {
	steps := p.Steps()
	return boundary(total, step, steps), boundary(total, step+1, steps)
}

func boundary(total, step, steps uint64) uint64 {
	hi, lo := bits.Mul64(total, step)
	q, _ := bits.Div64(hi, lo, steps)
	return q
}

// Fingerprint identifies the plan in the state file, so a resume with another plan is refused.
func (p Plan) Fingerprint() string {
	h := sha256.New()
	for _, v := range []uint64{p.Seed, p.Nullifiers, p.Assets, p.Keys, p.Caps.Nullifiers, p.Caps.Assets, p.Caps.Accounts} {
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, v))
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}
