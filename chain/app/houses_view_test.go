package app

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestOperatorNetwork_comesFromActiveNodesWithEndpointAndASN(t *testing.T) {
	SetAddressPrefixes()
	a := NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	const now = 2_000_000_000
	ctx := sdk.NewContext(a.CommitMultiStore().CacheMultiStore(), cmtproto.Header{Height: 1, Time: time.Unix(now, 0)}, false, log.NewNopLogger())
	lock := nodestypes.DefaultParams()
	lock.NetworkIdentityLockSeconds = 1000
	require.NoError(t, a.NodesKeeper.Params.Set(ctx, lock))

	nodes := []nodestypes.Node{
		{NodeId: "a-2", Operator: "op-full", Status: nodestypes.NodeStatusActive, Asn: 13335, Endpoints: []string{"https://93.184.113.9:443"}},
		{NodeId: "a-1", Operator: "op-full", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://45.33.100.9:443"}},
		{NodeId: "b-1", Operator: "op-no-asn", Status: nodestypes.NodeStatusActive, Endpoints: []string{"https://45.33.100.9:443"}},
		{NodeId: "c-1", Operator: "op-hostname", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://node.example:443"}},
		{NodeId: "d-1", Operator: "op-jailed", Status: nodestypes.NodeStatusJailed, Asn: 15169, Endpoints: []string{"https://45.33.100.9:443"}},
		{NodeId: "e-1", Operator: "op-second-node", Status: nodestypes.NodeStatusRegistered, Asn: 1, Endpoints: []string{"https://151.101.2.1:443"}},
		{NodeId: "e-2", Operator: "op-second-node", Status: nodestypes.NodeStatusActive, Asn: 16509, Endpoints: []string{"https://151.101.2.2:443"}},
		// Registered or changed inside the lock: not counted until the lock has passed.
		{NodeId: "f-1", Operator: "op-fresh", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://45.33.100.9:443"}, IdentitySinceUnix: now - 10},
		{NodeId: "g-1", Operator: "op-settled", Status: nodestypes.NodeStatusActive, Asn: 15169, Endpoints: []string{"https://45.33.100.9:443"}, IdentitySinceUnix: now - 1000},
	}
	for _, n := range nodes {
		require.NoError(t, a.NodesKeeper.Nodes.Set(ctx, n.NodeId, n))
		require.NoError(t, a.NodesKeeper.NodesByOperator.Set(ctx, collections.Join(n.Operator, n.NodeId)))
	}

	type network struct {
		prefix16 string
		asn      uint32
	}
	got := map[string]network{}
	for _, op := range []string{"op-full", "op-no-asn", "op-hostname", "op-jailed", "op-second-node", "op-fresh", "op-settled", "op-unknown"} {
		prefix, asn, err := a.NodesKeeper.OperatorNetwork(ctx, op)
		require.NoError(t, err)
		if prefix != "" || asn != 0 {
			got[op] = network{prefix16: prefix, asn: asn}
		}
	}
	require.Equal(t, map[string]network{
		"op-full":        {prefix16: "45.33.0.0/16", asn: 15169},
		"op-second-node": {prefix16: "151.101.0.0/16", asn: 16509},
		"op-settled":     {prefix16: "45.33.0.0/16", asn: 15169},
	}, got, "the lowest-id active node with both a /16 and an ASN, past the identity lock, speaks for its operator")
}
