package keeper_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetCoinType(params.CoinType)
	m.Run()
}

type fakeBank struct {
	balances map[string]math.Int
	burned   math.Int
	minted   math.Int
}

func newFakeBank() *fakeBank {
	return &fakeBank{balances: map[string]math.Int{}, burned: math.ZeroInt(), minted: math.ZeroInt()}
}

func (b *fakeBank) balanceOf(key string) math.Int {
	if v, ok := b.balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

func (b *fakeBank) fund(key string, amt math.Int) {
	b.balances[key] = b.balanceOf(key).Add(amt)
}

func (b *fakeBank) sub(key string, amt math.Int) error {
	cur := b.balanceOf(key)
	if cur.LT(amt) {
		return errorsmod.Wrapf(sdkerrors.ErrInsufficientFunds, "%s has %s, needs %s", key, cur, amt)
	}
	next := cur.Sub(amt)
	if next.IsZero() {
		delete(b.balances, key)
	} else {
		b.balances[key] = next
	}
	return nil
}

func (b *fakeBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, recipient string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(sender.String(), amount); err != nil {
		return err
	}
	b.fund(recipient, amount)
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToModule(_ context.Context, sender, recipient string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(sender, amount); err != nil {
		return err
	}
	b.fund(recipient, amount)
	return nil
}

func (b *fakeBank) SendCoinsFromModuleToAccount(_ context.Context, sender string, recipient sdk.AccAddress, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(sender, amount); err != nil {
		return err
	}
	b.fund(recipient.String(), amount)
	return nil
}

func (b *fakeBank) MintCoins(_ context.Context, module string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	b.fund(module, amount)
	b.minted = b.minted.Add(amount)
	return nil
}

func (b *fakeBank) BurnCoins(_ context.Context, module string, amt sdk.Coins) error {
	amount := amt.AmountOf(params.BaseDenom)
	if err := b.sub(module, amount); err != nil {
		return err
	}
	b.burned = b.burned.Add(amount)
	return nil
}

func (b *fakeBank) SpendableCoins(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(params.BaseDenom, b.balanceOf(addr.String())))
}

func (b *fakeBank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	if v, ok := b.balances[addr.String()]; ok {
		return sdk.NewCoin(denom, v)
	}
	for _, name := range []string{types.ModuleName, types.EscrowModuleName, types.ArchiveModuleName, "fees", "fees_deposits"} {
		if authtypes.NewModuleAddress(name).Equals(addr) {
			return sdk.NewCoin(denom, b.balanceOf(name))
		}
	}
	return sdk.NewCoin(denom, math.ZeroInt())
}

type fakeEarnings struct {
	bank *fakeBank
	bal  map[string]math.Int
	// funded records every FundSpendFromEarnings call, in order.
	funded []fundCall
	// failCredit makes CreditEarnings fail for this operator address, with creditErr when set and
	// otherwise with a blocked-account refusal.
	failCredit string
	creditErr  error
	// faultCredit makes CreditEarnings fail for this operator address with an error that is not a
	// refusal about the item.
	faultCredit string
}

type fundCall struct {
	addr   sdk.AccAddress
	amount math.Int
}

// FundSpendFromEarnings only records the request: what it moves is x/fees' own tested behaviour.
func (e *fakeEarnings) FundSpendFromEarnings(_ context.Context, addr sdk.AccAddress, _ string, needed math.Int) error {
	e.funded = append(e.funded, fundCall{addr: addr, amount: needed})
	return nil
}

func (e *fakeEarnings) CreditEarnings(_ context.Context, sender string, addr sdk.AccAddress, amt sdk.Coin) error {
	if !amt.IsPositive() {
		return nil
	}
	if e.faultCredit != "" && e.faultCredit == addr.String() {
		return errf("earnings ledger of %s cannot be decoded", addr)
	}
	if e.failCredit != "" && e.failCredit == addr.String() {
		if e.creditErr != nil {
			return e.creditErr
		}
		return errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "earnings account of %s is blocked", addr)
	}
	if err := e.bank.SendCoinsFromModuleToModule(context.Background(), sender, "fees", sdk.NewCoins(amt)); err != nil {
		return err
	}
	cur := e.bal[addr.String()]
	if cur.IsNil() {
		cur = math.ZeroInt()
	}
	e.bal[addr.String()] = cur.Add(amt.Amount)
	return nil
}

func (e *fakeEarnings) get(addr sdk.AccAddress) math.Int {
	v := e.bal[addr.String()]
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}

type fakeDeposits struct {
	earnings *fakeEarnings
	bank     *fakeBank
	locked   map[string]math.Int
	owner    map[string]string
	released []string
	// failLock makes LockDeposit and TopUpDeposit fail.
	failLock bool
}

