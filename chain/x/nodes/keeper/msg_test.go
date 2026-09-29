package keeper_test

import (
	"encoding/hex"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestMessages_registerBondDeclareCluster(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	_, err := f.Msg.RegisterOperator(f.Ctx, &types.MsgRegisterOperator{Operator: op.String()})
	require.ErrorIs(t, err, types.ErrExists)

	bindings := []types.Binding{
		secpBinding(t, testChainID, op.String(), "hot"),
		edBinding(t, testChainID, op.String(), "tor"),
	}
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage, types.RoleRelay}, bindings)
	require.NotEmpty(t, f.Deposits.locked)

	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(),
		NodeId:   "node-1",
		Roles:    []types.Role{types.RoleStorage},
		HotKey:   hot.String(),
		Bindings: withHot(t, op, hot, edBinding(t, testChainID, op.String(), "ipfs")),
	})
	require.ErrorIs(t, err, types.ErrExists)

	active, err := f.Keeper.IsActive(f.Ctx, "node-1")
	require.NoError(t, err)
	require.False(t, active, "a probation node has no bond yet")

	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: types.DefaultProbationCapacityBytes,
	})
	require.NoError(t, err)
	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: types.DefaultProbationCapacityBytes + 1,
	})
	require.ErrorIs(t, err, types.ErrCapacity)

	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(1),
	})
	require.NoError(t, err)
	active, err = f.Keeper.IsActive(f.Ctx, "node-1")
	require.NoError(t, err)
	require.True(t, active)
	roleActive, err := f.Keeper.IsRoleActive(f.Ctx, "node-1", types.RoleStorage)
	require.NoError(t, err)
	require.True(t, roleActive)
	relayActive, err := f.Keeper.IsRoleActive(f.Ctx, "node-1", types.RoleRelay)
	require.NoError(t, err)
	require.False(t, relayActive)

	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: types.GiB,
	})
	require.NoError(t, err)
	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: types.GiB + 1,
	})
	require.ErrorIs(t, err, types.ErrCapacity)

	gotHot, err := f.Keeper.HotKey(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, hot.String(), gotHot)
	tor, err := f.Keeper.Binding(f.Ctx, "node-1", "tor")
	require.NoError(t, err)
	require.Equal(t, types.KeyTypeEd25519, tor.KeyType)

	_, err = f.Msg.RegisterCluster(f.Ctx, &types.MsgRegisterCluster{
		Operator:        op.String(),
		ClusterId:       "cluster-1",
		BaseDomain:      "example.com",
		PublicEndpoints: []string{"https://cluster.example"},
		MetadataUri:     "https://example.com/meta.json",
	})
	require.NoError(t, err)
	node, err := f.Keeper.GetNode(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, "node-1", node.NodeId)
	_, err = f.Msg.UpdateCluster(f.Ctx, &types.MsgUpdateCluster{
		Operator:        op.String(),
		ClusterId:       "cluster-1",
		BaseDomain:      "cdn.example.com",
		PublicEndpoints: []string{"https://cdn.example"},
	})
	require.NoError(t, err)
	_, err = f.Msg.RetireCluster(f.Ctx, &types.MsgRetireCluster{Operator: op.String(), ClusterId: "cluster-1"})
	require.NoError(t, err)
	_, err = f.Msg.RegisterCluster(f.Ctx, &types.MsgRegisterCluster{
		Operator: op.String(), ClusterId: "cluster-1", BaseDomain: "example.com",
		PublicEndpoints: []string{"https://cluster.example"},
	})
	require.ErrorIs(t, err, types.ErrExists)
	_, err = f.Keeper.GetNode(f.Ctx, "node-1")
	require.NoError(t, err, "retiring a cluster must not touch the node")
	f.requireInvariants(t)
}

