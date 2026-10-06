package keeper

import (
	"errors"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// rewardRejection marks an error as a failure of the one validator's reward payment.
type rewardRejection struct{ err error }

func (r rewardRejection) Error() string        { return r.err.Error() }
func (r rewardRejection) Unwrap() error        { return r.err }
func (r rewardRejection) Is(target error) bool { return target == types.ErrRewardRejected }

// rejectReward marks err as a failure of one validator's payment: an address that does not
// parse, or a staking, bank or earnings call that refused it. The caller rolls the payment back and
// returns the share to its source. An encoding fault of a collection is never one validator's
// failure and stays fatal.
func rejectReward(err error) error {
	if err == nil || errors.Is(err, collections.ErrEncoding) {
		return err
	}
	return rewardRejection{err}
}