func (d *fakeDeposits) LockDeposit(_ context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	if d.failLock {
		return errorsmod.Wrap(sdkerrors.ErrInsufficientFunds, "deposit cannot be funded")
	}
	if !amount.IsPositive() {
		return errf("deposit amount must be positive")
	}
	if _, ok := d.locked[id]; ok {
		return errf("deposit %s exists", id)
	}
	cur := d.earnings.bal[owner.String()]
	if cur.IsNil() || cur.LT(amount) {
		return errf("insufficient earnings for deposit %s", id)
	}
	d.earnings.bal[owner.String()] = cur.Sub(amount)
	if err := d.bank.SendCoinsFromModuleToModule(context.Background(), "fees", "fees_deposits", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))); err != nil {
		return err
	}
	d.locked[id] = amount
	d.owner[id] = owner.String()
	return nil
}

func (d *fakeDeposits) TopUpDeposit(_ context.Context, id string, extra math.Int) error {
	if d.failLock {
		return errorsmod.Wrap(sdkerrors.ErrInsufficientFunds, "deposit cannot be funded")
	}
	held, ok := d.locked[id]
	if !ok {
		return errf("deposit %s missing", id)
	}
	owner := d.owner[id]
	cur := d.earnings.bal[owner]
	if cur.IsNil() || cur.LT(extra) {
		return errf("insufficient earnings for deposit %s top-up", id)
	}
	d.earnings.bal[owner] = cur.Sub(extra)
	if err := d.bank.SendCoinsFromModuleToModule(context.Background(), "fees", "fees_deposits", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, extra))); err != nil {
		return err
	}
	d.locked[id] = held.Add(extra)
	return nil
}

func (d *fakeDeposits) DepositAmount(_ context.Context, id string) (math.Int, bool, error) {
	amt, ok := d.locked[id]
	return amt, ok, nil
}

func (d *fakeDeposits) ReleaseDeposit(_ context.Context, id string) (math.Int, math.Int, error) {
	amt, ok := d.locked[id]
	if !ok {
		return math.Int{}, math.Int{}, errf("deposit %s missing", id)
	}
	owner := d.owner[id]
	if err := d.bank.SendCoinsFromModuleToModule(context.Background(), "fees_deposits", "fees", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amt))); err != nil {
		return math.Int{}, math.Int{}, err
	}
	cur := d.earnings.bal[owner]
	if cur.IsNil() {
		cur = math.ZeroInt()
	}
	d.earnings.bal[owner] = cur.Add(amt)
	delete(d.locked, id)
	d.released = append(d.released, id)
	return amt, math.ZeroInt(), nil
}

// SlashDeposit burns up to amount of the deposit and removes a deposit burned in full, like x/fees.
func (d *fakeDeposits) SlashDeposit(_ context.Context, id string, amount math.Int) (math.Int, error) {
	held, ok := d.locked[id]
	if !ok {
		return math.Int{}, errf("deposit %s missing", id)
	}
	burned := math.MinInt(amount, held)
	if err := d.bank.BurnCoins(context.Background(), "fees_deposits", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, burned))); err != nil {
		return math.Int{}, err
	}
	if burned.Equal(held) {
		delete(d.locked, id)
		delete(d.owner, id)
		return burned, nil
	}
	d.locked[id] = held.Sub(burned)
	return burned, nil
}

type fakeEmission struct {
	epoch   uint64
	ceiling map[uint64]math.Int
	bank    *fakeBank
	minted  map[uint64]math.Int
	// epochCalls counts CurrentEpoch reads.
	epochCalls int
}

// MintStorageService mirrors x/emission: it refuses a mint past the epoch's
// ceiling and credits the storage module account.
func (e *fakeEmission) MintStorageService(ctx context.Context, epoch uint64, amt math.Int) error {
	ceiling, _ := e.StorageCeiling(ctx, epoch)
	done := e.minted[epoch]
	if done.IsNil() {
		done = math.ZeroInt()
	}
	if done.Add(amt).GT(ceiling) {
		return errf("storage mint %s passes epoch %d ceiling %s", amt, epoch, ceiling)
	}
	e.minted[epoch] = done.Add(amt)
	return e.bank.MintCoins(ctx, types.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amt)))
}

func (e *fakeEmission) CurrentEpoch(context.Context) (uint64, error) {
	e.epochCalls++
	return e.epoch, nil
}

func (e *fakeEmission) StorageCeiling(_ context.Context, epoch uint64) (math.Int, error) {
	if v, ok := e.ceiling[epoch]; ok {
		return v, nil
	}
	return math.ZeroInt(), nil
}

