package types

import (
	"fmt"
	"regexp"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// subdenomRE is the canonical subdenom: a lowercase letter, then 1 to 43
// more lowercase letters or digits. Slashes are excluded so the denom always
// has exactly three segments.
var subdenomRE = regexp.MustCompile(`^[a-z][a-z0-9]{1,43}$`)

// Denom returns factory/{creator}/{subdenom}.
func Denom(creatorBech32, subdenom string) string {
	return DenomPrefix + "/" + creatorBech32 + "/" + subdenom
}

// DepositID is the x/fees deposit id for a token's metadata. x/fees does not
// interpret the id.
func DepositID(denom string) string {
	return ModuleName + "/" + denom
}

// ParseDenom splits a factory denom into its creator bech32 and subdenom.
func ParseDenom(denom string) (creator, subdenom string, err error) {
	prefix := DenomPrefix + "/"
	if len(denom) <= len(prefix) || denom[:len(prefix)] != prefix {
		return "", "", fmt.Errorf("denom %q is not %s/{creator}/{subdenom}", denom, DenomPrefix)
	}
	rest := denom[len(prefix):]
	slash := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			slash = i
			break
		}
	}
	if slash <= 0 || slash == len(rest)-1 || indexByte(rest[slash+1:], '/') >= 0 {
		return "", "", fmt.Errorf("denom %q is not %s/{creator}/{subdenom}", denom, DenomPrefix)
	}
	creator, subdenom = rest[:slash], rest[slash+1:]
	if _, err := sdk.AccAddressFromBech32(creator); err != nil {
		return "", "", fmt.Errorf("denom %q has an invalid creator: %w", denom, err)
	}
	if err := ValidateSubdenom(subdenom); err != nil {
		return "", "", err
	}
	return creator, subdenom, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ValidateSubdenom checks the subdenom segment.
func ValidateSubdenom(subdenom string) error {
	if !subdenomRE.MatchString(subdenom) {
		return fmt.Errorf("subdenom %q must match %s", subdenom, subdenomRE.String())
	}
	return nil
}

// ValidateMetadata checks the name, symbol and description stored for a token.
func ValidateMetadata(name, symbol, description string) error {
	if err := validateText("name", name, 1, MaxNameLen); err != nil {
		return err
	}
	if err := validateText("symbol", symbol, 1, MaxSymbolLen); err != nil {
		return err
	}
	for i := 0; i < len(symbol); i++ {
		c := symbol[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return fmt.Errorf("symbol %q must be alphanumeric", symbol)
		}
	}
	if err := validateText("description", description, 0, MaxDescriptionLen); err != nil {
		return err
	}
	return nil
}

func validateText(field, value string, minLen, maxLen int) error {
	if len(value) < minLen || len(value) > maxLen {
		return fmt.Errorf("%s length must be %d-%d, got %d", field, minLen, maxLen, len(value))
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] >= 0x7f {
			return fmt.Errorf("%s contains a non-printable byte", field)
		}
	}
	return nil
}

// MetadataBytes is the byte count the state deposit covers: subdenom, name,
// symbol and description. The creator address is part of the denom, not metadata.
func MetadataBytes(subdenom, name, symbol, description string) int {
	return len(subdenom) + len(name) + len(symbol) + len(description)
}

// DepositFor returns perByte multiplied by the metadata byte count.
func DepositFor(perByte math.Int, subdenom, name, symbol, description string) math.Int {
	return perByte.MulRaw(int64(MetadataBytes(subdenom, name, symbol, description)))
}

// TransferFeeAmount returns the token units burned as a transfer fee.
// The division floors. A zero result is possible for a small amount.
func TransferFeeAmount(amount math.Int, bps uint32) (math.Int, error) {
	if amount.IsNil() || amount.IsNegative() {
		return math.Int{}, fmt.Errorf("transfer amount must be non-negative")
	}
	if bps > MaxTransferFeeBps {
		return math.Int{}, fmt.Errorf("transfer fee basis points %d exceed %d", bps, MaxTransferFeeBps)
	}
	if bps == 0 || amount.IsZero() {
		return math.ZeroInt(), nil
	}
	return amount.MulRaw(int64(bps)).QuoRaw(int64(MaxTransferFeeBps)), nil
}