func TestRegisterNode_rejectsBadForeignAndDuplicatePubkeys(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	other := newAccount(t)
	f.fund(op, 100)
	f.fund(other, 100)
	f.registerOperator(t, op)
	f.registerOperator(t, other)

	_, err := f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "same-key", Roles: []types.Role{types.RoleValidator},
		HotKey: op.String(), Bindings: []types.Binding{edBinding(t, testChainID, op.String(), "comet")},
	})
	require.ErrorIs(t, err, types.ErrHotKey)

	good := edBinding(t, testChainID, op.String(), "tor")
	bad := good
	bad.Signature = append([]byte(nil), good.Signature...)
	bad.Signature[len(bad.Signature)-1] ^= 0xff
	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "bad-sig", Roles: []types.Role{types.RoleRelay},
		HotKey: hot.String(), Bindings: withHot(t, op, hot, bad),
	})
	require.ErrorIs(t, err, types.ErrInvalidBinding)

	foreign := edBinding(t, "other-chain", op.String(), "tor")
	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "foreign", Roles: []types.Role{types.RoleRelay},
		HotKey: hot.String(), Bindings: withHot(t, op, hot, foreign),
	})
	require.ErrorIs(t, err, types.ErrInvalidBinding)

	f.registerNode(t, op, hot, "first", []types.Role{types.RoleRelay}, []types.Binding{good})
	hot2 := newAccount(t)
	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "second", Roles: []types.Role{types.RoleRelay},
		HotKey: hot2.String(), Bindings: withHot(t, op, hot2, good),
	})
	require.ErrorIs(t, err, types.ErrPubkeyReused)

	_, err = f.Msg.RetireNode(f.Ctx, &types.MsgRetireNode{Operator: op.String(), NodeId: "first"})
	require.NoError(t, err)
	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "reuse-retired", Roles: []types.Role{types.RoleRelay},
		HotKey: hot2.String(), Bindings: withHot(t, op, hot2, good),
	})
	require.ErrorIs(t, err, types.ErrPubkeyReused)

	live := edBinding(t, testChainID, op.String(), "ipfs")
	f.registerNode(t, op, hot2, "tomb", []types.Role{types.RoleStorage}, []types.Binding{live})
	require.NoError(t, f.Keeper.Tombstone(f.Ctx, "tomb"))
	rev, err := f.Keeper.Revoked.Get(f.Ctx, hex.EncodeToString(live.Pubkey))
	require.NoError(t, err)
	require.Equal(t, types.RevocationTombstoned, rev.Reason)
	hot3 := newAccount(t)
	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "reuse-tomb", Roles: []types.Role{types.RoleStorage},
		HotKey: hot3.String(), Bindings: withHot(t, op, hot3, live),
	})
	require.ErrorIs(t, err, types.ErrPubkeyReused)
	f.requireInvariants(t)
}

func TestUpdateNode_rotatesKeysAndRejectsForeignSigner(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	stranger := newAccount(t)
	f.fund(op, 100)
	f.fund(stranger, 10)
	f.registerOperator(t, op)
	f.registerOperator(t, stranger)
	original := edBinding(t, testChainID, op.String(), "tor")
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleRelay}, []types.Binding{original})

	strangerHot := newAccount(t)
	_, err := f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: stranger.String(), NodeId: "node-1", HotKey: strangerHot.String(),
		Bindings: withHot(t, stranger, strangerHot, edBinding(t, testChainID, stranger.String(), "tor")),
	})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: stranger.String(), NodeId: "node-1", Role: types.RoleRelay, Amount: orama(1),
	})
	require.ErrorIs(t, err, types.ErrUnauthorized)

	nextHot := newAccount(t)
	rotated := edBinding(t, testChainID, op.String(), "tor")
	_, err = f.Msg.UpdateNode(f.Ctx, &types.MsgUpdateNode{
		Operator: op.String(), NodeId: "node-1", HotKey: nextHot.String(), Bindings: withHot(t, op, nextHot, rotated),
	})
	require.NoError(t, err)
	got, err := f.Keeper.HotKey(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Equal(t, nextHot.String(), got)
	binding, err := f.Keeper.Binding(f.Ctx, "node-1", "tor")
	require.NoError(t, err)
	require.Equal(t, rotated.Pubkey, binding.Pubkey)

	_, err = f.Msg.RegisterNode(f.Ctx, &types.MsgRegisterNode{
		Operator: op.String(), NodeId: "stolen", Roles: []types.Role{types.RoleRelay},
		HotKey: hot.String(), Bindings: withHot(t, op, hot, original),
	})
	require.ErrorIs(t, err, types.ErrPubkeyReused)
	f.requireInvariants(t)
}

