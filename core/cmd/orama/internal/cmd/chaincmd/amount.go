package chaincmd

import (
	"math/big"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

const (
	// noramaDecimals is the decimal places of 1 ORAMA (noramaPerOrama = 10^9).
	noramaDecimals = 9
	// oramaMaxWholeDigits bounds the whole part of an amount typed on a command line. The whole
	// supply is ten digits of ORAMA; a longer number is a typo, and a bounded number is safe to parse.
	oramaMaxWholeDigits = 12
)

// oramaAmount is a plain decimal: digits, and optionally a point and up to noramaDecimals more digits.
var oramaAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,9})?$`)

// parseOramaAmount reads an amount typed in ORAMA ("12", "0.5", "0.000000001") into norama. It takes
// no sign, exponent, separator or unit, and refuses zero, more than nine decimals and a number so
// large it can only be a typo.
func parseOramaAmount(arg string) (*big.Int, error) {
	if !oramaAmount.MatchString(arg) {
		return nil, clierr.Usage("amount %q is not an amount of ORAMA: write digits with at most %d decimals, for example 12 or 0.5", arg, noramaDecimals)
	}
	whole, frac, _ := strings.Cut(arg, ".")
	whole = strings.TrimLeft(whole, "0")
	if len(whole) > oramaMaxWholeDigits {
		return nil, clierr.Usage("amount %s ORAMA is too large: at most %d digits before the decimal point", arg, oramaMaxWholeDigits)
	}
	frac += strings.Repeat("0", noramaDecimals-len(frac))
	norama, _ := new(big.Int).SetString(whole+frac, 10)
	if norama == nil || norama.Sign() == 0 {
		return nil, clierr.Usage("amount must be more than zero")
	}
	return norama, nil
}
