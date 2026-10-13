package ops

import (
	"context"
	"encoding/hex"
	"fmt"

	gogoproto "github.com/cosmos/gogoproto/proto"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

// SubmitResponse submits msg the way client/node's Submit does (simulate, sign, wait for inclusion)
// and decodes the message response the chain returned into resp. It is for setup transactions that
// create something with an id the chain picks.
func (c *Chain) SubmitResponse(ctx context.Context, signer tx.Signer, msg sdk.Msg, resp gogoproto.Message) error {
	hash, err := c.Client.Submit(ctx, signer, msg)
	if err != nil {
		return err
	}
	raw, err := hex.DecodeString(hash)
	if err != nil {
		return fmt.Errorf("transaction hash %q is not hex: %w", hash, err)
	}
	res, err := c.Client.RPC().Tx(ctx, raw, false)
	if err != nil {
		return fmt.Errorf("read the result of transaction %s: %w", hash, err)
	}
	return decodeResponse(res.TxResult.Data, resp)
}

// decodeResponse unpacks the single message response in a transaction result's data.
func decodeResponse(data []byte, resp gogoproto.Message) error {
	var msgData sdk.TxMsgData
	if err := gogoproto.Unmarshal(data, &msgData); err != nil {
		return fmt.Errorf("decode the transaction result data: %w", err)
	}
	if len(msgData.MsgResponses) != 1 {
		return fmt.Errorf("the transaction returned %d message responses, want 1", len(msgData.MsgResponses))
	}
	if err := gogoproto.Unmarshal(msgData.MsgResponses[0].Value, resp); err != nil {
		return fmt.Errorf("decode the message response %s: %w", msgData.MsgResponses[0].TypeUrl, err)
	}
	return nil
}