func TestUnbondTiming_andSlashDuringUnbonding(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{
		secpBinding(t, testChainID, op.String(), "hot"),
	})
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(2),
	})
	require.NoError(t, err)
	before := f.Bank.balanceOf(op.String())
	start := f.Ctx.BlockTime()
	_, err = f.Msg.UnbondNode(f.Ctx, &types.MsgUnbondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(1),
	})
	require.NoError(t, err)
	require.True(t, f.Bank.balanceOf(op.String()).Equal(before))
	entries, err := f.Keeper.NodeUnbondings(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, start.Unix()+types.DefaultUnbondingSeconds, entries[0].CompletionUnix)

	f.Ctx = f.Ctx.WithBlockTime(start.Add(time.Duration(types.DefaultUnbondingSeconds-1) * time.Second))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(before), "unbonding pays only at completion")
	entries, err = f.Keeper.NodeUnbondings(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	slashed, err := f.Keeper.Slash(f.Ctx, "node-1", types.RoleStorage, math.LegacyMustNewDecFromStr("0.1"))
	require.NoError(t, err)
	require.True(t, slashed.Equal(orama(2).QuoRaw(10)), slashed.String())
	require.True(t, f.Bank.burned.Equal(orama(2).QuoRaw(10)))
	node, err := f.Keeper.GetNode(f.Ctx, "node-1")
	require.NoError(t, err)
	require.True(t, bondAmount(node, types.RoleStorage).Equal(orama(9).QuoRaw(10)))
	entries, err = f.Keeper.NodeUnbondings(f.Ctx, "node-1")
	require.NoError(t, err)
	require.True(t, entries[0].Amount.Equal(orama(9).QuoRaw(10)))
	f.requireInvariants(t)

	f.Ctx = f.Ctx.WithBlockTime(start.Add(time.Duration(types.DefaultUnbondingSeconds) * time.Second))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(before.Add(orama(9).QuoRaw(10))))
	entries, err = f.Keeper.NodeUnbondings(f.Ctx, "node-1")
	require.NoError(t, err)
	require.Empty(t, entries)
	f.requireInvariants(t)
}

func TestDeclareCapacity_unbondCannotExceedBacking(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{
		edBinding(t, testChainID, op.String(), "ipfs"),
	})
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(2),
	})
	require.NoError(t, err)
	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: 2 * types.GiB,
	})
	require.NoError(t, err)
	_, err = f.Msg.UnbondNode(f.Ctx, &types.MsgUnbondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(3).QuoRaw(2),
	})
	require.ErrorIs(t, err, types.ErrCapacity)
	f.requireInvariants(t)
}

func TestCapacityBuckets_indexByClassAndOperator(t *testing.T) {
	f := newTestFixture(t)
	opA, opB := newAccount(t), newAccount(t)
	f.fund(opA, 50)
	f.fund(opB, 50)
	f.registerOperator(t, opA)
	f.registerOperator(t, opB)
	f.registerNode(t, opA, newAccount(t), "a", []types.Role{types.RoleStorage}, []types.Binding{
		edBinding(t, testChainID, opA.String(), "ipfs"),
	})
	f.registerNode(t, opB, newAccount(t), "b", []types.Role{types.RoleStorage}, []types.Binding{
		edBinding(t, testChainID, opB.String(), "ipfs"),
	})
	for _, op := range []sdk.AccAddress{opA, opB} {
		id := "a"
		if op.Equals(opB) {
			id = "b"
		}
		_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
			Operator: op.String(), NodeId: id, Role: types.RoleStorage, Amount: orama(1),
		})
		require.NoError(t, err)
		_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
			Operator: op.String(), NodeId: id, CapacityBytes: types.GiB,
		})
		require.NoError(t, err)
	}
	class := types.CapacityClass(types.GiB)
	var got []string
	require.NoError(t, f.Keeper.IterateStorageFreeCapacity(f.Ctx, class, func(operator, nodeID string, free uint64) error {
		require.Equal(t, types.GiB, free)
		got = append(got, operator+"/"+nodeID)
		return nil
	}))
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	require.Equal(t, sorted, got)
	require.Len(t, got, 2)

	var only []string
	require.NoError(t, f.Keeper.IterateOperatorFreeCapacity(f.Ctx, class, opA.String(), func(nodeID string, free uint64) error {
		only = append(only, nodeID)
		require.Equal(t, types.GiB, free)
		return nil
	}))
	require.Equal(t, []string{"a"}, only)

	require.NoError(t, f.Keeper.ReserveCapacity(f.Ctx, "a", 1))
	moved := types.CapacityClass(types.GiB - 1)
	require.NotEqual(t, class, moved)
	var left []string
	require.NoError(t, f.Keeper.IterateStorageFreeCapacity(f.Ctx, class, func(operator, nodeID string, _ uint64) error {
		left = append(left, nodeID)
		return nil
	}))
	require.Equal(t, []string{"b"}, left)
	var shifted []string
	require.NoError(t, f.Keeper.IterateOperatorFreeCapacity(f.Ctx, moved, opA.String(), func(nodeID string, free uint64) error {
		shifted = append(shifted, nodeID)
		require.Equal(t, types.GiB-1, free)
		return nil
	}))
	require.Equal(t, []string{"a"}, shifted)
	require.NoError(t, f.Keeper.ReleaseCapacity(f.Ctx, "a", 1))
	f.requireInvariants(t)
}

