package indexer

import (
	"fmt"

	cosmath "cosmossdk.io/math"

	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// validatorRef is a validator by its 20-byte consensus address, the key blocks identify signers by.
type validatorRef struct {
	cons []byte
	info ValidatorInfo
}

// snapshotValidators reads every staking validator at the reader's height, stores each in the
// registry and returns them. The power of a bonded validator is x/power's LastPower, the voting
// power CometBFT was given.
func (f *Follower) snapshotValidators(r chainReader, w *writer, epoch uint64) ([]validatorRef, error) {
	vals, err := r.validators()
	if err != nil {
		return nil, err
	}
	refs := make([]validatorRef, 0, len(vals))
	for _, v := range vals {
		ref, err := f.validatorRef(r, v, epoch)
		if err != nil {
			return nil, err
		}
		if err := w.putValidator(ref.cons, ref.info); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (f *Follower) validatorRef(r chainReader, v stakingtypes.Validator, epoch uint64) (validatorRef, error) {
	cons, err := f.consensusAddress(v)
	if err != nil {
		return validatorRef{}, err
	}
	consStr, err := consBech32(cons)
	if err != nil {
		return validatorRef{}, err
	}
	info := ValidatorInfo{
		Operator: v.OperatorAddress, ConsensusAddress: consStr, Moniker: v.Description.Moniker,
		Status: validatorStatus(v.Status), Jailed: v.Jailed, Tokens: v.Tokens.String(), UpdatedEpoch: epoch,
	}
	if v.Status == stakingtypes.Bonded {
		if info.CometPower, err = r.cometPower(v.OperatorAddress); err != nil {
			return validatorRef{}, err
		}
	}
	return validatorRef{cons: cons, info: info}, nil
}

// closeValidators stores, for a closing epoch, a row for every validator that was bonded at its
// end or signed (or missed) a block in it, then drops the epoch's tallies. A validator that left
// the staking module before the epoch ended is read from the registry it was last stored in.
func (f *Follower) closeValidators(r chainReader, w *writer, epoch uint64) error {
	refs, err := f.snapshotValidators(r, w, epoch)
	if err != nil {
		return err
	}
	tallies, err := w.epochCounters(epoch)
	if err != nil {
		return err
	}
	rows := map[string]ValidatorInfo{}
	for _, ref := range refs {
		_, tallied := tallies[string(ref.cons)]
		if tallied || ref.info.Status == StatusBonded {
			rows[string(ref.cons)] = ref.info
		}
	}
	for cons := range tallies {
		if _, ok := rows[cons]; ok {
			continue
		}
		info, err := f.departedValidator(w, []byte(cons))
		if err != nil {
			return err
		}
		rows[cons] = info
	}
	for cons, info := range rows {
		c := tallies[cons]
		if c.Burned.IsNil() {
			c.Burned = cosmath.ZeroInt()
		}
		if err := w.putValidatorEpoch(epoch, []byte(cons), info, c); err != nil {
			return err
		}
	}
	return w.dropEpochCounters(epoch)
}

// departedValidator describes a validator that signed in the epoch but is no longer a staking
// validator: the registry's last record of it, now unbonded with no power, or only its consensus
// address when the index never saw it.
func (f *Follower) departedValidator(w *writer, cons []byte) (ValidatorInfo, error) {
	info, ok, err := w.validator(cons)
	if err != nil {
		return ValidatorInfo{}, err
	}
	if !ok {
		consStr, err := consBech32(cons)
		if err != nil {
			return ValidatorInfo{}, err
		}
		info = ValidatorInfo{ConsensusAddress: consStr, Tokens: "0"}
	}
	info.Status, info.CometPower = StatusUnbonded, 0
	return info, nil
}

// putValidatorEpoch stores a validator's row for a closed epoch and its place in the epoch's
// power-ordered index.
func (w *writer) putValidatorEpoch(epoch uint64, cons []byte, info ValidatorInfo, c counters) error {
	row := ValidatorEpoch{
		Epoch: epoch, Operator: info.Operator, ConsensusAddress: info.ConsensusAddress,
		CometPower: info.CometPower, SignedBlocks: c.Signed, MissedBlocks: c.Missed,
		Slashes: c.Slashes, SlashedBurned: c.Burned.String(), Jailed: info.Jailed, Status: info.Status,
	}
	if total := c.Signed + c.Missed; total > 0 {
		row.UptimeBps = c.Signed * uptimeScale / total
	}
	if err := w.setJSON(valEpochKey(cons, epoch), row); err != nil {
		return err
	}
	if err := w.set(powerIndexKey(epoch, info.CometPower, cons), nil); err != nil {
		return fmt.Errorf("failed to index validator %x of epoch %d by power: %w", cons, epoch, err)
	}
	return nil
}

// uptimeScale is 100 percent in basis points.
const uptimeScale = 10000
