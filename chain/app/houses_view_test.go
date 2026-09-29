package app

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestHouseOperators_networksComeFromActiveNodesWithEndpointAndASN(t *testing.T) {
	SetAddressPrefixes()
	a := NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	const now = 2_000_000_000
	ctx := sdk.NewContext(a.CommitMultiStore().CacheMultiStore(), cmtproto.Header{Height: 1, Time: time.Unix(now, 0)}, false, log.NewNopLogger())
	lock := nodestypes.DefaultParams()
	lock.NetworkIdentityLockSeconds = 1000
	require.NoError(t, a.NodesKeeper.Params.Set(ctx, lock))

	nodes := []nodestypes.Node{
		{NodeId: "a-2", Operator: "op-full", Status: nodestypes.NodeStatusActive, Asn: 13335, Endpoints: []string{"https://203.0.113.9:443"}},
		{NodeId: "a-1", Operator: "op-full", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://198.51.100.9:443"}},
		{NodeId: "b-1", Operator: "op-no-asn", Status: nodestypes.NodeStatusActive, Endpoints: []string{"https://198.51.100.9:443"}},
		{NodeId: "c-1", Operator: "op-hostname", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://node.example:443"}},
		{NodeId: "d-1", Operator: "op-jailed", Status: nodestypes.NodeStatusJailed, Asn: 15169, Endpoints: []string{"https://198.51.100.9:443"}},
		{NodeId: "e-1", Operator: "op-second-node", Status: nodestypes.NodeStatusRegistered, Asn: 1, Endpoints: []string{"https://192.0.2.1:443"}},
		{NodeId: "e-2", Operator: "op-second-node", Status: nodestypes.NodeStatusActive, Asn: 16509, Endpoints: []string{"https://192.0.2.2:443"}},
		// Registered or changed inside the lock: not counted until the lock has passed.
		{NodeId: "f-1", Operator: "op-fresh", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://198.51.100.9:443"}, IdentitySinceUnix: now - 10},
		{NodeId: "g-1", Operator: "op-settled", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://198.51.100.9:443"}, IdentitySinceUnix: now - 1000},
	}
	for _, n := range nodes {
		require.NoError(t, a.NodesKeeper.Nodes.Set(ctx, n.NodeId, n))
	}

	got, err := houseOperators{nodes: a.NodesKeeper}.networks(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]operatorNetwork{
		"op-full":        {prefix16: "198.51.0.0/16", asn: 15169},
		"op-second-node": {prefix16: "192.0.0.0/16", asn: 16509},
		"op-settled":     {prefix16: "198.51.0.0/16", asn: 15169},
	}, got, "the lowest-id active node with both a /16 and an ASN, past the identity lock, speaks for its operator")
}