func TestJailSlashAndServiceDays(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 100)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "store", []types.Role{types.RoleStorage}, []types.Binding{
		edBinding(t, testChainID, op.String(), "ipfs"),
	})
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: op.String(), NodeId: "store", Role: types.RoleStorage, Amount: orama(1),
	})
	require.NoError(t, err)
	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "store", CapacityBytes: types.GiB,
	})
	require.NoError(t, err)
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	days, err := f.Keeper.OperatorServiceDays(f.Ctx, op.String())
	require.NoError(t, err)
	require.Equal(t, uint64(1), days)
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	days, err = f.Keeper.OperatorServiceDays(f.Ctx, op.String())
	require.NoError(t, err)
	require.Equal(t, uint64(1), days, "several blocks in one UTC day count once")

	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(24 * time.Hour))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	days, err = f.Keeper.OperatorServiceDays(f.Ctx, op.String())
	require.NoError(t, err)
	require.Equal(t, uint64(2), days)

	require.NoError(t, f.Keeper.Jail(f.Ctx, "store"))
	active, err := f.Keeper.IsActive(f.Ctx, "store")
	require.NoError(t, err)
	require.False(t, active)
	hotKey, err := f.Keeper.HotKey(f.Ctx, "store")
	require.NoError(t, err)
	require.Equal(t, hot.String(), hotKey)
	var indexed []string
	require.NoError(t, f.Keeper.IterateStorageFreeCapacity(f.Ctx, types.CapacityClass(types.GiB), func(_, nodeID string, _ uint64) error {
		indexed = append(indexed, nodeID)
		return nil
	}))
	require.NotContains(t, indexed, "store")
	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(24 * time.Hour))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	days, err = f.Keeper.OperatorServiceDays(f.Ctx, op.String())
	require.NoError(t, err)
	require.Equal(t, uint64(2), days, "a jailed node does not accrue a service day")

	require.NoError(t, f.Keeper.Unjail(f.Ctx, "store"))
	active, err = f.Keeper.IsActive(f.Ctx, "store")
	require.NoError(t, err)
	require.True(t, active)

	slashed, err := f.Keeper.Slash(f.Ctx, "store", types.RoleStorage, math.LegacyMustNewDecFromStr("0.5"))
	require.NoError(t, err)
	require.True(t, slashed.Equal(orama(1).QuoRaw(2)))
	node, err := f.Keeper.GetNode(f.Ctx, "store")
	require.NoError(t, err)
	require.Equal(t, types.GiB/2, node.DeclaredCapacityBytes)
	f.requireInvariants(t)

	relayOp, relayHot := newAccount(t), newAccount(t)
	f.fund(relayOp, 20)
	f.registerOperator(t, relayOp)
	f.registerNode(t, relayOp, relayHot, "relay", []types.Role{types.RoleRelay}, []types.Binding{
		edBinding(t, testChainID, relayOp.String(), "tor"),
	})
	_, err = f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: relayOp.String(), NodeId: "relay", Role: types.RoleRelay, Amount: orama(1),
	})
	require.NoError(t, err)
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	relayDays, err := f.Keeper.OperatorServiceDays(f.Ctx, relayOp.String())
	require.NoError(t, err)
	require.Equal(t, uint64(1), relayDays, "an active relay counts without storage volume")
}

