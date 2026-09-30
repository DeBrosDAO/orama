package gw

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// SignIn runs the whole wallet sign-in: challenge, a real EIP-191 signature over
// the message exactly as issued, and verify. With a device, the challenge
// names it and the device signs the same message, binding the session to it.
func (c *Client) SignIn(ctx context.Context, w *wallet.EVM, namespace string, dev *wallet.Device) (*Session, error) {
	creq := ChallengeRequest{Wallet: w.Address(), Namespace: namespace}
	if dev != nil {
		creq.DeviceID = dev.ID()
	}
	ch, _, err := c.Challenge(ctx, creq)
	if err != nil {
		return nil, fmt.Errorf("failed to get a challenge for %s in %q: %w", w.Address(), namespace, err)
	}
	sig, err := w.Sign(ch.Message)
	if err != nil {
		return nil, err
	}
	vreq := VerifyRequest{Message: ch.Message, Signature: sig}
	if dev != nil {
		devSig, err := dev.Sign([]byte(ch.Message))
		if err != nil {
			return nil, err
		}
		vreq.DeviceKey, vreq.DeviceSignature, vreq.DeviceLabel = dev.PublicJWK(), devSig, "e2e-"+dev.Alg()
	}
	s, _, err := c.Verify(ctx, vreq)
	if err != nil {
		return nil, fmt.Errorf("failed to sign %s in to %q: %w", w.Address(), namespace, err)
	}
	if s.AccessToken == "" {
		return nil, fmt.Errorf("sign-in of %s to %q returned no access token (status %q)", w.Address(), namespace, s.Status)
	}
	return s, nil
}
