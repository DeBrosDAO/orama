package clusterreg

import (
	"fmt"
	"strings"
)

const (
	// UnjailTypeURL is the Any type URL of cosmos.slashing.v1beta1.MsgUnjail.
	UnjailTypeURL = "/cosmos.slashing.v1beta1.MsgUnjail"
	// EditValidatorTypeURL is the Any type URL of
	// cosmos.staking.v1beta1.MsgEditValidator.
	EditValidatorTypeURL = "/cosmos.staking.v1beta1.MsgEditValidator"

	// validatorHRP is the bech32 prefix of a validator operator address.
	validatorHRP = accountHRP + "valoper"
	// DoNotModify is x/staking's marker for a description field an edit
	// leaves as it is. An empty field would clear it.
	DoNotModify = "[do-not-modify]"
	// decPrecision is the number of fractional digits cosmos LegacyDec keeps.
	decPrecision = 18
)

// ValidatorAddress is the oramavaloper address of an operator account: the
// same 20 bytes under the validator prefix. x/staking takes the signer of
// MsgUnjail and MsgEditValidator from this address, so the operator account
// signs both.
func ValidatorAddress(operator string) (string, error) {
	if _, err := CanonicalAccount(operator); err != nil {
		return "", fmt.Errorf("operator: %w", err)
	}
	_, data, err := bech32Decode(operator)
	if err != nil {
		return "", err
	}
	return bech32Encode(validatorHRP, data)
}

// EncodeUnjail is the protobuf body of MsgUnjail.
func EncodeUnjail(validator string) []byte {
	return appendStringField(nil, 1, validator)
}

// ValidatorEdit is the body of MsgEditValidator. A description field left as
// DoNotModify is unchanged on chain. CommissionRate is a decimal such as
// "0.05", or empty for no change.
type ValidatorEdit struct {
	Validator       string
	Moniker         string
	Identity        string
	Website         string
	SecurityContact string
	Details         string
	CommissionRate  string
}

// NewValidatorEdit is an edit of validator that changes nothing yet.
func NewValidatorEdit(validator string) ValidatorEdit {
	return ValidatorEdit{
		Validator: validator, Moniker: DoNotModify, Identity: DoNotModify,
		Website: DoNotModify, SecurityContact: DoNotModify, Details: DoNotModify,
	}
}

// EncodeEditValidator is the protobuf body of MsgEditValidator. An empty
// description field is omitted, as proto3 does, and clears that field. The
// commission rate is a cosmos LegacyDec, which marshals as the integer of the
// value scaled by 10^18 (0.05 is "50000000000000000").
func EncodeEditValidator(e ValidatorEdit) ([]byte, error) {
	var desc []byte
	for i, field := range []string{e.Moniker, e.Identity, e.Website, e.SecurityContact, e.Details} {
		if field != "" {
			desc = appendStringField(desc, i+1, field)
		}
	}
	if len(desc) == 0 {
		return nil, fmt.Errorf("every description field is empty; x/staking refuses an empty description")
	}
	out := appendBytesField(nil, 1, desc)
	out = appendStringField(out, 2, e.Validator)
	if e.CommissionRate == "" {
		return out, nil
	}
	rate, err := legacyDecInteger(e.CommissionRate)
	if err != nil {
		return nil, err
	}
	return appendStringField(out, 3, rate), nil
}

// legacyDecInteger turns a decimal in [0, 1] into LegacyDec's wire integer.
func legacyDecInteger(s string) (string, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if whole != "0" && whole != "1" {
		return "", fmt.Errorf("commission rate %q must be a decimal from 0 to 1", s)
	}
	if len(frac) > decPrecision || strings.Trim(frac, "0123456789") != "" {
		return "", fmt.Errorf("commission rate %q must have at most %d decimal digits", s, decPrecision)
	}
	if whole == "1" && strings.Trim(frac, "0") != "" {
		return "", fmt.Errorf("commission rate %q is above 1", s)
	}
	digits := strings.TrimLeft(whole+frac+strings.Repeat("0", decPrecision-len(frac)), "0")
	if digits == "" {
		return "0", nil
	}
	return digits, nil
}