func TestInvariants_breakWhenStateIsCorrupted(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 20)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, []types.Binding{
		secpBinding(t, testChainID, op.String(), "comet"),
	})
	f.requireInvariants(t)

	node, err := f.Keeper.GetNode(f.Ctx, "node-1")
	require.NoError(t, err)
	node.Bonds = []types.RoleBond{{Role: types.RoleValidator, Amount: orama(1)}}
	node.Status = types.NodeStatusActive
	require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, node.NodeId, node))
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, got.BalanceMatches, got.Detail)

	node.DeclaredCapacityBytes = 10
	require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, node.NodeId, node))
	got, err = f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, got.CapacityBacked, got.Detail)

	node.Bonds = nil
	node.DeclaredCapacityBytes = 0
	node.Status = types.NodeStatusActive
	require.NoError(t, f.Keeper.Nodes.Set(f.Ctx, node.NodeId, node))
	got, err = f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.False(t, got.ActiveRolesBonded, got.Detail)
}

func TestCreditRoleBond_requiresFundsAlreadyInTheModule(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 20)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleValidator}, []types.Binding{
		secpBinding(t, testChainID, op.String(), "comet"),
	})
	err := f.Keeper.CreditRoleBond(f.Ctx, "node-1", types.RoleValidator, orama(1))
	require.Error(t, err)
	f.Bank.fund(types.ModuleName, orama(1))
	require.NoError(t, f.Keeper.CreditRoleBond(f.Ctx, "node-1", types.RoleValidator, orama(1)))
	active, err := f.Keeper.IsActive(f.Ctx, "node-1")
	require.NoError(t, err)
	require.True(t, active)
	f.requireInvariants(t)
}

func TestGenesisRoundTrip(t *testing.T) {
	f := newTestFixture(t)
	op, hot := newAccount(t), newAccount(t)
	f.fund(op, 30)
	f.registerOperator(t, op)
	f.registerNode(t, op, hot, "node-1", []types.Role{types.RoleStorage}, []types.Binding{
		edBinding(t, testChainID, op.String(), "ipfs"),
	})
	_, err := f.Msg.BondNode(f.Ctx, &types.MsgBondNode{
		Operator: op.String(), NodeId: "node-1", Role: types.RoleStorage, Amount: orama(1),
	})
	require.NoError(t, err)
	_, err = f.Msg.DeclareCapacity(f.Ctx, &types.MsgDeclareCapacity{
		Operator: op.String(), NodeId: "node-1", CapacityBytes: types.GiB,
	})
	require.NoError(t, err)
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	f2 := newRawFixture(t)
	var ledger math.Int = math.ZeroInt()
	for _, node := range exported.Nodes {
		for _, bond := range node.Bonds {
			ledger = ledger.Add(bond.Amount)
		}
	}
	for _, entry := range exported.Unbondings {
		ledger = ledger.Add(entry.Amount)
	}
	if ledger.IsPositive() {
		f2.Bank.fund(types.ModuleName, ledger)
	}
	require.NoError(t, f2.Keeper.InitGenesis(f2.Ctx, *exported))
	node, err := f2.Keeper.GetNode(f2.Ctx, "node-1")
	require.NoError(t, err)
	require.True(t, bondAmount(node, types.RoleStorage).Equal(orama(1)))
	require.Equal(t, types.GiB, node.DeclaredCapacityBytes)
	days, err := f2.Keeper.OperatorServiceDays(f2.Ctx, op.String())
	require.NoError(t, err)
	require.Equal(t, uint64(1), days)
	again, err := f2.Keeper.ExportGenesis(f2.Ctx)
	require.NoError(t, err)
	require.NoError(t, again.Validate())
}

func TestClusterAndNodeRejectPrivateEndpoints(t *testing.T) {
	f := newTestFixture(t)
	op := newAccount(t)
	f.fund(op, 10)
	f.registerOperator(t, op)
	_, err := f.Msg.RegisterCluster(f.Ctx, &types.MsgRegisterCluster{
		Operator: op.String(), ClusterId: "c1", BaseDomain: "example.com",
		PublicEndpoints: []string{"https://10.0.0.1/"},
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, types.ErrExists))
}

func bondAmount(node types.Node, role types.Role) math.Int {
	for _, bond := range node.Bonds {
		if bond.Role == role {
			return bond.Amount
		}
	}
	return math.ZeroInt()
}
