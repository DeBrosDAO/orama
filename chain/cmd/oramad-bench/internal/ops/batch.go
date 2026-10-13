package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

const (
	// batchPoll is how often a batch asks whether the chain has caught up with it.
	batchPoll = 300 * time.Millisecond
	// DefaultBatchTimeout bounds how long a batch waits for its transactions to be included.
	DefaultBatchTimeout = 2 * time.Minute
)

// ErrBatchTimeout is returned when the chain did not include a whole batch in time.
var ErrBatchTimeout = errors.New("the chain did not include the whole batch in time")

// Batch signs msgs with one account, one transaction each with consecutive sequences, broadcasts
// them in order to the primary node and waits until the account's sequence shows all of them were
// included. It is for setup (funding accounts, minting to them): the transactions are not measured,
// but they are sent without a simulate and a poll each, so many of them take a few blocks, not
// one block each.
//
// gas is the limit every transaction declares; a batch's messages must be alike enough for one
// limit to fit them. Pass 0 to size it by simulating the first message.
func (c *Chain) Batch(ctx context.Context, signer tx.Account, gas uint64, timeout time.Duration, msgs ...sdk.Msg) error {
	price, err := c.GasPrice(ctx)
	if err != nil {
		return err
	}
	return c.BatchPriced(ctx, signer, gas, price, timeout, msgs...)
}

// BatchPriced is Batch at an explicit gas price. A price above the base fee is a tip, which the
// chain takes only from a public bank balance, never from a seat's earnings: an account that holds
// only earnings must offer exactly the base fee.
func (c *Chain) BatchPriced(ctx context.Context, signer tx.Account, gas uint64, price math.Int, timeout time.Duration, msgs ...sdk.Msg) error {
	if len(msgs) == 0 {
		return nil
	}
	if gas == 0 {
		est, err := c.EstimateGas(ctx, signer, msgs[0])
		if err != nil {
			return fmt.Errorf("size the batch of %s: %w", signer.Address, err)
		}
		gas = est
	}
	number, first, err := c.Client.Account(ctx, signer.Address)
	if err != nil {
		return err
	}
	for i, msg := range msgs {
		raw, err := c.Sign(signer, number, first+uint64(i), gas, price, msg)
		if err != nil {
			return fmt.Errorf("sign message %d of the batch: %w", i, err)
		}
		res, err := c.Client.RPC().BroadcastTxSync(ctx, raw)
		if err != nil {
			return fmt.Errorf("broadcast message %d of the batch: %w", i, err)
		}
		if res.Code != 0 {
			return fmt.Errorf("message %d of the batch was refused by CheckTx (code %d): %s", i, res.Code, res.Log)
		}
	}
	return c.waitSequence(ctx, signer.Address, first+uint64(len(msgs)), timeout)
}

// waitSequence polls until the account's committed sequence is at least want.
func (c *Chain) waitSequence(ctx context.Context, addr string, want uint64, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(batchPoll)
	defer tick.Stop()
	var last uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: %s is at sequence %d, wanted %d", ErrBatchTimeout, addr, last, want)
		case <-tick.C:
			_, seq, err := c.Client.Account(ctx, addr)
			if err != nil {
				return err
			}
			last = seq
			if seq >= want {
				return nil
			}
		}
	}
}
