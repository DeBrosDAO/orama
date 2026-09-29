// Package tx builds and verifies Orama SIGN_MODE_DIRECT transactions.
//
// The account key is the 32-byte secp256k1 leaf itself. The BIP-44 coin type
// is params.CoinType, applied through the SDK config the same way
// app.SetAddressPrefixes does. This package does not derive that leaf again,
// does not submit transactions, and does not prove shielded payments. A
// user-to-user MsgSend can be encoded here; whether the chain accepts the
// payment is the app's send restriction, not this builder.
package tx

import (
	"fmt"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"

	sdksecp "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// Account is an Orama bech32 account for one 32-byte secp256k1 private key.
// The key is the account key itself: the Cosmos BIP-44 leaf at params.CoinType.
// DeriveAccount does not run another derivation over those bytes.
type Account struct {
	// Address is the orama bech32 account address.
	Address string

	priv cryptotypes.PrivKey
}

// DeriveAccount returns the orama account for a 32-byte secp256k1 private key.
// The SDK coin type is params.CoinType, the same constant app.SetAddressPrefixes
// applies. The number is not repeated here: the params comment marks the
// current coin type as temporary, so a later change to that constant is the
// only change required.
func DeriveAccount(privKey []byte) (Account, error) {
	if err := useChainParams(); err != nil {
		return Account{}, err
	}
	if len(privKey) != sdksecp.PrivKeySize {
		return Account{}, fmt.Errorf("secp256k1 private key must be %d bytes", sdksecp.PrivKeySize)
	}
	if !validSecp256k1Scalar(privKey) {
		return Account{}, fmt.Errorf("secp256k1 private key is not a valid scalar")
	}

	key := make([]byte, sdksecp.PrivKeySize)
	copy(key, privKey)
	priv := &sdksecp.PrivKey{Key: key}
	return Account{
		Address: sdk.AccAddress(priv.PubKey().Address()).String(),
		priv:    priv,
	}, nil
}

// validSecp256k1Scalar reports whether key is in [1, N-1]. A value outside that
// range is not a secp256k1 private key; the curve library would reduce it and
// silently produce a different account.
func validSecp256k1Scalar(key []byte) bool {
	if len(key) != secp256k1.PrivKeyBytesLen {
		return false
	}
	var scalar secp256k1.ModNScalar
	// SetByteSlice reports whether the 256-bit integer was outside the scalar
	// field. A private key has to be the integer itself, not its reduction.
	if scalar.SetByteSlice(key) || scalar.IsZero() {
		return false
	}
	return true
}

// useChainParams applies Orama's bech32 prefixes and BIP-44 coin type. It is the
// same configuration as app.SetAddressPrefixes. This package does not import
// chain/app, so a client does not link the node; the setters are the SDK's,
// and the values are only the params constants.
func useChainParams() (err error) {
	cfg := sdk.GetConfig()
	if chainParamsApplied(cfg) {
		return nil
	}
	// The SDK panics once the config is sealed. oramad seals it only after
	// SetAddressPrefixes, so a sealed config is already Orama's and the check
	// above returns first. A panic here means some other prefix was sealed.
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("apply orama chain params: %v", rec)
		}
	}()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(params.Bech32PrefixConsAddr, params.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType)
	if !chainParamsApplied(cfg) {
		return fmt.Errorf("orama chain params were not applied")
	}
	return nil
}

func chainParamsApplied(cfg *sdk.Config) bool {
	return cfg.GetBech32AccountAddrPrefix() == params.Bech32Prefix &&
		cfg.GetBech32AccountPubPrefix() == params.Bech32PrefixAccPub &&
		cfg.GetBech32ValidatorAddrPrefix() == params.Bech32PrefixValAddr &&
		cfg.GetBech32ValidatorPubPrefix() == params.Bech32PrefixValPub &&
		cfg.GetBech32ConsensusAddrPrefix() == params.Bech32PrefixConsAddr &&
		cfg.GetBech32ConsensusPubPrefix() == params.Bech32PrefixConsPub &&
		cfg.GetCoinType() == params.CoinType
}

// Signer signs SIGN_MODE_DIRECT sign bytes for one account. Account is one, holding its key in
// memory; a caller whose key lives elsewhere (a signing agent) implements it over that channel.
type Signer interface {
	// AccountAddress is the orama bech32 address of the signing account.
	AccountAddress() string
	// PublicKey is the account's secp256k1 public key.
	PublicKey() cryptotypes.PubKey
	// Sign returns the 64-byte secp256k1 signature over signBytes (the digest is SHA-256, as the
	// SDK's own PrivKey.Sign takes it).
	Sign(signBytes []byte) ([]byte, error)
}

// AccountAddress returns the account's orama bech32 address.
func (a Account) AccountAddress() string { return a.Address }

// PublicKey returns the account's public key, or nil for an account with no key.
func (a Account) PublicKey() cryptotypes.PubKey {
	if a.priv == nil {
		return nil
	}
	return a.priv.PubKey()
}

// Sign signs signBytes with the account's key.
func (a Account) Sign(signBytes []byte) ([]byte, error) {
	if a.priv == nil {
		return nil, fmt.Errorf("account has no private key")
	}
	return a.priv.Sign(signBytes)
}
