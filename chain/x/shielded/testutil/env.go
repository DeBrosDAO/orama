package testutil

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	store "github.com/cosmos/cosmos-sdk/store/v2"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// BlockInterval is the time between the blocks an Env produces.
const BlockInterval = 6 * time.Second

// Env is a keeper on a real multistore (IAVL and transient) with the fakes of this package. It
// runs blocks the way the app does: transactions on a cache context that is written only on
// success, EndBlock, then a multistore commit that clears the transient store.
type Env struct {
	T      *testing.T
	Ctx    sdk.Context
	CMS    store.CommitMultiStore
	Keeper keeper.Keeper
	Bank   *Bank
	Fees   *Fees
	Bonder *Bonder
	Nodes  *NodeBonder
	Store  *nullifier.Store
	V1, V2 *Verifier
	Height int64
	Time   time.Time

	key       *storetypes.KVStoreKey
	tkey      *storetypes.TransientStoreKey
	committed snapshot
}

func (e *Env) snapshot() snapshot {
	return snapshot{
		balances: cloneInts(e.Bank.Balances), burned: e.Bank.Burned, earnings: cloneInts(e.Fees.Earnings), feeBalances: cloneInts(e.Fees.FeeBalances),
		delegated: cloneInts(e.Bonder.Delegated), bonds: len(e.Nodes.Bonds),
	}
}

func (e *Env) restore(s snapshot) {
	e.Bank.Balances, e.Bank.Burned = cloneInts(s.balances), s.burned
	e.Fees.Earnings = cloneInts(s.earnings)
	e.Fees.FeeBalances = cloneInts(s.feeBalances)
	e.Bonder.Delegated = cloneInts(s.delegated)
	e.Nodes.Bonds = e.Nodes.Bonds[:s.bonds]
}

// NewEnv builds an Env at height 1. mutate may change the genesis before it is applied.
func NewEnv(t *testing.T, mutate func(*types.GenesisState)) *Env {
	t.Helper()
	sdk.GetConfig().SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	sdk.GetConfig().SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey(types.TransientKey)
	db := dbm.NewMemDB()
	cms := store.NewCommitMultiStore(db, log.NewNopLogger())
	cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
	cms.MountStoreWithDB(tkey, storetypes.StoreTypeTransient, db)
	require.NoError(t, cms.LoadLatestVersion())

	e := &Env{T: t, CMS: cms, key: key, tkey: tkey, Bank: NewBank(), Store: nullifier.NewStore(dbm.NewMemDB()),
		V1: &Verifier{Name: "test-a"}, V2: &Verifier{Name: "test-b"}, Height: 1, Time: time.Unix(1_700_000_000, 0).UTC()}
	e.Fees = NewFees(e.Bank)
	e.Bonder = NewBonder(e.Bank)
	e.Nodes = &NodeBonder{Bank: e.Bank}
	e.Keeper = e.KeeperWith(e.V1, e.V2)
	e.Ctx = sdk.NewContext(cms, cmtproto.Header{}, false, log.NewNopLogger()).WithBlockHeader(e.header())

	gs := types.DefaultGenesisState()
	gs.Params.NullifierFee = paramsInt(1)
	gs.Params.ActionGas = 10
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, e.Keeper.InitGenesis(e.Ctx, *gs))
	e.committed = e.snapshot()
	return e
}

func (e *Env) header() cmtproto.Header {
	return cmtproto.Header{ChainID: "orama-test", Height: e.Height, Time: e.Time}
}

// Tx runs fn as a transaction: on a cache context, written only when fn succeeds.
func (e *Env) Tx(fn func(ctx sdk.Context) error) error {
	before := e.snapshot()
	cache, write := e.Ctx.CacheContext()
	if err := fn(cache); err != nil {
		e.restore(before)
		return err
	}
	write()
	return nil
}

// EndBlock ends the block, commits, and starts the next one.
func (e *Env) EndBlock() {
	e.T.Helper()
	require.NoError(e.T, e.Keeper.EndBlock(e.Ctx))
	e.CMS.Commit()
	e.committed = e.snapshot()
	e.Height++
	e.Time = e.Time.Add(BlockInterval)
	e.Ctx = e.Ctx.WithBlockHeader(e.header())
}

// Blocks ends n blocks.
func (e *Env) Blocks(n int) {
	for i := 0; i < n; i++ {
		e.EndBlock()
	}
}

// CheckCtx is the mempool's view: a check-mode context one block behind the block being built.
func (e *Env) CheckCtx() sdk.Context { return e.Ctx.WithIsCheckTx(true) }

// KeeperWith builds another keeper over the same stores and fakes with these verifiers, to test
// what happens when fewer than two are linked or one is missing.
func (e *Env) KeeperWith(verifiers ...verify.Verifier) keeper.Keeper {
	return keeper.NewKeeper(
		codec.NewProtoCodec(codectypes.NewInterfaceRegistry()),
		runtime.NewKVStoreService(e.key),
		keeper.TransientKVService(runtime.NewTransientStoreService(e.tkey)),
		keeper.Dependencies{
			Bank: e.Bank, Fees: e.Fees, Bonder: e.Bonder, NodeBonder: e.Nodes,
			Tree: Tree{}, Nullifiers: e.Store, Verifiers: verifiers,
		},
	)
}

// EndBlockUncommitted runs the keeper's EndBlock and stops before the multistore commit: the state
// a node is in when it stops between FinalizeBlock and Commit.
func (e *Env) EndBlockUncommitted() {
	e.T.Helper()
	require.NoError(e.T, e.Keeper.EndBlock(e.Ctx))
}

// Crash discards everything not committed, as a restart does, and keeps the nullifier database,
// which is outside the multistore.
func (e *Env) Crash() {
	e.T.Helper()
	require.NoError(e.T, e.CMS.LoadLatestVersion())
	e.restore(e.committed)
	e.Ctx = sdk.NewContext(e.CMS, cmtproto.Header{}, false, log.NewNopLogger()).WithBlockHeader(e.header())
}

// Advance moves the current block's time forward without ending it.
func (e *Env) Advance(d time.Duration) {
	e.Time = e.Time.Add(d)
	e.Ctx = e.Ctx.WithBlockHeader(e.header())
}
