package inclusion

import "errors"

var (
	// ErrParams means the limits are unusable.
	ErrParams = errors.New("inclusion: invalid params")

	// ErrInsufficientPower means the extensions still counted after
	// invalid ones were dropped hold under 2/3 of the supplied total power.
	ErrInsufficientPower = errors.New("inclusion: included voting power is under 2/3")

	// ErrEmbeddedTooLarge means the deduplicated transaction bytes exceed
	// MaxEmbeddedListBytes.
	ErrEmbeddedTooLarge = errors.New("inclusion: deduplicated inclusion list exceeds max_embedded_list_bytes")

	// ErrMissing means a listed transaction that was valid and fit is not
	// in the block.
	ErrMissing = errors.New("inclusion: valid listed transaction missing from the block")

	// ErrOrder means a transaction that is not the next required listed
	// transaction appears before one that was valid and fit.
	ErrOrder = errors.New("inclusion: listed transactions must occupy the front of the block")
)
