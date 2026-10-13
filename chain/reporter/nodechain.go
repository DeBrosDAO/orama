package reporter

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// NodeChain is Chain over one oramad RPC, signing with the reporter key.
type NodeChain struct {
	client *node.Client
	signer tx.Account
}

// NewNodeChain pairs an RPC client with the reporter's signing account.
func NewNodeChain(client *node.Client, signer tx.Account) (*NodeChain, error) {
	if client == nil || signer.Address == "" {
		return nil, errors.New("reporter chain needs a client and a signer")
	}
	return &NodeChain{client: client, signer: signer}, nil
}

// CurrentEpoch reads x/emission's epoch in progress.
func (c *NodeChain) CurrentEpoch(ctx context.Context) (Epoch, error) {
	var resp emissiontypes.QueryCurrentEpochResponse
	if err := c.client.Query(ctx, "/orama.emission.v1.Query/CurrentEpoch", &emissiontypes.QueryCurrentEpochRequest{}, &resp); err != nil {
		return Epoch{}, err
	}
	return Epoch{Number: resp.EpochState.CurrentEpoch, Start: time.Unix(0, resp.EpochState.EpochStartUnixNano).UTC()}, nil
}

// Relay reads one registered relay; found is false when none is.
func (c *NodeChain) Relay(ctx context.Context, fingerprint []byte) (relaytypes.Relay, bool, error) {
	var resp relaytypes.QueryRelayResponse
	err := c.client.Query(ctx, "/orama.relay.v1.Query/Relay", &relaytypes.QueryRelayRequest{RsaFingerprintHex: hex.EncodeToString(fingerprint)}, &resp)
	if err != nil {
		var qe *node.QueryError
		if errors.As(err, &qe) && qe.NotFound() {
			return relaytypes.Relay{}, false, nil
		}
		return relaytypes.Relay{}, false, err
	}
	return resp.Relay, true, nil
}

// EpochSettled reads whether x/relay holds a result for the epoch.
func (c *NodeChain) EpochSettled(ctx context.Context, epoch uint64) (bool, error) {
	var resp relaytypes.QueryEpochResultResponse
	err := c.client.Query(ctx, "/orama.relay.v1.Query/EpochResult", &relaytypes.QueryEpochResultRequest{Epoch: epoch}, &resp)
	if err != nil {
		var qe *node.QueryError
		if errors.As(err, &qe) && qe.NotFound() {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Submit signs one chunk with the reporter key and waits for inclusion.
func (c *NodeChain) Submit(ctx context.Context, msg *relaytypes.MsgReportEpoch) error {
	if _, err := c.client.Submit(ctx, c.signer, msg); err != nil {
		return fmt.Errorf("broadcast MsgReportEpoch: %w", err)
	}
	return nil
}
