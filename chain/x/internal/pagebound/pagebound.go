// Package pagebound bounds the page a public list query may ask for.
package pagebound

import (
	"errors"

	"github.com/cosmos/cosmos-sdk/types/query"
)

// ErrOffset is returned for a page request that sets an offset.
var ErrOffset = errors.New("pagination.offset is not supported: page with the key of the previous page")

// Clamp turns a caller's page request into one that cannot walk the whole map: the limit is
// clamped to max, a total count (which walks every key) is not computed, and an offset (which
// skips keys one by one) is refused in favour of the page key the previous page returned. An unset
// limit is left to the SDK's default of 100, so max must be at least that.
func Clamp(in *query.PageRequest, max uint64) (*query.PageRequest, error) {
	if in == nil {
		return &query.PageRequest{}, nil
	}
	if in.Offset != 0 {
		return nil, ErrOffset
	}
	out := *in
	out.CountTotal = false
	if out.Limit > max {
		out.Limit = max
	}
	return &out, nil
}
