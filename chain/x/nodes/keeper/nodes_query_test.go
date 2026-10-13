package keeper_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestNodes_pagesEveryNodeInIDOrder(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	op := newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	for _, id := range []string{"node-d", "node-a", "node-c", "node-b", "node-e"} {
		f.registerNode(t, op, newAccount(t), id, []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, op.String(), "ipfs")})
	}

	var got []string
	var key []byte
	for pages := 0; pages < 10; pages++ {
		res, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: key, Limit: 2}})
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Nodes), 2)
		for _, node := range res.Nodes {
			got = append(got, node.NodeId)
			require.Equal(t, op.String(), node.Operator)
			require.Equal(t, []types.Role{types.RoleStorage}, node.Roles)
		}
		if key = res.Pagination.NextKey; key == nil {
			break
		}
	}
	require.Equal(t, []string{"node-a", "node-b", "node-c", "node-d", "node-e"}, got, "every node once, in id order")
}

func TestNodes_listsRetiredNodesWithTheirStatus(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{edBinding(t, testChainID, op.String(), "ipfs")})
	_, err := f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "node-1"})
	require.NoError(t, err)

	res, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{})
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	require.Equal(t, types.NodeStatusRetired, res.Nodes[0].Status)
}

func TestNodes_emptyListIsEmptyNotAnError(t *testing.T) {
	f := newTestFixture(t)
	res, err := keeper.NewQueryServerImpl(f.Keeper).Nodes(f.Ctx, &types.QueryNodesRequest{})
	require.NoError(t, err)
	require.NotNil(t, res.Nodes)
	require.Empty(t, res.Nodes)
}

func TestNodes_aPageIsBoundedAndPagesByKey(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)

	_, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Offset: 5}})
	require.Error(t, err, "an offset skips keys one by one, so the query refuses it")

	for i := 0; i < keeper.MaxNodesPerPage+50; i++ {
		id := fmt.Sprintf("node-%03d", i)
		require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, id, types.Node{NodeId: id, Status: types.NodeStatusActive}))
	}
	res, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Limit: 10 * keeper.MaxNodesPerPage, CountTotal: true}})
	require.NoError(t, err)
	require.Len(t, res.Nodes, keeper.MaxNodesPerPage, "a page holds at most 100 nodes")
	require.NotNil(t, res.Pagination.NextKey)
	require.Zero(t, res.Pagination.Total, "a total would walk every node, so it is not computed")
}

// TestNodes_aPageOfTheLargestRecordsStopsAtItsByteBoundUnderTheQueryGasLimit: 100 records of 64
// maximum-length endpoints and 64 bindings (the largest the genesis parameters allow) read more
// than the node's query-gas-limit (2,000,000). On the caller's meter a page ends at
// MaxNodesPageBytes instead, under the limit, and its next_key resumes at the next node, so every
// node is read without the query ever running out of gas.
func TestNodes_aPageOfTheLargestRecordsStopsAtItsByteBoundUnderTheQueryGasLimit(t *testing.T) {
	const queryGasLimit = 2_000_000
	const largest = 64
	f := newTestFixture(t)
	op := newAccount(t)
	endpoints := make([]string, largest)
	bindings := make([]types.Binding, largest)
	for i := range endpoints {
		endpoints[i] = "https://" + strings.Repeat("h", types.MaxEndpointLen-len("https://:443")-3) + fmt.Sprintf("%03d", i) + ":443"
		bindings[i] = types.Binding{Service: fmt.Sprintf("service-%02d", i), KeyType: types.KeyTypeEd25519, Pubkey: make([]byte, 32), Signature: make([]byte, 64)}
	}
	for i := 0; i < keeper.MaxNodesPerPage; i++ {
		id := fmt.Sprintf("node-%03d", i)
		require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, id, types.Node{NodeId: id, Operator: op.String(), Endpoints: endpoints, Bindings: bindings, Status: types.NodeStatusActive}))
	}

	q := keeper.NewQueryServerImpl(f.Keeper)
	seen := map[string]bool{}
	var key []byte
	for pages := 0; ; pages++ {
		require.Less(t, pages, keeper.MaxNodesPerPage, "the pages never reach the last node")
		bounded := f.Ctx.WithGasMeter(storetypes.NewGasMeter(queryGasLimit))
		res, err := q.Nodes(bounded, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: key}})
		require.NoError(t, err, "a page runs on the caller's meter and stays under the query gas limit")
		require.NotEmpty(t, res.Nodes)
		require.Less(t, len(res.Nodes), keeper.MaxNodesPerPage, "a page of the largest records ends at its byte bound")
		for _, n := range res.Nodes {
			require.False(t, seen[n.NodeId], "node %s on two pages", n.NodeId)
			seen[n.NodeId] = true
		}
		key = res.Pagination.NextKey
		if len(key) == 0 {
			break
		}
	}
	require.Len(t, seen, keeper.MaxNodesPerPage, "every node is read once")
}