// ValidateExtensions checks a capability set that was fixed at creation.
func ValidateExtensions(e Extensions) error {
	if e.TransferFeeBps > MaxTransferFeeBps {
		return fmt.Errorf("transfer_fee_bps %d exceeds %d", e.TransferFeeBps, MaxTransferFeeBps)
	}
	if e.PermanentDelegate != "" {
		if _, err := sdk.AccAddressFromBech32(e.PermanentDelegate); err != nil {
			return fmt.Errorf("invalid permanent delegate %q: %w", e.PermanentDelegate, err)
		}
	}
	if e.TransferHook != "" {
		if _, err := sdk.AccAddressFromBech32(e.TransferHook); err != nil {
			return fmt.Errorf("invalid transfer hook contract %q: %w", e.TransferHook, err)
		}
	}
	return nil
}

// BlocksShield reports whether freeze, permanent delegate, or pause is still held.
func (e Extensions) BlocksShield() bool {
	return e.Freeze || e.PermanentDelegate != "" || e.Pause
}

// Holds reports whether ext is still set.
func (e Extensions) Holds(ext Extension) bool {
	switch ext {
	case EXTENSION_MINT:
		return e.Mint
	case EXTENSION_FREEZE:
		return e.Freeze
	case EXTENSION_PERMANENT_DELEGATE:
		return e.PermanentDelegate != ""
	case EXTENSION_TRANSFER_FEE:
		return e.TransferFeeBps > 0
	case EXTENSION_NON_TRANSFERABLE:
		return e.NonTransferable
	case EXTENSION_PAUSE:
		return e.Pause
	case EXTENSION_TRANSFER_HOOK:
		return e.TransferHook != ""
	default:
		return false
	}
}

// Renounce returns a copy of e with ext cleared. It fails if ext is not held.
func (e Extensions) Renounce(ext Extension) (Extensions, error) {
	if !e.Holds(ext) {
		return Extensions{}, fmt.Errorf("extension %s is not held", ext)
	}
	switch ext {
	case EXTENSION_MINT:
		e.Mint = false
	case EXTENSION_FREEZE:
		e.Freeze = false
	case EXTENSION_PERMANENT_DELEGATE:
		e.PermanentDelegate = ""
	case EXTENSION_TRANSFER_FEE:
		e.TransferFeeBps = 0
	case EXTENSION_NON_TRANSFERABLE:
		e.NonTransferable = false
	case EXTENSION_PAUSE:
		e.Pause = false
	case EXTENSION_TRANSFER_HOOK:
		e.TransferHook = ""
	default:
		return Extensions{}, fmt.Errorf("unknown extension %s", ext)
	}
	return e, nil
}

// Validate checks one token record for internal consistency.
func (t Token) Validate() error {
	creator, _, err := ParseDenom(t.Denom)
	if err != nil {
		return err
	}
	if t.Creator != creator {
		return fmt.Errorf("token %s creator %s does not match the denom", t.Denom, t.Creator)
	}
	if err := ValidateMetadata(t.Name, t.Symbol, t.Description); err != nil {
		return err
	}
	if err := ValidateExtensions(t.Extensions); err != nil {
		return err
	}
	if t.Shieldable && t.Extensions.BlocksShield() {
		return fmt.Errorf("%s: %w", t.Denom, ErrShieldPowers)
	}
	if t.Issued.IsNil() || t.Issued.IsNegative() {
		return fmt.Errorf("token %s issued supply must be non-negative", t.Denom)
	}
	if t.DepositAmount.IsNil() || !t.DepositAmount.IsPositive() {
		return fmt.Errorf("token %s deposit_amount must be positive", t.Denom)
	}
	return nil
}