type nodeInfo struct {
	id       string
	hot      sdk.AccAddress
	operator sdk.AccAddress
	net      string
	asn      uint32
	capacity uint64
	active   bool
	// probation marks a fee-free registration with no bond: not active, tracked as probation.
	probation bool
	slashN    int
	slashed   math.Int
	jailed    bool
	// slashErr makes Slash fail, like x/nodes refusing a slash it cannot apply.
	slashErr error
	// capacityAfterSlash, when non-zero, is the declared capacity a slash clamps the node to,
	// like x/nodes lowering the declaration to what the smaller bond backs.
	capacityAfterSlash uint64
}

type fakeNodes struct {
	byID  map[string]*nodeInfo
	dirty map[string]struct{}
}

// touch queues a node the way x/nodes does when it writes the node.
func (n *fakeNodes) touch(id string) { n.dirty[id] = struct{}{} }

func (n *fakeNodes) TakeStorageChanges(context.Context) ([]string, error) {
	ids := make([]string, 0, len(n.dirty))
	for id := range n.dirty {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	n.dirty = map[string]struct{}{}
	return ids, nil
}

func (n *fakeNodes) MarkStorageChanged(_ context.Context, id string) error {
	n.touch(id)
	return nil
}

func (n *fakeNodes) IsActive(_ context.Context, id string) (bool, error) {
	info, ok := n.byID[id]
	if !ok {
		return false, refusedf("unknown node %s", id)
	}
	return info.active && !info.jailed, nil
}

func (n *fakeNodes) IsProbation(_ context.Context, id string) (bool, error) {
	info, ok := n.byID[id]
	if !ok {
		return false, refusedf("unknown node %s", id)
	}
	return info.probation && !info.active && !info.jailed, nil
}

func (n *fakeNodes) HotKey(_ context.Context, id string) (sdk.AccAddress, error) {
	info, err := n.get(id)
	if err != nil {
		return nil, err
	}
	return info.hot, nil
}

func (n *fakeNodes) Operator(_ context.Context, id string) (string, error) {
	info, err := n.get(id)
	if err != nil {
		return "", err
	}
	return info.operator.String(), nil
}

func (n *fakeNodes) Network16(_ context.Context, id string) (string, error) {
	info, err := n.get(id)
	if err != nil {
		return "", err
	}
	return info.net, nil
}

func (n *fakeNodes) ASN(_ context.Context, id string) (uint32, error) {
	info, err := n.get(id)
	if err != nil {
		return 0, err
	}
	return info.asn, nil
}

func (n *fakeNodes) DeclaredCapacity(_ context.Context, id string) (uint64, error) {
	info, err := n.get(id)
	if err != nil {
		return 0, err
	}
	return info.capacity, nil
}

func (n *fakeNodes) Slash(_ context.Context, id string, amount math.Int) error {
	info, err := n.get(id)
	if err != nil {
		return err
	}
	if info.slashErr != nil {
		return info.slashErr
	}
	info.slashN++
	if info.capacityAfterSlash != 0 && info.capacity > info.capacityAfterSlash {
		info.capacity = info.capacityAfterSlash
	}
	if info.slashed.IsNil() {
		info.slashed = math.ZeroInt()
	}
	info.slashed = info.slashed.Add(amount)
	return nil
}

func (n *fakeNodes) Jail(_ context.Context, id string) error {
	info, err := n.get(id)
	if err != nil {
		return err
	}
	info.jailed = true
	info.active = false
	return nil
}

func (n *fakeNodes) get(id string) (*nodeInfo, error) {
	info, ok := n.byID[id]
	if !ok {
		return nil, refusedf("unknown node %s", id)
	}
	return info, nil
}

type fixture struct {
	Ctx      sdk.Context
	Keeper   keeper.Keeper
	Bank     *fakeBank
	Earnings *fakeEarnings
	Deposits *fakeDeposits
	Emission *fakeEmission
	Nodes    *fakeNodes
	Msg      types.MsgServer
	Query    types.QueryServer
	height   int64
	prev     []byte
	seq      byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeight(1).WithBlockHeader(cmtproto.Header{
		Height:      1,
		Time:        time.Unix(1_700_000_000, 0),
		LastBlockId: cmtproto.BlockID{Hash: bytesOf(32, 0x11)},
	})
	bank := newFakeBank()
	earnings := &fakeEarnings{bank: bank, bal: map[string]math.Int{}}
	deposits := &fakeDeposits{earnings: earnings, bank: bank, locked: map[string]math.Int{}, owner: map[string]string{}}
	emission := &fakeEmission{epoch: 1, ceiling: map[uint64]math.Int{}, bank: bank, minted: map[uint64]math.Int{}}
	nodes := &fakeNodes{byID: map[string]*nodeInfo{}, dirty: map[string]struct{}{}}
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), bank, earnings, deposits, emission, nodes)
	f := &fixture{
		Ctx: ctx, Keeper: k, Bank: bank, Earnings: earnings, Deposits: deposits,
		Emission: emission, Nodes: nodes, height: 1, prev: bytesOf(32, 0x11), seq: 1,
	}
	f.Msg = keeper.NewMsgServer(k)
	f.Query = keeper.NewQueryServer(k)
	return f
}

