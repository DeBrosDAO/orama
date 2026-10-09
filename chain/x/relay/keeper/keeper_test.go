package keeper_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	chainparams "github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(chainparams.Bech32Prefix, chainparams.Bech32PrefixAccPub)
}

func acc(n byte) sdk.AccAddress {
	addr := make(sdk.AccAddress, 20)
	for i := range addr {
		addr[i] = n
	}
	addr[19] = n
	return addr
}

type boundNode struct {
	pub      []byte
	operator sdk.AccAddress
	ipv4     string
}

type fakeNodes struct {
	byID map[string]boundNode
}

func newFakeNodes() *fakeNodes {
	return &fakeNodes{byID: map[string]boundNode{}}
}

func (n *fakeNodes) add(rk relayKey) {
	n.byID[rk.nodeID] = boundNode{pub: rk.pub, operator: rk.operator, ipv4: rk.ipv4}
}

func (n *fakeNodes) RelayBinding(_ context.Context, nodeID string) ([]byte, sdk.AccAddress, string, error) {
	node, ok := n.byID[nodeID]
	if !ok {
		return nil, nil, "", errNotFound(nodeID)
	}
	return append([]byte(nil), node.pub...), node.operator, node.ipv4, nil
}

type errNotFound string

func (e errNotFound) Error() string { return "node " + string(e) + " not found" }

type fakeEmission struct {
	current uint64
	ceiling map[uint64]math.Int
	minted  map[uint64]math.Int
	calls   int
}

func newFakeEmission() *fakeEmission {
	return &fakeEmission{ceiling: map[uint64]math.Int{}, minted: map[uint64]math.Int{}}
}

func (e *fakeEmission) setCeiling(epoch uint64, amount math.Int) {
	e.ceiling[epoch] = amount
}

func (e *fakeEmission) mintedOf(epoch uint64) math.Int {
	if v, ok := e.minted[epoch]; ok {
		return v
	}
	return math.ZeroInt()
}

func (e *fakeEmission) CurrentEpoch(_ context.Context) (uint64, error) {
	return e.current, nil
}

func (e *fakeEmission) RelayCeiling(_ context.Context, epoch uint64) (math.Int, error) {
	ceiling, ok := e.ceiling[epoch]
	if !ok {
		return math.Int{}, errNotFound("ceiling")
	}
	return ceiling, nil
}

func (e *fakeEmission) MintRelayReward(_ context.Context, epoch uint64, amt math.Int) error {
	if amt.IsNil() || !amt.IsPositive() {
		return errNotFound("mint amount")
	}
	ceiling, ok := e.ceiling[epoch]
	if !ok {
		return errNotFound("ceiling")
	}
	used := e.mintedOf(epoch)
	if used.Add(amt).GT(ceiling) {
		return errNotFound("ceiling exceeded")
	}
	e.minted[epoch] = used.Add(amt)
	e.calls++
	return nil
}

type fakeEarnings struct {
	balances map[string]math.Int
}

func newFakeEarnings() *fakeEarnings {
	return &fakeEarnings{balances: map[string]math.Int{}}
}

func (e *fakeEarnings) balance(addr sdk.AccAddress) math.Int {
	if v, ok := e.balances[addr.String()]; ok {
		return v
	}
	return math.ZeroInt()
}

func (e *fakeEarnings) CreditEarnings(_ context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error {
	if senderModule != types.ModuleName {
		return errNotFound("sender")
	}
	if amt.Denom != chainparams.BaseDenom || !amt.IsPositive() {
		return errNotFound("coin")
	}
	e.balances[addr.String()] = e.balance(addr).Add(amt.Amount)
	return nil
}

// fakeService is the C2 service split with the real split function, recording what is burned and
// what funds the archive.
type fakeService struct {
	burned  math.Int
	archive math.Int
}

func newFakeService() *fakeService {
	return &fakeService{burned: math.ZeroInt(), archive: math.ZeroInt()}
}

func (s *fakeService) SplitServicePayment(amount math.Int) (math.Int, math.Int, math.Int) {
	return storagetypes.SplitServicePayment(amount)
}

func (s *fakeService) BurnService(_ context.Context, senderModule string, amt math.Int) error {
	if senderModule != types.ModuleName || !amt.IsPositive() {
		return errNotFound("burn")
	}
	s.burned = s.burned.Add(amt)
	return nil
}

func (s *fakeService) FundArchive(_ context.Context, senderModule string, amt math.Int) error {
	if senderModule != types.ModuleName || !amt.IsPositive() {
		return errNotFound("archive")
	}
	s.archive = s.archive.Add(amt)
	return nil
}

type relayKey struct {
	nodeID   string
	fp       []byte
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	operator sdk.AccAddress
	ipv4     string
}

func newRelayKey(t *testing.T, id string, mark byte, operator sdk.AccAddress, ipv4 string) relayKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fp := bytes.Repeat([]byte{mark}, types.RSAFingerprintLen)
	return relayKey{nodeID: id, fp: fp, pub: pub, priv: priv, operator: operator, ipv4: ipv4}
}

