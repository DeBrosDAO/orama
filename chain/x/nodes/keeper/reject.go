package keeper

import (
	"errors"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// unbondingRejection marks an error as a failure of paying the one unbonding entry.
type unbondingRejection struct{ err error }

func (r unbondingRejection) Error() string        { return r.err.Error() }
func (r unbondingRejection) Unwrap() error        { return r.err }
func (r unbondingRejection) Is(target error) bool { return target == types.ErrUnbondingRejected }

// rejectUnbonding marks err as a failure of paying one unbonding entry. An encoding fault of a
// collection is never one entry's failure and stays fatal.
func rejectUnbonding(err error) error {
	if err == nil || errors.Is(err, collections.ErrEncoding) {
		return err
	}
	return unbondingRejection{err}
}

// isUnbondingFailure reports whether err is about the one entry being paid: a rejection, or an
// entry that an index still names but that is gone.
func isUnbondingFailure(err error) bool {
	if errors.Is(err, collections.ErrEncoding) {
		return false
	}
	return errors.Is(err, types.ErrUnbondingRejected) || errors.Is(err, collections.ErrNotFound)
}
