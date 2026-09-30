package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"cosmossdk.io/math"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// cmdTxFee prints the fee a transaction of --gas pays at the chain's current base fee: exactly the
// base fee, with no tip. A stagenet operator holds only earnings, and x/fees pays a base fee from
// earnings but a tip only from a bank balance, so any fee above this is refused.
func cmdTxFee(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("tx-fee", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	gas := fs.Uint64("gas", 0, "the transaction's gas limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *gas == 0 {
		return errors.New("--gas is required and must be positive")
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var resp feestypes.QueryBaseFeeResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/BaseFee", &feestypes.QueryBaseFeeRequest{}, &resp); err != nil {
		return fmt.Errorf("read the base fee from %s (is the chain running?): %w", *rpc, err)
	}
	fmt.Fprintln(out, baseFeeOnly(resp.BaseFee, *gas))
	return nil
}

// baseFeeOnly is gas times the base fee.
func baseFeeOnly(baseFee math.Int, gas uint64) math.Int {
	return baseFee.Mul(math.NewIntFromUint64(gas))
}
