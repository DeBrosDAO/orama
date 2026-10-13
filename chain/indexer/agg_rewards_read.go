package indexer

import (
	"fmt"
)

// EpochRewards returns one page of what a closed epoch paid each validator, by operator address.
// found is false when the epoch is not closed in the index. recorded is the epoch's own flag: when
// it is false the payout was not reported by the chain, and rows is empty.
func (s *Store) EpochRewards(epoch uint64, page, limit int) (rows []ValidatorReward, recorded, found bool, err error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	var row EpochRow
	if found, err = getJSON(snap, epochKey(epoch), &row); err != nil || !found {
		return nil, false, false, err
	}
	if !row.RewardsRecorded {
		return []ValidatorReward{}, false, true, nil
	}
	entries, err := scan(snap, rewardEpochPrefix(epoch), false, page, limit)
	if err != nil {
		return nil, false, false, err
	}
	rows, err = decodeRows[ValidatorReward](entries, "a validator reward")
	return rows, true, true, err
}

// ValidatorRewardEpoch is one closed epoch of a validator's history: whether the epoch's payout
// was recorded, and, if it was, what the validator was paid (nil: it was paid nothing).
type ValidatorRewardEpoch struct {
	Epoch    uint64           `json:"epoch"`
	Recorded bool             `json:"recorded"`
	Reward   *ValidatorReward `json:"reward"`
}

// ValidatorRewards returns one page of a validator's closed epochs, newest first, each with what it
// was paid. The epochs are the ones the validator has a row for (it was bonded at the epoch's end
// or signed in it). found is false if the operator is not a validator the index has seen.
func (s *Store) ValidatorRewards(operator string, page, limit int) ([]ValidatorRewardEpoch, bool, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	cons, ok, err := consensusOfOperator(snap, operator)
	if err != nil || !ok {
		return nil, false, err
	}
	entries, err := scan(snap, valEpochPrefix(cons), true, page, limit)
	if err != nil {
		return nil, false, err
	}
	history, err := decodeRows[ValidatorEpoch](entries, "a validator epoch")
	if err != nil {
		return nil, false, err
	}
	out := make([]ValidatorRewardEpoch, 0, len(history))
	for _, h := range history {
		var epoch EpochRow
		if found, err := getJSON(snap, epochKey(h.Epoch), &epoch); err != nil || !found {
			return nil, false, missingEpoch(h.Epoch, err)
		}
		item := ValidatorRewardEpoch{Epoch: h.Epoch, Recorded: epoch.RewardsRecorded}
		if item.Recorded {
			var r ValidatorReward
			found, err := getJSON(snap, rewardValKey(operator, h.Epoch), &r)
			if err != nil {
				return nil, false, err
			}
			if found {
				item.Reward = &r
			}
		}
		out = append(out, item)
	}
	return out, true, nil
}

func missingEpoch(epoch uint64, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("a validator has a row for epoch %d but the index does not hold the epoch", epoch)
}

// AccountRewards returns one page of what an account was credited by x/power, newest epoch first,
// and the first epoch whose payout the index recorded (0 when none yet): an epoch before it, or any
// epoch while it is 0, was not recorded, so its absence from the page says nothing.
func (s *Store) AccountRewards(addr string, page, limit int) (from uint64, rows []AccountReward, err error) {
	return s.accountRewards(func(snap iterable) ([]entry, error) {
		return scan(snap, rewardAcctPrefix(addr), true, page, limit)
	})
}

// AccountRewardsAfter is AccountRewards from the rewards after the position of after.
func (s *Store) AccountRewardsAfter(addr string, after RewardCursor, limit int) (from uint64, rows []AccountReward, err error) {
	return s.accountRewards(func(snap iterable) ([]entry, error) {
		return scanBefore(snap, rewardAcctPrefix(addr), after.bound(), limit)
	})
}

// accountRewards reads the rewards that read selects, and the first recorded epoch, from one snapshot.
func (s *Store) accountRewards(read func(snap iterable) ([]entry, error)) (from uint64, rows []AccountReward, err error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	from, _, err = getUint(snap, []byte(keyRewardsFrom))
	if err != nil {
		return 0, nil, err
	}
	entries, err := read(snap)
	if err != nil {
		return 0, nil, err
	}
	rows, err = decodeRows[AccountReward](entries, "an account reward")
	return from, rows, err
}
