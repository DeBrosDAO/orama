package archiver

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/repair"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// NodeChain is Chain over one oramad RPC, signing with the archiver key.
type NodeChain struct {
	*node.Client
	signer tx.Account
}

// NewNodeChain pairs an RPC client with the archiver's signing account.
func NewNodeChain(client *node.Client, signer tx.Account) (*NodeChain, error) {
	if client == nil || signer.Address == "" {
		return nil, errors.New("archiver chain needs a client and a signer")
	}
	return &NodeChain{Client: client, signer: signer}, nil
}

// Block returns one committed block and its hash.
func (c *NodeChain) Block(ctx context.Context, height int64) (Block, error) {
	res, err := c.Client.Block(ctx, height)
	if err != nil {
		return Block{}, err
	}
	pb, err := res.Block.ToProto()
	if err != nil {
		return Block{}, err
	}
	body, err := pb.Marshal()
	if err != nil {
		return Block{}, err
	}
	return Block{Height: height, Hash: append([]byte(nil), res.BlockID.Hash...), Proto: body}, nil
}

// Range reads the range record. found is false when nobody attested it yet.
func (c *NodeChain) Range(ctx context.Context, start, end int64) (types.RangeRecord, bool, error) {
	return QueryRange(ctx, c.Client, start, end)
}

// QueryRange reads one x/archive range record.
func QueryRange(ctx context.Context, client *node.Client, start, end int64) (types.RangeRecord, bool, error) {
	var resp types.QueryRangeResponse
	err := client.Query(ctx, "/orama.archive.v1.Query/Range", &types.QueryRangeRequest{StartHeight: start, EndHeight: end}, &resp)
	if err != nil {
		var qe *node.QueryError
		if errors.As(err, &qe) && qe.NotFound() {
			return types.RangeRecord{}, false, nil
		}
		return types.RangeRecord{}, false, err
	}
	return resp.Range, true, nil
}

// QueryRangeBlocks reads Params.RangeBlocks, the width every archived range must have.
func QueryRangeBlocks(ctx context.Context, client *node.Client) (int64, error) {
	var resp types.QueryParamsResponse
	if err := client.Query(ctx, "/orama.archive.v1.Query/Params", &types.QueryParamsRequest{}, &resp); err != nil {
		return 0, fmt.Errorf("failed to read the archive params: %w", err)
	}
	return resp.Params.RangeBlocks, nil
}

// Submit signs msgs with the archiver key and waits for inclusion.
func (c *NodeChain) Submit(ctx context.Context, msgs ...sdk.Msg) error {
	_, err := c.Client.Submit(ctx, c.signer, msgs...)
	return err
}

// CreateArchiveDeal submits msg and returns the deal id x/archive reports in its event.
func (c *NodeChain) CreateArchiveDeal(ctx context.Context, msg *types.MsgCreateArchiveDeal) (uint64, error) {
	hash, events, err := c.Client.SubmitWithEvents(ctx, c.signer, msg)
	if err != nil {
		return 0, err
	}
	for _, ev := range events {
		if ev.Type != types.EventTypeCreateArchiveDeal {
			continue
		}
		for _, attr := range ev.Attributes {
			if attr.Key == types.AttributeKeyDealID {
				id, err := strconv.ParseUint(attr.Value, 10, 64)
				if err != nil {
					return 0, fmt.Errorf("transaction %s reports deal id %q: %w", hash, attr.Value, err)
				}
				return id, nil
			}
		}
	}
	return 0, fmt.Errorf("transaction %s opened no archive deal event", hash)
}

// DealStatus reads one x/storage deal.
func (c *NodeChain) DealStatus(ctx context.Context, dealID uint64) (storagetypes.DealStatus, bool, error) {
	var resp storagetypes.QueryDealResponse
	err := c.Client.Query(ctx, "/orama.storage.v1.Query/Deal", &storagetypes.QueryDealRequest{DealId: dealID}, &resp)
	if err != nil {
		var qe *node.QueryError
		if errors.As(err, &qe) && qe.NotFound() {
			return storagetypes.DealStatus_DEAL_STATUS_UNSPECIFIED, false, nil
		}
		return storagetypes.DealStatus_DEAL_STATUS_UNSPECIFIED, false, err
	}
	return resp.Deal.Status, true, nil
}

// LastArchivedHeight reads x/archive's contiguous archived prefix.
func (c *NodeChain) LastArchivedHeight(ctx context.Context) (int64, error) {
	var resp types.QueryLastArchivedHeightResponse
	if err := c.Client.Query(ctx, "/orama.archive.v1.Query/LastArchivedHeight", &types.QueryLastArchivedHeightRequest{}, &resp); err != nil {
		return 0, err
	}
	return resp.LastArchivedHeight, nil
}

// DealSlots reads the deal for its replica count, then each slot. A slot that does not exist yet
// is left out.
func (c *NodeChain) DealSlots(ctx context.Context, dealID uint64) ([]storagetypes.Slot, error) {
	chain := repair.NodeChain{Client: c.Client}
	deal, err := chain.Deal(ctx, dealID)
	if err != nil {
		return nil, fmt.Errorf("read deal: %w", err)
	}
	slots := make([]storagetypes.Slot, 0, deal.Replicas)
	for i := uint32(0); i < deal.Replicas; i++ {
		slot, err := chain.Slot(ctx, dealID, i)
		if err != nil {
			var qe *node.QueryError
			if errors.As(err, &qe) && qe.NotFound() {
				continue
			}
			return nil, fmt.Errorf("read slot %d: %w", i, err)
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

// ProviderURL is the node's first http(s) endpoint in x/nodes.
func (c *NodeChain) ProviderURL(ctx context.Context, nodeID string) (string, error) {
	return repair.NodeChain{Client: c.Client}.ProviderURL(ctx, nodeID)
}
