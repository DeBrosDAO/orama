package onchain

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// Privacy is how a transfer is seen on the chain. The zero value is Private, so a transfer asked
// for without a choice can never become public: a public one is a deliberate Public.
type Privacy int

const (
	// Private moves value inside the shielded pool: no sender, recipient or amount on the chain.
	Private Privacy = iota
	// Public is an ordinary bank payment: sender, recipient and amount are on the chain for good.
	Public
)

// PublicWarning is what a caller shows next to every public transfer, before and after it.
const PublicWarning = "This is a public transfer: the sender, the recipient and the amount are visible on the chain to everyone, permanently."

// ErrPrivateUnavailable is returned for a Private transfer. A shielded transfer is proven with the
// owner's shielded spending key, which the RootWallet agent does not hold or use yet (it refuses
// shielded messages). The error never turns into a public payment: the caller must choose Public.
var ErrPrivateUnavailable = errors.New(
	"a private transfer needs the RootWallet to build the shielded bundle, and this RootWallet cannot yet " +
		"(its agent refuses shielded messages); to pay publicly instead, choose a public transfer explicitly")

// Send pays amount norama from the signing account to the orama account to. privacy chooses how the
// payment is seen on the chain; Private is the zero value.
func (c *Client) Send(ctx context.Context, to, amount string, privacy Privacy) (*Receipt, error) {
	switch privacy {
	case Public:
		return c.sendPublic(ctx, to, amount)
	case Private:
		return nil, ErrPrivateUnavailable
	default:
		return nil, fmt.Errorf("unknown privacy %d: choose Private or Public", privacy)
	}
}

func (c *Client) sendPublic(ctx context.Context, to, amount string) (*Receipt, error) {
	p, err := c.PreparePublicSend(ctx, to, amount)
	if err != nil {
		return nil, err
	}
	return p.Submit(ctx)
}

// PreparePublicSend builds a public payment of amount norama to the account to, priced and not yet
// signed: the caller shows Gas and Fee for approval, then calls Submit. Public is in the name on
// purpose; there is no prepared private send.
func (c *Client) PreparePublicSend(ctx context.Context, to, amount string) (*Prepared, error) {
	from, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := clusterreg.EncodeSend(from, to, amount)
	if err != nil {
		return nil, fmt.Errorf("send %s norama to %s: %w", amount, to, err)
	}
	what := "send " + amount + " norama to " + to
	p, err := c.prepare(ctx, clusterreg.SendTypeURL, msg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	p.what = what
	return p, nil
}

// WithdrawEarnings moves amount norama of the signing account's earnings to its own bank balance
// (MsgWithdrawEarnings). The destination is never a parameter: the chain pays the signer.
func (c *Client) WithdrawEarnings(ctx context.Context, amount string) (*Receipt, error) {
	signer, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := clusterreg.EncodeWithdrawEarnings(signer, amount)
	if err != nil {
		return nil, fmt.Errorf("withdraw %s norama of earnings: %w", amount, err)
	}
	return c.sendMsg(ctx, "withdraw "+amount+" norama of earnings", clusterreg.WithdrawEarningsTypeURL, msg)
}
