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
)

// Params are the inclusion-list limits. Zero is rejected; callers that want
// the C13 defaults should use DefaultParams.
type Params struct {
	ListMaxBytes         int
	MaxEmbeddedListBytes int
	MaxSenderBytes       int
	MaxBlockBytes        int
}

// DefaultParams returns the C13 limits.
func DefaultParams() Params {
	return Params{
		ListMaxBytes:         DefaultListMaxBytes,
		MaxEmbeddedListBytes: DefaultMaxEmbeddedListBytes,
		MaxSenderBytes:       DefaultMaxSenderBytes,
		MaxBlockBytes:        DefaultMaxBlockBytes,
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
	return nil
}
