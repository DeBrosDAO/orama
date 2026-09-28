package tx

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/cosmos/cosmos-sdk/client"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signing "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	txsigning "github.com/cosmos/cosmos-sdk/x/tx/signing"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

var (
	// ErrEmptyChainID is returned when a transaction has no chain id. The chain
	// id is part of the SIGN_MODE_DIRECT sign doc, not the tx body, so both
	// building and verifying take it from the caller.
	ErrEmptyChainID = errors.New("chain id is empty")

	// ErrFeeDenom is returned when a fee is missing or denominated in anything
	// other than the chain base denom.
	ErrFeeDenom = errors.New("fee denom is not " + params.BaseDenom)

	// ErrSignature is returned when the signature is not SIGN_MODE_DIRECT, the
	// public key is not the message signer, or the signature does not verify
	// for that signer over the sign doc.
	ErrSignature = errors.New("signature does not match the signer")

	// ErrNotSigner is returned when the account passed to Build is not the
	// transaction's only message signer.
	ErrNotSigner = errors.New("account is not the signer")
)

// Unsigned is a transaction to sign with SIGN_MODE_DIRECT.
type Unsigned struct {
	ChainID       string
	AccountNumber uint64
	Sequence      uint64
	GasLimit      uint64
	// Fee must be a positive amount of norama (params.BaseDenom) only.
	Fee  sdk.Coins
	Msgs []sdk.Msg
	Memo string
}

// Decoded is a verified SIGN_MODE_DIRECT transaction.
type Decoded struct {
	Signer      string
	Fee         sdk.Coins
	MsgTypeURLs []string
}

// Builder builds and verifies Orama transactions with one injected TxConfig.
// Tests pass the chain app's encoder (oramad's TxConfig). This package does
// not construct a second codec.
type Builder struct {
	txConfig client.TxConfig
}

// New returns a builder that encodes with txConfig. txConfig must be the chain
// app's encoder, or any TxConfig that registers the same interfaces.
func New(txConfig client.TxConfig) (*Builder, error) {
	if txConfig == nil {
		return nil, errors.New("tx config is nil")
	}
	return &Builder{txConfig: txConfig}, nil
}

// Build signs tx with account under SIGN_MODE_DIRECT and returns the encoded
// bytes. It does not broadcast them.
func (b *Builder) Build(account Account, tx Unsigned) ([]byte, error) {
	if err := useChainParams(); err != nil {
		return nil, err
	}
	if account.priv == nil {
		return nil, errors.New("account has no private key")
	}
	if tx.ChainID == "" {
		return nil, ErrEmptyChainID
	}
	if err := validateFee(tx.Fee); err != nil {
		return nil, err
	}
	if len(tx.Msgs) == 0 {
		return nil, errors.New("transaction has no messages")
	}

	builder := b.txConfig.NewTxBuilder()
	if err := builder.SetMsgs(tx.Msgs...); err != nil {
		return nil, err
	}
	if tx.Memo != "" {
		builder.SetMemo(tx.Memo)
	}
	builder.SetFeeAmount(tx.Fee)
	builder.SetGasLimit(tx.GasLimit)

	signers, err := builder.GetTx().GetSigners()
	if err != nil {
		return nil, err
	}
	if len(signers) != 1 || !account.signs(signers[0]) {
		return nil, ErrNotSigner
	}

	mode := signing.SignMode_SIGN_MODE_DIRECT
	sig := signing.SignatureV2{
		PubKey: account.priv.PubKey(),
		Data: &signing.SingleSignatureData{
			SignMode: mode,
		},
		Sequence: tx.Sequence,
	}
	// Signer infos have to be set before the sign bytes exist: SIGN_MODE_DIRECT
	// signs the auth info, which includes the public key and sequence, but not
	// the signature itself.
	if err := builder.SetSignatures(sig); err != nil {
		return nil, err
	}

	signerData := authsigning.SignerData{
		Address:       account.Address,
		ChainID:       tx.ChainID,
		AccountNumber: tx.AccountNumber,
		Sequence:      tx.Sequence,
		PubKey:        account.priv.PubKey(),
	}
	signBytes, err := authsigning.GetSignBytesAdapter(
		context.Background(), b.txConfig.SignModeHandler(), mode, signerData, builder.GetTx(),
	)
	if err != nil {
		return nil, err
	}
	sigBytes, err := account.priv.Sign(signBytes)
	if err != nil {
		return nil, err
	}
	sig.Data = &signing.SingleSignatureData{
		SignMode:  mode,
		Signature: sigBytes,
	}
	if err := builder.SetSignatures(sig); err != nil {
		return nil, err
	}
	return b.txConfig.TxEncoder()(builder.GetTx())
}

