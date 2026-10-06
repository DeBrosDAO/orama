package types

import (
	"errors"

	"cosmossdk.io/collections"
)

// ErrItemRejected marks a failure that belongs to one item (one node, deal, settlement row or
// challenge) and not to the state machine: an operator that cannot be paid, a module account that
// is short, a record the item points to that is gone, a collaborator module that refused the item's
// write. The per-item isolation in BeginBlock and EndBlock rolls such an item back and goes on.
// Every other error (a collection that cannot be read or decoded, a broken counter, an unexpected
// condition) is not about one item and stays fatal, so the block fails instead of running on state
// it can no longer trust.
var ErrItemRejected = errors.New("storage item rejected")

type rejection struct{ err error }

func (r rejection) Error() string        { return r.err.Error() }
func (r rejection) Unwrap() error        { return r.err }
func (r rejection) Is(target error) bool { return target == ErrItemRejected }

// Refuse marks err as a failure of the one item being processed: the returned error matches
// ErrItemRejected, keeps err's message and stays unwrappable to err (transaction result codes still
// resolve). A collections encoding fault is never one item's failure, the state itself cannot be
// read, so it is returned unmarked and stays fatal. Refuse(nil) is nil.
//
// It is for the errors that legitimately mean "this item cannot be done": an item's own
// consistency failure inside x/storage, or a collaborator's refusal that its adapter has
// recognized (x/nodes reporting a node that is gone or not active). It must not be applied to a
// collaborator error unchecked.
func Refuse(err error) error {
	if err == nil || errors.Is(err, collections.ErrEncoding) {
		return err
	}
	return rejection{err}
}

// MaxSettlementAttempts is how many times one settlement payout (or one penalty) is applied before
// it is dropped. A row that fails is queued again behind the rows already waiting and is not due
// again before the next epoch, so a failure that clears by itself (a module account refilled by the
// next epoch's reserve, an earnings account that unfroze) does not cost the operator the payout;
// a row that fails every time is dropped on the last attempt and its reserved mint is burned. A
// miss row is never dropped: its miss counter and eviction do not depend on a collaborator and are
// applied the first time the row is processed. It is a constant, not a parameter: it bounds how
// long a broken row can occupy the queue, and nothing about it is tuned per chain.
const MaxSettlementAttempts uint32 = 5