// The node list has one order; a reverse page is refused rather than answered in id order.
func TestNodes_aReversePageIsRefused(t *testing.T) {
	f := newTestFixture(t)
	_, err := keeper.NewQueryServerImpl(f.Keeper).Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: []byte{}, Reverse: true}})
	require.Error(t, err, "the node list is read in id order only")
}

// The reverse refusal comes before the key is looked at, so a reverse request is answered with the
// reason it is refused, whatever key it carries.
func TestNodes_aReversePageIsRefusedForItsOrderWhateverItsKey(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	for name, key := range map[string][]byte{"no key": nil, "an empty key": {}, "a key that is no node id": {0xff, 0x00, 0x7f}, "a node id": []byte("node-1")} {
		_, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: key, Reverse: true}})
		require.Equal(t, codes.InvalidArgument, status.Code(err), name)
		require.ErrorContains(t, err, "node id order only", name)
	}
}

// pageIDs are the node ids of a page.
func pageIDs(nodes []types.Node) []string {
	ids := []string{}
	for _, n := range nodes {
		ids = append(ids, n.NodeId)
	}
	return ids
}

// TestNodes_aPageKeyIsWhatCollectionPaginateGivesForTheSamePage: the key the query names for the
// next page is the one query.CollectionPaginate names for the same page of the same store, and the
// page holds the same nodes, so a client that pages the node list with the SDK's own keys is
// answered as it would be by any other paginated list.
func TestNodes_aPageKeyIsWhatCollectionPaginateGivesForTheSamePage(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	const count = 7
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("node-%d", i)
		require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, id, types.Node{NodeId: id, Status: types.NodeStatusActive}))
	}
	first, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Limit: 3}})
	require.NoError(t, err)
	second, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: first.Pagination.NextKey, Limit: 3}})
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		key   []byte
		limit uint64
	}{
		{"the first page", nil, 3},
		{"a page of one", nil, 1},
		{"a middle page", first.Pagination.NextKey, 3},
		{"the last page, short", second.Pagination.NextKey, 3},
		{"a page that ends exactly at the last node", nil, count},
		{"a limit above the nodes", nil, 50},
		{"a key between two node ids", []byte("node-30"), 2},
		{"a key that is no node id", []byte{0xff, 0x00, 0x7f}, 3},
		{"a key past the last node", []byte("node-9"), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &query.PageRequest{Key: tc.key, Limit: tc.limit}
			got, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: page})
			require.NoError(t, err)
			want, wantPage, err := query.CollectionPaginate(f.Ctx, f.Keeper.Nodes, page, func(_ string, n types.Node) (types.Node, error) { return n, nil })
			require.NoError(t, err)
			require.Equal(t, pageIDs(want), pageIDs(got.Nodes))
			require.Equal(t, wantPage.NextKey, got.Pagination.NextKey, "the next key is CollectionPaginate's")
			require.Equal(t, len(wantPage.NextKey) == 0, got.Pagination.NextKey == nil, "no next key after the last node")
		})
	}

	past, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: []byte("node-9")}})
	require.NoError(t, err)
	require.NotNil(t, past.Nodes, "an empty page is an empty list")
	require.Empty(t, past.Nodes)
	require.Nil(t, past.Pagination.NextKey, "a key past the last node has no next page")
}

// TestNodes_aRecordLargerThanTheByteBoundIsAPageOfItsOwn: a record that alone reads more than
// MaxNodesPageBytes is returned alone, with the key of the node after it, so the pages still reach
// every node and none repeats. It cannot be cut, and it cannot stop the list.
func TestNodes_aRecordLargerThanTheByteBoundIsAPageOfItsOwn(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	huge := types.Node{NodeId: "node-b", Status: types.NodeStatusActive, Endpoints: []string{strings.Repeat("h", keeper.MaxNodesPageBytes+1)}}
	require.Greater(t, huge.Size(), keeper.MaxNodesPageBytes)
	for _, n := range []types.Node{{NodeId: "node-a", Status: types.NodeStatusActive}, huge, {NodeId: "node-c", Status: types.NodeStatusActive}} {
		require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, n.NodeId, n))
	}

	var pages [][]string
	var key []byte
	for len(pages) < 10 {
		res, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: key}})
		require.NoError(t, err)
		pages = append(pages, pageIDs(res.Nodes))
		if key = res.Pagination.NextKey; key == nil {
			break
		}
	}
	require.Equal(t, [][]string{{"node-a"}, {"node-b"}, {"node-c"}}, pages, "the record over the bound is alone on its page")

	alone, err := q.Nodes(f.Ctx, &types.QueryNodesRequest{Pagination: &query.PageRequest{Key: []byte("node-b")}})
	require.NoError(t, err)
	require.Equal(t, []string{"node-b"}, pageIDs(alone.Nodes), "a page that starts at the record returns it, not an empty page")
	require.Equal(t, []byte("node-c"), alone.Pagination.NextKey)
}