// Decode verifies txBytes as a SIGN_MODE_DIRECT transaction for chainID and
// accountNumber (both are in the sign doc, not in the raw body) and returns
// the signer, the fee, and each message type URL.
func (b *Builder) Decode(txBytes []byte, chainID string, accountNumber uint64) (Decoded, error) {
	if chainID == "" {
		return Decoded{}, ErrEmptyChainID
	}
	sdkTx, err := b.txConfig.TxDecoder()(txBytes)
	if err != nil {
		return Decoded{}, fmt.Errorf("decode tx: %w", err)
	}
	feeTx, ok := sdkTx.(sdk.FeeTx)
	if !ok {
		return Decoded{}, fmt.Errorf("decode tx: not a fee transaction")
	}
	fee := append(sdk.Coins(nil), feeTx.GetFee()...)
	if err := validateFee(fee); err != nil {
		return Decoded{}, err
	}

	msgs := sdkTx.GetMsgs()
	if len(msgs) == 0 {
		return Decoded{}, errors.New("transaction has no messages")
	}
	urls := make([]string, len(msgs))
	for i, msg := range msgs {
		urls[i] = codectypes.MsgTypeURL(msg)
	}

	signer, err := b.verifySigner(sdkTx, chainID, accountNumber)
	if err != nil {
		return Decoded{}, err
	}
	return Decoded{Signer: signer, Fee: fee, MsgTypeURLs: urls}, nil
}

func (b *Builder) verifySigner(sdkTx sdk.Tx, chainID string, accountNumber uint64) (string, error) {
	sigTx, ok := sdkTx.(authsigning.Tx)
	if !ok {
		return "", fmt.Errorf("%w: not a signed transaction", ErrSignature)
	}
	adaptable, ok := sdkTx.(authsigning.V2AdaptableTx)
	if !ok {
		return "", fmt.Errorf("%w: not a SIGN_MODE_DIRECT transaction", ErrSignature)
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return "", err
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return "", err
	}
	if len(signers) != 1 || len(sigs) != 1 {
		return "", fmt.Errorf("%w: got %d signers and %d signatures", ErrSignature, len(signers), len(sigs))
	}
	sig := sigs[0]
	single, ok := sig.Data.(*signing.SingleSignatureData)
	if !ok || single.SignMode != signing.SignMode_SIGN_MODE_DIRECT {
		return "", fmt.Errorf("%w: not SIGN_MODE_DIRECT", ErrSignature)
	}
	if sig.PubKey == nil || !bytes.Equal(sig.PubKey.Address(), signers[0]) {
		return "", ErrSignature
	}

	signer, err := b.txConfig.SigningContext().AddressCodec().BytesToString(signers[0])
	if err != nil {
		return "", err
	}
	anyPk, err := codectypes.NewAnyWithValue(sig.PubKey)
	if err != nil {
		return "", err
	}
	txSignerData := txsigning.SignerData{
		Address:       signer,
		ChainID:       chainID,
		AccountNumber: accountNumber,
		Sequence:      sig.Sequence,
		PubKey: &anypb.Any{
			TypeUrl: anyPk.TypeUrl,
			Value:   anyPk.Value,
		},
	}
	if err := authsigning.VerifySignature(
		context.Background(),
		sig.PubKey,
		txSignerData,
		sig.Data,
		b.txConfig.SignModeHandler(),
		adaptable.GetSigningTxData(),
	); err != nil {
		return "", fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return signer, nil
}

// validateFee requires a positive fee whose every coin is the base denom.
func validateFee(fee sdk.Coins) error {
	if len(fee) == 0 {
		return ErrFeeDenom
	}
	for _, coin := range fee {
		if coin.Denom != params.BaseDenom {
			return ErrFeeDenom
		}
	}
	if err := fee.Validate(); err != nil {
		return fmt.Errorf("fee: %w", err)
	}
	return nil
}
