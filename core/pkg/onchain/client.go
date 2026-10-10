package onchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	// GasSafetyNumerator and GasSafetyDenominator scale the gas a simulation
	// used into the gas limit: 1.5x. A simulation does not run signature
	// verification on the real signature, and the state can move between the
	// simulation and the block.
	GasSafetyNumerator   = 3
	GasSafetyDenominator = 2

	// FeeMarginNumerator and FeeMarginDenominator scale gas x base fee into the
	// fee: 1.5x. The base fee moves with every block and the transaction is
	// included a few blocks after it was read; a fee under the base fee of the
	// block that runs it is refused. What exceeds the base fee is a tip to the
	// proposer.
	FeeMarginNumerator   = 3
	FeeMarginDenominator = 2

	// MaxGas bounds the gas limit a transaction declares, and MaxFeeNorama the fee it
	// pays (100 ORAMA). Both figures come from the chain node the client talks to: a
	// node that lies about its base fee or the gas a simulation used could otherwise
	// have the operator's wallet sign a fee that takes the account's balance. A real
	// transaction of this client uses a few hundred thousand gas at a fee of a few
	// norama, so neither bound is near an honest figure.
	MaxGas       = 200_000_000
	MaxFeeNorama = 100_000_000_000
	// maxBaseFeeDigits bounds the base fee text before it is parsed.
	maxBaseFeeDigits = 40

	// simulationGasLimit and simulationFee fill the gas and fee fields of the
	// transaction that is simulated: the chain does not check either in a
	// simulation, and the builder needs both positive.
	simulationGasLimit = 10_000_000
	simulationFee      = "1"
	// signatureLen is the size of the placeholder signature of a simulation.
	signatureLen = 64
)

// ErrAccountNotFound says the signing account is not on the chain yet. An
// account exists once it has received funds, and only an existing account can
// pay a fee.
var ErrAccountNotFound = errors.New("the signing account does not exist on the chain yet")

// Receipt is a transaction that is in a block.
type Receipt struct {
	// Hash is the transaction hash.
	Hash string
	// Height is the block that holds it.
	Height int64
	// Gas and Fee are what the transaction declared.
	Gas uint64
	Fee string
}

// Client sends one operator's transactions to one chain.
type Client struct {
	chain   Chain
	signer  Signer
	chainID string

	mu       sync.Mutex
	identity *rwagent.OramaAccount
}

// New returns a Client for chainID. The signer is the operator's RootWallet.
func New(chain Chain, signer Signer, chainID string) (*Client, error) {
	if chain == nil || signer == nil {
		return nil, errors.New("a chain and a signer are required")
	}
	if chainID == "" || len(chainID) > 64 {
		return nil, fmt.Errorf("chain id %q must be 1..64 characters", chainID)
	}
	return &Client{chain: chain, signer: signer, chainID: chainID}, nil
}

// Operator returns the address of the account that signs: the operator.
func (c *Client) Operator(ctx context.Context) (string, error) {
	id, err := c.account(ctx)
	if err != nil {
		return "", err
	}
	return id.Address, nil
}

// account asks the RootWallet for the signing account once and remembers it, so
// a run of transactions is one request to the agent and not one per message. The
// signature of every transaction is still checked against it.
func (c *Client) account(ctx context.Context) (*rwagent.OramaAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity != nil {
		return c.identity, nil
	}
	id, err := c.signer.OramaAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the RootWallet's orama account: %w", err)
	}
	c.identity = id
	return id, nil
}

// send builds, signs, broadcasts and waits for one message from the signing
// account.
func (c *Client) send(ctx context.Context, typeURL string, msg []byte) (*Receipt, error) {
	id, err := c.account(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := c.chain.Account(ctx, id.Address)
	if err != nil {
		return nil, accountError(id.Address, err)
	}
	if len(acct.PubKey) != 0 && !bytes.Equal(acct.PubKey, id.PubKey) {
		return nil, fmt.Errorf("account %s is known to the chain under another public key than the RootWallet's", id.Address)
	}
	tx := clusterreg.Direct{
		TypeURL: typeURL, Msg: msg, PubKey: id.PubKey, Sequence: acct.Sequence,
		ChainID: c.chainID, AccountNumber: acct.Number,
	}
	if tx.Gas, err = c.gasFor(ctx, tx); err != nil {
		return nil, err
	}
	if tx.FeeAmount, err = c.feeFor(ctx, tx.Gas); err != nil {
		return nil, err
	}
	return c.signAndBroadcast(ctx, tx, id)
}

func accountError(address string, err error) error {
	var status *clusterreg.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return fmt.Errorf("%w: %s must receive funds first (on a test network, from its faucet): %w", ErrAccountNotFound, address, err)
	}
	return fmt.Errorf("read the chain account of %s: %w", address, err)
}

