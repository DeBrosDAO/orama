package clusterreg

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// RegisterOperatorTypeURL is the Any type URL of orama.nodes.v1.MsgRegisterOperator.
	RegisterOperatorTypeURL = "/orama.nodes.v1.MsgRegisterOperator"
	// CreateValidatorTypeURL is the Any type URL of
	// cosmos.staking.v1beta1.MsgCreateValidator.
	CreateValidatorTypeURL = "/cosmos.staking.v1beta1.MsgCreateValidator"
	// ConsensusPubKeyTypeURL is the Any type URL of a validator's consensus key.
	ConsensusPubKeyTypeURL = "/cosmos.crypto.ed25519.PubKey"

	// ConsensusPubKeyLen is the size of a validator's ed25519 consensus key.
	ConsensusPubKeyLen = 32
	// maxMonikerLen is x/staking's MaxMonikerLength.
	maxMonikerLen = 70
)

// EncodeRegisterOperator is the protobuf orama.nodes.v1.MsgRegisterOperator.
func EncodeRegisterOperator(operator string) ([]byte, error) {
	if _, err := CanonicalAccount(operator); err != nil {
		return nil, fmt.Errorf("operator: %w", err)
	}
	return appendStringField(nil, 1, operator), nil
}

// ValidatorCreate is the body of MsgCreateValidator. The operator account both
// signs the message and, under the valoper prefix, is the validator. Rates are
// decimals from 0 to 1 such as "0.10"; SelfBond and MinSelfDelegation are
// positive integers of norama.
type ValidatorCreate struct {
	Operator          string
	Moniker           string
	Identity          string
	Website           string
	SecurityContact   string
	Details           string
	CommissionRate    string
	CommissionMaxRate string
	CommissionMaxStep string
	MinSelfDelegation string
	SelfBond          string
	// ConsensusPubKey is the 32-byte ed25519 key the node signs blocks with.
	ConsensusPubKey []byte
}

var monikerForbidden = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// ValidateValidatorCreate checks the stateless rules of MsgCreateValidator
// that fail late otherwise: a moniker x/staking refuses, a consensus key of
// the wrong size, an amount that is not a positive integer, a rate outside
// 0..1 or a commission whose maximum is below the rate.
func ValidateValidatorCreate(v ValidatorCreate) error {
	if _, err := CanonicalAccount(v.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if strings.TrimSpace(v.Moniker) == "" || len(v.Moniker) > maxMonikerLen || monikerForbidden.MatchString(v.Moniker) {
		return fmt.Errorf("moniker must be 1..%d characters without control characters", maxMonikerLen)
	}
	if len(v.ConsensusPubKey) != ConsensusPubKeyLen {
		return fmt.Errorf("consensus key is %d bytes, want an ed25519 key of %d", len(v.ConsensusPubKey), ConsensusPubKeyLen)
	}
	if !positiveInteger(v.SelfBond) {
		return fmt.Errorf("self bond must be a positive integer of %s", FeeDenom)
	}
	if !positiveInteger(v.MinSelfDelegation) {
		return fmt.Errorf("minimum self delegation must be a positive integer of %s", FeeDenom)
	}
	return validateCommission(v)
}

func validateCommission(v ValidatorCreate) error {
	rate, err := legacyDecInteger(v.CommissionRate)
	if err != nil {
		return err
	}
	maxRate, err := legacyDecInteger(v.CommissionMaxRate)
	if err != nil {
		return err
	}
	if _, err := legacyDecInteger(v.CommissionMaxStep); err != nil {
		return err
	}
	if compareIntegers(rate, maxRate) > 0 {
		return fmt.Errorf("commission rate %s is above its maximum %s", v.CommissionRate, v.CommissionMaxRate)
	}
	return nil
}

// compareIntegers compares two non-negative decimal integers without leading
// zeros, which is how legacyDecInteger writes them.
func compareIntegers(a, b string) int {
	switch {
	case len(a) != len(b):
		return len(a) - len(b)
	default:
		return strings.Compare(a, b)
	}
}

// EncodeCreateValidator is the protobuf cosmos.staking.v1beta1.MsgCreateValidator.
// The deprecated delegator_address is left empty: the validator address and
// the delegator are the same account.
func EncodeCreateValidator(v ValidatorCreate) ([]byte, error) {
	if err := ValidateValidatorCreate(v); err != nil {
		return nil, err
	}
	valoper, err := ValidatorAddress(v.Operator)
	if err != nil {
		return nil, err
	}
	rate, _ := legacyDecInteger(v.CommissionRate)
	maxRate, _ := legacyDecInteger(v.CommissionMaxRate)
	maxStep, _ := legacyDecInteger(v.CommissionMaxStep)

	var desc []byte
	for i, field := range []string{v.Moniker, v.Identity, v.Website, v.SecurityContact, v.Details} {
		if field != "" {
			desc = appendStringField(desc, i+1, field)
		}
	}
	var commission []byte
	for i, dec := range []string{rate, maxRate, maxStep} {
		commission = appendStringField(commission, i+1, dec)
	}
	key := appendBytesField(nil, 1, v.ConsensusPubKey)
	pubAny := appendStringField(nil, 1, ConsensusPubKeyTypeURL)
	pubAny = appendBytesField(pubAny, 2, key)
	coin := appendStringField(nil, 1, FeeDenom)
	coin = appendStringField(coin, 2, v.SelfBond)

	out := appendBytesField(nil, 1, desc)
	out = appendBytesField(out, 2, commission)
	out = appendStringField(out, 3, v.MinSelfDelegation)
	out = appendStringField(out, 5, valoper)
	out = appendBytesField(out, 6, pubAny)
	return appendBytesField(out, 7, coin), nil
}
