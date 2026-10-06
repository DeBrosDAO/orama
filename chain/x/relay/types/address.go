package types

import (
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// CanonicalAddress returns the canonical bech32 form of addr.
func CanonicalAddress(addr string) (string, error) {
	parsed, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address %q: %w", addr, err)
	}
	return parsed.String(), nil
}

// NormalizeReporters canonicalizes, de-duplicates, and sorts a reporter set.
// An empty set is rejected when allowEmpty is false (governance updates must
// not wipe the set). Genesis may start empty.
func NormalizeReporters(reporters []string, allowEmpty bool) ([]string, error) {
	if len(reporters) == 0 {
		if allowEmpty {
			return []string{}, nil
		}
		return nil, fmt.Errorf("reporter set is empty")
	}
	if len(reporters) > MaxReporters {
		return nil, fmt.Errorf("reporter set has %d entries, max %d", len(reporters), MaxReporters)
	}
	seen := make(map[string]struct{}, len(reporters))
	out := make([]string, 0, len(reporters))
	for _, raw := range reporters {
		canonical, err := CanonicalAddress(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[canonical]; ok {
			return nil, fmt.Errorf("duplicate reporter %s", canonical)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	sort.Strings(out)
	return out, nil
}
