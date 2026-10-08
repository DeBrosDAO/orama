package ipfs

import (
	"fmt"

	gocid "github.com/ipfs/go-cid"
)

// CanonicalCID reports why cid is not a CID in the one spelling an upload
// records, or nil. A non-canonical spelling (an identity multihash can carry
// arbitrary bytes, and one CID has several encodings) must not be handed on to
// IPFS, matched against the ownership registry, or named in a capability.
func CanonicalCID(cid string) error {
	parsed, err := gocid.Decode(cid)
	if err != nil {
		return fmt.Errorf("%q is not a valid CID: %w", cid, err)
	}
	if parsed.String() != cid {
		return fmt.Errorf("%q is not a CID in canonical form (%s)", cid, parsed.String())
	}
	return nil
}