// gasFor simulates tx and returns the gas limit to declare.
func (c *Client) gasFor(ctx context.Context, tx clusterreg.Direct) (uint64, error) {
	tx.Gas, tx.FeeAmount = simulationGasLimit, simulationFee
	raw, err := tx.TxRaw(make([]byte, signatureLen))
	if err != nil {
		return 0, fmt.Errorf("build the transaction to simulate: %w", err)
	}
	used, err := c.chain.SimulateGas(ctx, raw)
	if err != nil {
		return 0, fmt.Errorf("simulate the transaction: %w", err)
	}
	gas := scaleUpBig(used, GasSafetyNumerator, GasSafetyDenominator)
	if !gas.IsUint64() || gas.Uint64() > MaxGas {
		return 0, fmt.Errorf("the simulation reports %d gas used, and %s with the safety margin is over the %d this client signs: the node it asked may be wrong", used, gas, MaxGas)
	}
	return gas.Uint64(), nil
}

// feeFor is the fee for a transaction of gas: the base fee now, times gas, times
// the margin, and at least 1 norama because a fee must be positive.
func (c *Client) feeFor(ctx context.Context, gas uint64) (string, error) {
	baseFee, err := c.chain.BaseFee(ctx)
	if err != nil {
		return "", fmt.Errorf("read the base fee: %w", err)
	}
	perGas, ok := parseNonNegative(baseFee)
	if !ok {
		return "", fmt.Errorf("the chain's base fee %q is not an integer", baseFee)
	}
	fee := new(big.Int).Mul(perGas, new(big.Int).SetUint64(gas))
	fee = ceilDiv(fee.Mul(fee, big.NewInt(FeeMarginNumerator)), big.NewInt(FeeMarginDenominator))
	if fee.Sign() == 0 {
		return "1", nil
	}
	if fee.Cmp(big.NewInt(MaxFeeNorama)) > 0 {
		return "", fmt.Errorf("the chain asks %s norama for one transaction (base fee %s for %d gas), over the %d this client signs: the node it asked may be wrong", fee, baseFee, gas, int64(MaxFeeNorama))
	}
	return fee.String(), nil
}

// parseNonNegative reads a decimal integer of at most maxBaseFeeDigits digits.
func parseNonNegative(s string) (*big.Int, bool) {
	if s == "" || len(s) > maxBaseFeeDigits || s[0] == '-' || s[0] == '+' {
		return nil, false
	}
	return new(big.Int).SetString(s, 10)
}

func (c *Client) signAndBroadcast(ctx context.Context, tx clusterreg.Direct, id *rwagent.OramaAccount) (*Receipt, error) {
	doc, err := tx.SignDoc()
	if err != nil {
		return nil, fmt.Errorf("build the sign document: %w", err)
	}
	sig, err := c.signer.SignOramaTx(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("sign the transaction: %w", err)
	}
	if sig.Address != id.Address || !bytes.Equal(sig.PubKey, id.PubKey) {
		return nil, fmt.Errorf("the RootWallet signed as %s, not as the account %s it reported", sig.Address, id.Address)
	}
	raw, err := tx.TxRaw(sig.Signature)
	if err != nil {
		return nil, fmt.Errorf("build the transaction: %w", err)
	}
	hash, err := c.chain.Broadcast(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("broadcast the transaction: %w", err)
	}
	height, err := c.chain.WaitIncluded(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("transaction %s: %w", hash, err)
	}
	return &Receipt{Hash: hash, Height: height, Gas: tx.Gas, Fee: tx.FeeAmount}, nil
}

// scaleUpBig is scaleUp without the overflow: the result can exceed a uint64.
func scaleUpBig(v uint64, num, den uint64) *big.Int {
	scaled := new(big.Int).Mul(new(big.Int).SetUint64(v), new(big.Int).SetUint64(num))
	return ceilDiv(scaled, new(big.Int).SetUint64(den))
}

// scaleUp returns ceil(v * num / den).
func scaleUp(v uint64, num, den uint64) uint64 {
	scaled := new(big.Int).Mul(new(big.Int).SetUint64(v), new(big.Int).SetUint64(num))
	return ceilDiv(scaled, new(big.Int).SetUint64(den)).Uint64()
}

func ceilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}
