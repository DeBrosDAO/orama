package types

import "errors"

// ErrRewardRejected marks a failure that belongs to the payment of one validator's epoch reward:
// an address that does not parse, or a staking, bank or earnings call that refused the payment.
// The payment is rolled back and its share returned to the source module. Any other failure (a
// collection that cannot be read or decoded, the return transfer itself) is a fault of the state
// machine and fails the block.
var ErrRewardRejected = errors.New("validator reward rejected")