func (f *fixture) init(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

func (f *fixture) begin(t *testing.T) {
	t.Helper()
	f.height++
	f.Ctx = f.Ctx.WithBlockHeight(f.height).WithBlockHeader(cmtproto.Header{
		Height:      f.height,
		Time:        time.Unix(1_700_000_000, 0).Add(time.Duration(f.height) * time.Second),
		LastBlockId: cmtproto.BlockID{Hash: append([]byte(nil), f.prev...)},
	})
	require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
}

func (f *fixture) end(t *testing.T) {
	t.Helper()
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	sum := sha256.Sum256(append(append([]byte{}, f.prev...), byte(f.height)))
	f.prev = sum[:]
}

func (f *fixture) addNode(t *testing.T, id, net string, asn uint32, capacity uint64, probation bool) *nodeInfo {
	t.Helper()
	f.seq++
	info := &nodeInfo{
		id: id, hot: acc(f.seq), operator: acc(f.seq + 100),
		net: net, asn: asn, capacity: capacity, active: !probation, probation: probation, slashed: math.ZeroInt(),
	}
	f.Nodes.byID[id] = info
	require.NoError(t, f.Keeper.TrackNode(f.Ctx, id, probation))
	return info
}

func (f *fixture) fund(addr sdk.AccAddress, amt int64) {
	f.Bank.fund(addr.String(), math.NewInt(amt))
}

func (f *fixture) requireInvariants(t *testing.T) {
	t.Helper()
	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.Truef(t, got.EscrowConserved && got.SubsidyWithinCeiling && got.DistinctOperators && got.ReservedWithinDeclared && got.QueueWellFormed, got.Detail)
}

func payload(fill byte) []byte {
	return bytesOf(int(piece.LeafSize), fill)
}

func commit(t *testing.T, data []byte) types.PieceCommitment {
	t.Helper()
	c, err := piece.Commit(data)
	require.NoError(t, err)
	return types.PieceCommitment{
		Root: c.Root, RealLeafCount: c.RealLeafCount, PaddedLeafCount: c.PaddedLeafCount, PieceBytes: uint64(len(data)),
	}
}

func acc(b byte) sdk.AccAddress {
	return sdk.AccAddress(bytesOf(20, b))
}

func bytesOf(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// refusedf is what the app's x/nodes adapter returns for a node that is gone: an error marked as
// a failure of the one item asked about.
func refusedf(format string, args ...any) error {
	return types.Refuse(fmt.Errorf(format, args...))
}

func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func eventTypes(ctx sdk.Context) []string {
	var out []string
	for _, e := range ctx.EventManager().Events() {
		out = append(out, e.Type)
	}
	return out
}

// TestInvariantsQuery_runsPastTheQueryGasLimit pins the root cause of the fleet e2e
// "storage: query_failed": the Invariants query sums every deal, so under the node's
// query-gas-limit (2,000,000) it ran out of gas once a chain held a few hundred deals.
func TestInvariantsQuery_runsPastTheQueryGasLimit(t *testing.T) {
	const queryGasLimit = 2_000_000
	f := newFixture(t)
	f.init(t, nil)
	for id := uint64(1); id <= 3000; id++ {
		require.NoError(t, f.Keeper.Deals.Set(f.Ctx, id, types.Deal{
			Id: id, Escrow: math.ZeroInt(), Status: types.DealStatus_DEAL_STATUS_REFUNDED,
		}))
		for index := uint32(0); index < 3; index++ {
			require.NoError(t, f.Keeper.Slots.Set(f.Ctx, collections.Join(id, index), types.Slot{
				DealId: id, Index: index, NodeId: fmt.Sprintf("node-%d-%d", id, index), Operator: fmt.Sprintf("operator-%d-%d", id, index),
			}))
		}
	}

	bounded := f.Ctx.WithGasMeter(storetypes.NewGasMeter(queryGasLimit))
	got, err := f.Query.Invariants(bounded, &types.QueryInvariantsRequest{})
	require.NoError(t, err)
	require.True(t, got.EscrowConserved && got.QueueWellFormed, got.Detail)

	// The walk itself exceeds the limit: only the query's own meter lets it finish.
	over := f.Ctx.WithGasMeter(storetypes.NewGasMeter(queryGasLimit))
	require.Panics(t, func() { _, _ = f.Keeper.CheckInvariants(over) })
}
