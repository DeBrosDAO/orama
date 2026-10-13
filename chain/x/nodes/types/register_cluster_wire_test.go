package types

import (
	"encoding/hex"
	"testing"
)

// registerClusterWireHex is MsgRegisterCluster for the fixed body below, as
// core/pkg/clusterreg.EncodeRegisterCluster must also produce it. The two
// modules do not import each other.
const registerClusterWireHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312096d79636c75737465721a13636c75737465722e6578616d706c652e636f6d221b68747470733a2f2f636c75737465722e6578616d706c652e636f6d"

func TestMsgRegisterCluster_wireMatchesCoreEncoder(t *testing.T) {
	msg := &MsgRegisterCluster{
		Operator:        "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		ClusterId:       "mycluster",
		BaseDomain:      "cluster.example.com",
		PublicEndpoints: []string{"https://cluster.example.com"},
	}
	got, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != registerClusterWireHex {
		t.Fatalf("wire %x", got)
	}
}
