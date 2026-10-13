package wasmpolicy

import (
	"strings"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// RefuseNoramaWrapper rejects a token wrapper that creates a norama denom, or that
// holds a factory denom whose subdenom is norama. Holding the native norama denom
// is allowed: contracts may escrow ORAMA (plans/open-network.md O-B).
func RefuseNoramaWrapper(createDenom string, holdDenoms []string) error {
	if isNoramaDenom(createDenom) {
		return types.ErrNoramaWrapper.Wrapf("create %q", createDenom)
	}
	for _, denom := range holdDenoms {
		sub, ok := factorySubdenom(denom)
		if ok && sub == params.BaseDenom {
			return types.ErrNoramaWrapper.Wrapf("hold %q", denom)
		}
	}
	return nil
}

func isNoramaDenom(denom string) bool {
	if denom == params.BaseDenom {
		return true
	}
	sub, ok := factorySubdenom(denom)
	return ok && sub == params.BaseDenom
}

// factorySubdenom parses factory/{creator}/{subdenom}. creator and subdenom are non-empty
// and subdenom contains no slash.
func factorySubdenom(denom string) (string, bool) {
	rest, ok := strings.CutPrefix(denom, "factory/")
	if !ok {
		return "", false
	}
	creator, sub, ok := strings.Cut(rest, "/")
	if !ok || creator == "" || sub == "" || strings.Contains(sub, "/") {
		return "", false
	}
	return sub, true
}
