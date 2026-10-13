package ops

import (
	"testing"

	gogoproto "github.com/cosmos/gogoproto/proto"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

func packedResponse(t *testing.T, msgs ...gogoproto.Message) []byte {
	t.Helper()
	var data sdk.TxMsgData
	for _, m := range msgs {
		a, err := codectypes.NewAnyWithValue(m)
		if err != nil {
			t.Fatal(err)
		}
		data.MsgResponses = append(data.MsgResponses, a)
	}
	raw, err := gogoproto.Marshal(&data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeResponse_readsTheOneMessageResponse(t *testing.T) {
	var got cnfttypes.MsgCreateTreeResponse
	if err := decodeResponse(packedResponse(t, &cnfttypes.MsgCreateTreeResponse{Id: 42, Root: []byte{1, 2}}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Id != 42 || len(got.Root) != 2 {
		t.Fatalf("decoded %+v", got)
	}
}

func TestDecodeResponse_refusesAnythingButOneResponse(t *testing.T) {
	var got cnfttypes.MsgCreateTreeResponse
	if err := decodeResponse(nil, &got); err == nil {
		t.Error("empty data accepted")
	}
	two := packedResponse(t, &cnfttypes.MsgCreateTreeResponse{Id: 1}, &cnfttypes.MsgCreateTreeResponse{Id: 2})
	if err := decodeResponse(two, &got); err == nil {
		t.Error("two responses accepted")
	}
	if err := decodeResponse([]byte{0xff, 0xff, 0xff}, &got); err == nil {
		t.Error("garbage accepted")
	}
}
