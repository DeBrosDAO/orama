package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Ed25519PubKeyLen is the length, in bytes, of the raw ed25519 consensus public key
// BootstrapMember.ConsensusPubkey carries.
const Ed25519PubKeyLen = 32

// DefaultGenesisState returns x/power's default genesis: default Params and an empty bootstrap
// committee (a real genesis must always set BootstrapCommittee explicitly; see
// keeper.Keeper.InitGenesis, which rejects an empty or undersized committee).
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:             DefaultParams(),
		BootstrapCommittee: []BootstrapMember{},
		Lambda:             math.LegacyZeroDec(),
		CurrentCapBps:      CapBpsNormal,
		RampRecords:        []ValidatorRampRecord{},
		PowerRecords:       []ValidatorPowerRecord{},
		CommitteeSelfBonds: []CommitteeSelfBondRecord{},
		Exported:           false,
		GateSatisfied:      false,
	}
}

// Validate performs genesis-state sanity checks that do not require chain-id or bank-state
// context (that additional check - the bootstrap committee's size against the production floor -
// is in keeper.Keeper.InitGenesis, mirroring x/emission's checkBootstrapChainID pattern, since
// Params/GenesisState alone have no access to ctx.ChainID()).
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	if uint64(len(gs.BootstrapCommittee)) < gs.Params.MinCommitteeSize {
		return fmt.Errorf(
			"bootstrap_committee has %d members, want at least min_committee_size=%d",
			len(gs.BootstrapCommittee), gs.Params.MinCommitteeSize,
		)
	}

	// seenOperator and seenPubkey are keyed by the NORMALIZED form (decoded address bytes, raw
	// pubkey bytes), not the raw input string (security review L2): a bech32 address is only valid
	// in one case (all-lower or all-upper) per BIP-173, but two syntactically different strings can
	// still decode to the same underlying bytes, and comparing the raw strings would miss that.
	seenOperator := make(map[string]bool, len(gs.BootstrapCommittee))
	seenPubkey := make(map[string]bool, len(gs.BootstrapCommittee))
	for _, m := range gs.BootstrapCommittee {
		if m.OperatorAddress == "" {
			return fmt.Errorf("bootstrap committee member has an empty operator_address")
		}
		accAddr, err := sdk.AccAddressFromBech32(m.OperatorAddress)
		if err != nil {
			return fmt.Errorf("bootstrap committee member has an invalid operator_address %q: %w", m.OperatorAddress, err)
		}
		operatorKey := string(accAddr)
		if seenOperator[operatorKey] {
			return fmt.Errorf("duplicate bootstrap committee operator_address %q", m.OperatorAddress)
		}
		seenOperator[operatorKey] = true

		if len(m.ConsensusPubkey) != Ed25519PubKeyLen {
			return fmt.Errorf(
				"bootstrap committee member %q has a %d-byte consensus_pubkey, want exactly %d (ed25519)",
				m.OperatorAddress, len(m.ConsensusPubkey), Ed25519PubKeyLen,
			)
		}
		pubkeyKey := string(m.ConsensusPubkey)
		if seenPubkey[pubkeyKey] {
			return fmt.Errorf("duplicate bootstrap committee consensus_pubkey for %q", m.OperatorAddress)
		}
		seenPubkey[pubkeyKey] = true
	}

	if gs.Lambda.IsNil() || gs.Lambda.IsNegative() || gs.Lambda.GT(math.LegacyOneDec()) {
		return fmt.Errorf("lambda must be in [0, 1], got %s", gs.Lambda)
	}
	normalBps := BpsFromFraction(gs.Params.CapFractionNormal)
	reducedBps := BpsFromFraction(gs.Params.CapFractionReduced)
	if gs.CurrentCapBps != normalBps && gs.CurrentCapBps != reducedBps {
		return fmt.Errorf("current_cap_bps must be %d (cap_fraction_normal) or %d (cap_fraction_reduced), got %d", normalBps, reducedBps, gs.CurrentCapBps)
	}

	seenRamp := make(map[string]bool, len(gs.RampRecords))
	for _, r := range gs.RampRecords {
		if seenRamp[r.OperatorAddress] {
			return fmt.Errorf("duplicate ramp record for %q", r.OperatorAddress)
		}
		seenRamp[r.OperatorAddress] = true
	}

	seenPower := make(map[string]bool, len(gs.PowerRecords))
	for _, r := range gs.PowerRecords {
		if seenPower[r.OperatorAddress] {
			return fmt.Errorf("duplicate power record for %q", r.OperatorAddress)
		}
		seenPower[r.OperatorAddress] = true
		if r.CometPower < 0 {
			return fmt.Errorf("power record for %q has a negative comet_power %d", r.OperatorAddress, r.CometPower)
		}
	}

	seenSelfBond := make(map[string]bool, len(gs.CommitteeSelfBonds))
	for _, r := range gs.CommitteeSelfBonds {
		if seenSelfBond[r.OperatorAddress] {
			return fmt.Errorf("duplicate committee self-bond record for %q", r.OperatorAddress)
		}
		seenSelfBond[r.OperatorAddress] = true
		if r.ForceBonded.IsNil() || r.ForceBonded.IsNegative() {
			return fmt.Errorf("committee self-bond record for %q must be a non-negative integer", r.OperatorAddress)
		}
	}

	return nil
}
