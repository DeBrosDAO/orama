package types

import "errors"

// ErrItemRejected marks a failure that belongs to one item (one node, deal, settlement row or
// challenge) and not to the state machine: an operator that cannot be paid, a module account that
// is short, a record the item points to that is gone, a collaborator module that refused the item's
// write. The per-item isolation in BeginBlock and EndBlock rolls such an item back and goes on.
// Every other error (a collection that cannot be read or decoded, a broken counter, an unexpected
// condition) is not about one item and stays fatal, so the block fails instead of running on state
// it can no longer trust.
var ErrItemRejected = errors.New("storage item rejected")

// MaxSettlementAttempts is how many times one settlement row is applied before it is dropped. A
// row that fails is queued again behind the rows already waiting, so a failure that clears by
// itself (a module account refilled by the next epoch's reserve, an earnings account that
// unfroze) does not cost the operator the payout; a row that fails every time is dropped on the
// last attempt and its reserved mint is burned. It is a constant, not a parameter: it bounds how
// long a broken row can occupy the queue, and nothing about it is tuned per chain.
const MaxSettlementAttempts uint32 = 5
