package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
)

func TestArchiveViews_refuseUnknownNodesAndDeals(t *testing.T) {
	SetAddressPrefixes()
	a := NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	ctx := a.NewContext(true)
	nodes := archiveNodes{nodes: a.NodesKeeper}
	_, err := nodes.ArchiverOperator(ctx, "", "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae")
	require.Error(t, err)
	_, err = nodes.ArchiverOperator(ctx, "no-such-node", "orama1qyqszqgpqyqszqgpqyqszqgpqyqszqgp6cszae")
	require.Error(t, err)
	ok, err := archiveStorage{storage: a.StorageKeeper}.ArchiveDealActive(ctx, 42)
	require.NoError(t, err)
	require.False(t, ok, "an unknown deal is not an active ARCHIVE deal")
}
