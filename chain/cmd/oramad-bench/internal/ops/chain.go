// Package ops is what the load generator does to the chain besides the measured broadcast: sign
// offline, price gas, simulate once per transaction type, and run the setup transactions a type
// needs. The measured path never calls Simulate or polls per transaction (client/node's Submit
// does both); setup may, because it is not measured.
package ops

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
)

const (
	// DefaultFeeMultiple is how many times the base fee a pre-signed transaction offers per gas
	// unit. Fees are fixed when a transaction is signed, and the base fee can rise 12.5% a block
	// under load, so a margin keeps a transaction valid for the length of a step.
	DefaultFeeMultiple int64 = 32
	// DefaultGasAdjustPercent scales a simulated gas figure into the gas limit, as client/node does.
	DefaultGasAdjustPercent uint64 = 150

	percent = 100
)

// ErrNoGas is returned when a simulation reports no gas used.
var ErrNoGas = errors.New("simulation reported no gas used")

// Chain is one chain as the load generator sees it: a node to query and the signing parameters.
type Chain struct {
	Client           *node.Client
	ChainID          string
	FeeMultiple      int64
	GasAdjustPercent uint64
}

// New wraps a client. chainID is the chain the transactions are signed for.
func New(client *node.Client, chainID string, feeMultiple int64, gasAdjustPercent uint64) (*Chain, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	if chainID == "" {
		return nil, errors.New("chain id is empty")
	}
	if feeMultiple < 1 {
		return nil, fmt.Errorf("fee multiple %d must be at least 1", feeMultiple)
	}
	if gasAdjustPercent < percent {
		return nil, fmt.Errorf("gas adjustment %d%% would set a limit below the simulated gas", gasAdjustPercent)
	}
	return &Chain{Client: client, ChainID: chainID, FeeMultiple: feeMultiple, GasAdjustPercent: gasAdjustPercent}, nil
}

// GasPrice is the price per gas unit to offer now: the chain's base fee times the fee multiple.
func (c *Chain) GasPrice(ctx context.Context) (math.Int, error) {
	base, err := c.Client.BaseFee(ctx)
	if err != nil {
		return math.Int{}, fmt.Errorf("read the base fee: %w", err)
	}
	return PriceFor(base, c.FeeMultiple), nil
}

// BasePrice is the chain's base fee per gas unit and nothing above it: the price a transaction pays
// when its fee comes from a seat's earnings, which cannot cover a tip.
func (c *Chain) BasePrice(ctx context.Context) (math.Int, error) {
	base, err := c.Client.BaseFee(ctx)
	if err != nil {
		return math.Int{}, fmt.Errorf("read the base fee: %w", err)
	}
	return PriceFor(base, 1), nil
}

// PriceFor is base times multiple, and at least one norama.
func PriceFor(base math.Int, multiple int64) math.Int {
	price := base.MulRaw(multiple)
	if !price.IsPositive() {
		return math.OneInt()
	}
	return price
}

// FeeFor is the fee that pays gas at price, and at least one norama so it is a valid coin.
func FeeFor(gas uint64, price math.Int) sdk.Coins {
	fee := price.Mul(math.NewIntFromUint64(gas))
	if !fee.IsPositive() {
		fee = math.OneInt()
	}
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, fee))
}

// GasLimit scales a simulated gas figure by the adjustment.
func (c *Chain) GasLimit(simulated uint64) uint64 {
	return simulated * c.GasAdjustPercent / percent
}

// Sign builds and signs a transaction offline for the given account state.
func (c *Chain) Sign(acct tx.Account, number, sequence, gas uint64, price math.Int, msg sdk.Msg) ([]byte, error) {
	return c.Client.TxBuilder().Build(acct, tx.Unsigned{
		ChainID: c.ChainID, AccountNumber: number, Sequence: sequence,
		GasLimit: gas, Fee: FeeFor(gas, price), Msgs: []sdk.Msg{msg},
	})
}

// Signerless encodes a transaction with no signature and no fee, declaring exactly gas: the form
// of x/shielded's MsgShieldedTransfer.
func (c *Chain) Signerless(gas uint64, msg sdk.Msg) ([]byte, error) {
	b := c.Client.TxConfig().NewTxBuilder()
	if err := b.SetMsgs(msg); err != nil {
		return nil, fmt.Errorf("build the signerless transaction: %w", err)
	}
	b.SetGasLimit(gas)
	raw, err := c.Client.TxConfig().TxEncoder()(b.GetTx())
	if err != nil {
		return nil, fmt.Errorf("encode the signerless transaction: %w", err)
	}
	return raw, nil
}

// SimulateGas runs a signed transaction through the node's simulation and returns the gas it
// used. The transaction is not included.
func (c *Chain) SimulateGas(ctx context.Context, raw []byte) (uint64, error) {
	var resp txtypes.SimulateResponse
	if err := c.Client.Query(ctx, "/cosmos.tx.v1beta1.Service/Simulate", &txtypes.SimulateRequest{TxBytes: raw}, &resp); err != nil {
		return 0, fmt.Errorf("simulate transaction: %w", err)
	}
	if resp.GasInfo == nil || resp.GasInfo.GasUsed == 0 {
		return 0, ErrNoGas
	}
	return resp.GasInfo.GasUsed, nil
}

// EstimateGas signs msg with the account's current chain state, simulates it and returns the gas
// limit to declare for transactions like it.
func (c *Chain) EstimateGas(ctx context.Context, acct tx.Account, msg sdk.Msg) (uint64, error) {
	number, seq, err := c.Client.Account(ctx, acct.Address)
	if err != nil {
		return 0, err
	}
	price, err := c.BasePrice(ctx)
	if err != nil {
		return 0, err
	}
	raw, err := c.Sign(acct, number, seq, 1, price, msg)
	if err != nil {
		return 0, fmt.Errorf("sign for simulation: %w", err)
	}
	used, err := c.SimulateGas(ctx, raw)
	if err != nil {
		return 0, err
	}
	return c.GasLimit(used), nil
}
