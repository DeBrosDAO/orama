package inclusion

import "fmt"

const (
	// DefaultListMaxBytes is the per-extension cap. A 2-action shielded
	// bundle is about 9 KiB, so 32 KiB holds a few of them.
	DefaultListMaxBytes = 32 * 1024

	// DefaultMaxEmbeddedListBytes bounds the deduplicated transaction bytes
	// embedded with the included extensions. 4 MiB covers a 2/3 quorum of
	// disjoint full lists at the 150-validator active-set ceiling.
	DefaultMaxEmbeddedListBytes = 4 * 1024 * 1024

	// DefaultMaxSenderBytes is the per-sender cap on listed bytes that a
	// block must carry. One sender cannot fill more than a single extension.
	DefaultMaxSenderBytes = 32 * 1024

	// DefaultMaxBlockBytes is the block budget used to decide whether the
	// next listed transaction fits. It matches CometBFT's usual 21 MiB cap.
	DefaultMaxBlockBytes = 21 * 1024 * 1024

	// DefaultMaxAnteAttempts caps how many listed transactions one block's
	// walk runs through the full ante chain. Each attempt is charged to its
	// sender's byte budget as well, but the ante chain is the expensive
	// step, so the count is bounded on its own. Like the verify cap it is the
	// block's total, divided among the extensions that list anything.
	DefaultMaxAnteAttempts = 1024

	// DefaultMaxVerifyAttempts caps how many listed transactions one block's
	// walk runs through signature verification. Verify comes before the
	// sender's byte charge, so a transaction that only names a real sender at
	// its next sequence costs the node a verification and costs that sender
	// nothing. The embedded cap holds ~20,000 of the smallest such
	// transactions, so without this bound every ProcessProposal would run that
	// many signature checks. It is four times the ante cap: a transaction that
	// passes Verify is charged and counted against MaxAnteAttempts, so an
	// honest list needs about one verification per attempt.
	//
	// The cap is the block's total, and it is divided evenly among the vote
	// extensions that list anything, each transaction being charged to the
	// extension that lists it and has the most left. A validator that lists
	// junk therefore uses up its own share, not another validator's: the
	// transactions only others list are still verified and required. The cost
	// of the bound is that a validator (or coalition of f of N) can still
	// starve what only it lists, and no more than f/N of the total. Skipped
	// transactions are not refused: a proposer may still include them, and
	// they stay in every node's mempool.
	DefaultMaxVerifyAttempts = 4096
)

// Params are the inclusion-list limits. Zero is rejected; callers that want
// the C13 defaults should use DefaultParams.
type Params struct {
	ListMaxBytes         int
	MaxEmbeddedListBytes int
	MaxSenderBytes       int
	MaxBlockBytes        int
	MaxAnteAttempts      int
	MaxVerifyAttempts    int
}

// DefaultParams returns the C13 limits.
func DefaultParams() Params {
	return Params{
		ListMaxBytes:         DefaultListMaxBytes,
		MaxEmbeddedListBytes: DefaultMaxEmbeddedListBytes,
		MaxSenderBytes:       DefaultMaxSenderBytes,
		MaxBlockBytes:        DefaultMaxBlockBytes,
		MaxAnteAttempts:      DefaultMaxAnteAttempts,
		MaxVerifyAttempts:    DefaultMaxVerifyAttempts,
	}
}

// Validate checks that every limit is positive.
func (p Params) Validate() error {
	if p.ListMaxBytes <= 0 {
		return fmt.Errorf("%w: list_max_bytes must be positive", ErrParams)
	}
	if p.MaxEmbeddedListBytes <= 0 {
		return fmt.Errorf("%w: max_embedded_list_bytes must be positive", ErrParams)
	}
	if p.MaxSenderBytes <= 0 {
		return fmt.Errorf("%w: max sender bytes must be positive", ErrParams)
	}
	if p.MaxBlockBytes <= 0 {
		return fmt.Errorf("%w: max block bytes must be positive", ErrParams)
	}
	if p.MaxAnteAttempts <= 0 {
		return fmt.Errorf("%w: max ante attempts must be positive", ErrParams)
	}
	if p.MaxVerifyAttempts <= 0 {
		return fmt.Errorf("%w: max verify attempts must be positive", ErrParams)
	}
	return nil
}
