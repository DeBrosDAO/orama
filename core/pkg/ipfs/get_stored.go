package ipfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// pinsetLookupTimeout bounds GetStored's pinset lookup. The local cluster peer
// answers from its own state in milliseconds, but holds the request while
// that state is loading.
const pinsetLookupTimeout = 3 * time.Second

// ErrNotInPinset is returned by GetStored for a CID this node does not hold
// and the cluster's pinset does not list: nothing the cluster knows of stores
// it (never pinned, or unpinned and reclaimed).
var ErrNotInPinset = errors.New("content is not in the cluster pinset")

// ErrPinsetUnavailable is returned by GetStored when, after a local miss, the
// cluster peer could not say whether the CID is pinned.
var ErrPinsetUnavailable = errors.New("the cluster pinset could not be read")

// GetStored reads stored content by CID for a caller that wants to know when
// it is gone (bugboard #414). It differs from Get in one step: after a local
// miss it asks the pinset before searching the network. A CID nobody pins
// cannot be found by a networked fetch, which could only run out its
// deadline; it is ErrNotInPinset at once instead.
//
// Content this node holds is served without consulting the cluster at all,
// so a local read does not depend on the cluster peer being up. cid must be a
// bare CID, not an /ipfs/ path.
func (c *Client) GetStored(ctx context.Context, cid, ipfsAPIURL string) (io.ReadCloser, error) {
	if ipfsAPIURL == "" {
		ipfsAPIURL = c.ipfsAPIURL
	}
	if r, err := c.getLocal(ctx, ipfsAPIURL, cid); !isContentNotFound(err) {
		return r, err
	}

	lookupCtx, cancel := context.WithTimeout(ctx, pinsetLookupTimeout)
	inPinset, err := c.InPinset(lookupCtx, cid)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			// The caller's own deadline ran out, not the cluster peer.
			return nil, fmt.Errorf("pinset lookup for %s: %w", cid, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", ErrPinsetUnavailable, err)
	}
	if !inPinset {
		return nil, fmt.Errorf("%w: %s", ErrNotInPinset, cid)
	}
	return c.getNetworked(ctx, ipfsAPIURL, cid)
}
