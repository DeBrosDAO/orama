package types

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// registerNodeWireHex is MsgRegisterNode for the body below. core's
// EncodeRegisterNode must produce the same bytes.
const registerNodeWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a0102222c6f72616d613171717171717171717171717171717171717171717171717171717171717171716e72716c38612a710a0870726f766964657210011a210202020202020202020202020202020202020202020202020202020202020202022240abababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab321868747470733a2f2f6e6f64652e6578616d706c652e636f6d3a026575"

func TestMsgRegisterNode_wireMatchesCoreEncoder(t *testing.T) {
	msg := &MsgRegisterNode{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeId:   "node-a",
		Roles:    []Role{RoleStorage},
		HotKey:   "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a",
		Bindings: []Binding{{
			Service:   "provider",
			KeyType:   KeyTypeSecp256k1,
			Pubkey:    bytes.Repeat([]byte{0x02}, 33),
			Signature: bytes.Repeat([]byte{0xab}, 64),
		}},
		Endpoints:  []string{"https://node.example.com"},
		RegionHint: "eu",
	}
	got, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != registerNodeWireHex {
		t.Fatalf("wire %x", got)
	}
}
