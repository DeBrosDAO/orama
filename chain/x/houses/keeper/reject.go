package keeper

import (
	"errors"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// proposalRejection marks an error as a failure of the one proposal being advanced.
type proposalRejection struct{ err error }

func (r proposalRejection) Error() string        { return r.err.Error() }
func (r proposalRejection) Unwrap() error        { return r.err }
func (r proposalRejection) Is(target error) bool { return target == types.ErrAdvanceRejected }

// rejectAdvance marks err as a failure of the one proposal being advanced: a staking, power or
// operator read that failed, or a tally over data that does not add up. The proposal is retried in
// a later block. An encoding fault of a collection is never one proposal's failure and stays fatal.
func rejectAdvance(err error) error {
	if err == nil || errors.Is(err, collections.ErrEncoding) {
		return err
	}
	return proposalRejection{err}
}