type testFixture struct {
	Ctx      sdk.Context
	Keeper   keeper.Keeper
	Nodes    *fakeNodes
	Emission *fakeEmission
	Earnings *fakeEarnings
	Service  *fakeService
	Msg      types.MsgServer
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0)})

	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	nodes := newFakeNodes()
	emission := newFakeEmission()
	earnings := newFakeEarnings()
	service := newFakeService()
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), nodes, emission, earnings, service)
	return &testFixture{
		Ctx:      ctx,
		Keeper:   k,
		Nodes:    nodes,
		Emission: emission,
		Earnings: earnings,
		Service:  service,
		Msg:      keeper.NewMsgServerImpl(k),
	}
}

func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

func (f *testFixture) init(t *testing.T, quorum uint32, reporters []sdk.AccAddress, mutate func(*types.Params)) {
	t.Helper()
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params.MinReportersQuorum = quorum
		gs.Params.MinUptimeFraction = math.LegacyMustNewDecFromStr("0.8")
		gs.Params.ExitMultiplier = math.LegacyMustNewDecFromStr("2")
		gs.Params.PerRelayCap = math.NewInt(1_000_000_000)
		gs.Params.PerOperatorCap = math.NewInt(1_000_000_000)
		gs.Params.PerPrefix16Cap = math.NewInt(1_000_000_000)
		if mutate != nil {
			mutate(&gs.Params)
		}
		for _, reporter := range reporters {
			gs.Reporters = append(gs.Reporters, reporter.String())
		}
	})
}

func (f *testFixture) register(t *testing.T, rk relayKey, exit bool) {
	t.Helper()
	f.Nodes.add(rk)
	_, err := f.Msg.RegisterRelay(f.Ctx, &types.MsgRegisterRelay{
		Operator:         rk.operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   rk.fp,
		Exit:             exit,
		Ed25519Signature: ed25519.Sign(rk.priv, types.CrossCertMessage(rk.nodeID, rk.fp)),
	})
	require.NoError(t, err)
}

func obs(rk relayKey, weight int64, uptime string, exit bool) types.RelayObservation {
	flags := uint32(0)
	if exit {
		flags = types.FlagExit
	}
	return types.RelayObservation{
		RsaFingerprint:  append([]byte(nil), rk.fp...),
		Ed25519Id:       append([]byte(nil), rk.pub...),
		ConsensusWeight: math.NewInt(weight),
		Flags:           flags,
		UptimeFraction:  math.LegacyMustNewDecFromStr(uptime),
	}
}

func (f *testFixture) submit(t *testing.T, reporter sdk.AccAddress, epoch uint64, entries []types.RelayObservation) error {
	t.Helper()
	// Reports are sent for the epoch that just closed: the chain is in the next one.
	f.closeEpoch(epoch)
	return f.submitAt(t, reporter, epoch, entries)
}

// closeEpoch puts the chain in the epoch after epoch, with a ceiling recorded
// for epoch unless the test already set one.
func (f *testFixture) closeEpoch(epoch uint64) {
	f.Emission.current = epoch + 1
	if _, ok := f.Emission.ceiling[epoch]; !ok {
		f.Emission.setCeiling(epoch, math.ZeroInt())
	}
}

// submitAt sends a report without moving the chain's current epoch.
func (f *testFixture) submitAt(t *testing.T, reporter sdk.AccAddress, epoch uint64, entries []types.RelayObservation) error {
	t.Helper()
	root, err := types.InputsRoot(entries)
	require.NoError(t, err)
	_, err = f.Msg.ReportEpoch(f.Ctx, &types.MsgReportEpoch{
		Reporter:   reporter.String(),
		Epoch:      epoch,
		ChunkIndex: 0,
		ChunkCount: 1,
		Entries:    entries,
		InputsRoot: root,
	})
	return err
}

func (f *testFixture) activate(t *testing.T, reporters []sdk.AccAddress, entries []types.RelayObservation) {
	t.Helper()
	for _, reporter := range reporters {
		require.NoError(t, f.submit(t, reporter, 1, entries))
	}
	result, err := f.Keeper.SettleEpoch(f.Ctx, 1)
	require.NoError(t, err)
	require.True(t, result.QuorumMet)
	require.True(t, result.Activating)
	require.True(t, result.Minted.IsZero())
	require.True(t, f.Emission.mintedOf(1).IsZero())
}

func (f *testFixture) payouts(t *testing.T, epoch uint64) []types.RelayPayout {
	t.Helper()
	var out []types.RelayPayout
	err := f.Keeper.Payouts.Walk(f.Ctx, collections.NewPrefixedPairRange[uint64, []byte](epoch), func(_ collections.Pair[uint64, []byte], payout types.RelayPayout) (bool, error) {
		out = append(out, payout)
		return false, nil
	})
	require.NoError(t, err)
	return out
}

func payoutFor(payouts []types.RelayPayout, fp []byte) math.Int {
	for _, payout := range payouts {
		if bytes.Equal(payout.RsaFingerprint, fp) {
			return payout.Amount
		}
	}
	return math.ZeroInt()
}
