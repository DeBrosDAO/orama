package onchain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// Faucet asks the chain's faucet to mint amount norama to recipient (MsgFaucet), signed by the
// client's account, which pays the fee. The chain refuses it on a production chain id, unless
// the genesis enabled the faucet, above the maximum drip, inside the recipient's cooldown and past
// the epoch's cap; the simulation that prices the transaction meets those refusals first, so a
// refused drip costs no fee.
func (c *Client) Faucet(ctx context.Context, recipient string, amount *big.Int) (*Receipt, error) {
	signer, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("faucet drip to %s: the amount must be more than zero", recipient)
	}
	msg := clusterreg.Faucet{Signer: signer, Recipient: recipient, Amount: amount.String()}
	if err := clusterreg.ValidateFaucet(msg); err != nil {
		return nil, fmt.Errorf("faucet drip to %s: %w", recipient, err)
	}
	return c.sendMsg(ctx, "faucet drip to "+recipient, clusterreg.FaucetTypeURL, clusterreg.EncodeFaucet(msg))
}
