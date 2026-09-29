package archiver

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
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

// Submit signs msgs with the archiver key and waits for inclusion.
func (c *NodeChain) Submit(ctx context.Context, msgs ...sdk.Msg) error {
	_, err := c.Client.Submit(ctx, c.signer, msgs...)
	return err
}
