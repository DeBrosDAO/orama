package app_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	wasmpolicytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// enactmentApp is a running test chain, one epoch closed, with the height-1 block committed.
type enactmentApp struct {
	app     *app.OramaApp
	genesis time.Time
}

func newEnactmentApp(t *testing.T, mutate func(app.GenesisState)) enactmentApp {
	t.Helper()
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	if mutate != nil {
		mutate(genState)
	}
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))
	return enactmentApp{app: oramaApp, genesis: genesisTime}
}

func (e enactmentApp) ctxAt(height int64, after time.Duration) sdk.Context {
	return e.app.NewNextBlockContext(cmtproto.Header{Height: height, Time: e.genesis.Add(after)})
}

// openStructuralTier writes the operator house the structural tier needs besides lambda: 21 members
// with the minimum service days, each holding a house bond, spread over 7 /16 networks and 5 ASNs.
func openStructuralTier(t *testing.T, ctx sdk.Context, a *app.OramaApp) {
	t.Helper()
	for i := 0; i < 21; i++ {
		addr := sdk.AccAddress(bytes.Repeat([]byte{byte(i + 1)}, 20)).String()
		canonical, err := nodestypes.CanonicalAddress(addr)
		require.NoError(t, err)
		require.NoError(t, a.NodesKeeper.Operators.Set(ctx, addr, nodestypes.Operator{Address: addr}))
		node := nodestypes.Node{
			NodeId: fmt.Sprintf("tier-%d", i), Operator: addr, Status: nodestypes.NodeStatusActive,
			Asn: uint32(i%5 + 1), Endpoints: []string{fmt.Sprintf("https://%d.%d.0.1:443", 40+i/3, 100+i/3)},
		}
		require.NoError(t, a.NodesKeeper.Nodes.Set(ctx, node.NodeId, node))
		require.NoError(t, a.NodesKeeper.NodesByOperator.Set(ctx, collections.Join(addr, node.NodeId)))
		for day := uint64(0); day < housetypes.MinServiceDays; day++ {
			require.NoError(t, a.NodesKeeper.ServiceDays.Set(ctx, collections.Join(canonical, day), nodestypes.ServiceDay{Operator: canonical, DayIndex: day}))
		}
		require.NoError(t, a.HousesKeeper.Bonds.Set(ctx, addr, housetypes.HouseBond{
			Address: addr, Amount: housetypes.DefaultHouseBond(), LockedAtHeight: 1,
		}))
	}
}

// enact runs ExecuteProposal with lambda at 1, the last condition of the structural tier that a
// structural proposal is checked against when its timelock ends, and puts lambda back after: lambda at
// 1 would change how the chain under test distributes rewards.
func enact(ctx sdk.Context, a *app.OramaApp, id uint64) error {
	lambda, err := a.PowerKeeper.Lambda.Get(ctx)
	if err != nil {
		return err
	}
	if err := a.PowerKeeper.Lambda.Set(ctx, math.LegacyOneDec()); err != nil {
		return err
	}
	execErr := a.HousesKeeper.ExecuteProposal(ctx, id)
	if err := a.PowerKeeper.Lambda.Set(ctx, lambda); err != nil {
		return err
	}
	return execErr
}

// timelockedProposal stores a proposal whose vote and timelock are done, so ExecuteProposal (through enact) is
// the real enactment path. Votes and timelock timing are covered in x/houses; the structural tier
// the proposal needs is opened here.
func timelockedProposal(t *testing.T, ctx sdk.Context, a *app.OramaApp, id uint64, content housetypes.ProposalContent) {
	t.Helper()
	openStructuralTier(t, ctx, a)
	proposer := sdk.AccAddress(bytes.Repeat([]byte{9}, 20)).String()
	ended := ctx.BlockTime().Add(-time.Hour).UnixNano()
	require.NoError(t, a.HousesKeeper.Proposals.Set(ctx, id, housetypes.Proposal{
		Id: id, Proposer: proposer, SubmitUnixNano: ended - 1, VotingEndUnixNano: ended,
		TimelockEndUnixNano: ended, Status: housetypes.ProposalStatus_TIMELOCK, Content: content,
		TokenYes: math.ZeroInt(), TokenNo: math.ZeroInt(), TokenAbstain: math.ZeroInt(),
	}))
	require.NoError(t, a.HousesKeeper.Active.Set(ctx, id))
}

func TestEnactment_emissionClosesEpochsAtTheEnactedSplit(t *testing.T) {
	e := newEnactmentApp(t, nil)
	ctx := e.ctxAt(2, 4*time.Second)

	first, err := e.app.EmissionKeeper.Ceilings.Get(ctx, 1)
	require.NoError(t, err)
	require.True(t, first.Percents().IsCanonical())

	timelockedProposal(t, ctx, e.app, 1, housetypes.ProposalContent{EmissionSplit: &housetypes.EmissionSplitChange{
		ValidatorPercent: 70, StoragePercent: 15, RelayPercent: 10, DevelopmentPercent: 5,
	}})
	require.NoError(t, enact(ctx, e.app, 1))
	require.NoError(t, e.app.EmissionKeeper.AdvanceBlock(ctx))

	second, err := e.app.EmissionKeeper.Ceilings.Get(ctx, 2)
	require.NoError(t, err)
	max := emissiontypes.MaxMintableForEpoch(2)
	require.Equal(t, uint32(70), second.ValidatorPercent)
	require.True(t, second.ValidatorMinted.Equal(max.MulRaw(70).QuoRaw(100)), second.ValidatorMinted)
	require.True(t, second.StorageCeiling.Equal(max.MulRaw(15).QuoRaw(100)), second.StorageCeiling)
	require.True(t, first.ValidatorMinted.LT(second.ValidatorMinted), "epoch 1 closed before the split was enacted")

	_, broken := e.app.EmissionKeeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

func TestEnactment_relayReportersChangeThroughRelaysOwnPath(t *testing.T) {
	e := newEnactmentApp(t, nil)
	ctx := e.ctxAt(2, 4*time.Second)
	old := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20)).String()
	added := sdk.AccAddress(bytes.Repeat([]byte{0x32}, 20)).String()
	require.NoError(t, e.app.RelayKeeper.Reporters.Set(ctx, old, true))
	keep := sdk.AccAddress(bytes.Repeat([]byte{0x33}, 20)).String()
	require.NoError(t, e.app.RelayKeeper.Reporters.Set(ctx, keep, true))

	timelockedProposal(t, ctx, e.app, 1, housetypes.ProposalContent{RelayReporters: &housetypes.RelayReporterChange{
		Add: []string{added}, Remove: []string{old},
	}})
	require.NoError(t, enact(ctx, e.app, 1))

	got := map[string]bool{}
	require.NoError(t, e.app.RelayKeeper.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		got[addr] = true
		return false, nil
	}))
	require.Equal(t, map[string]bool{added: true, keep: true}, got)
	p, err := e.app.HousesKeeper.Proposals.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, housetypes.ProposalStatus_EXECUTED, p.Status, p.FailReason)
}

func TestEnactment_relayReporterChangeThatEmptiesTheSetFails(t *testing.T) {
	e := newEnactmentApp(t, nil)
	ctx := e.ctxAt(2, 4*time.Second)
	only := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20)).String()
	require.NoError(t, e.app.RelayKeeper.Reporters.Set(ctx, only, true))

	timelockedProposal(t, ctx, e.app, 1, housetypes.ProposalContent{RelayReporters: &housetypes.RelayReporterChange{Remove: []string{only}}})
	require.NoError(t, enact(ctx, e.app, 1))

	p, err := e.app.HousesKeeper.Proposals.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, housetypes.ProposalStatus_FAILED, p.Status)
	has, err := e.app.RelayKeeper.Reporters.Has(ctx, only)
	require.NoError(t, err)
	require.True(t, has, "a refused change must leave the reporter set as it was")
}

func TestEnactment_softwareUpgradeBecomesAnUpgradePlan(t *testing.T) {
	e := newEnactmentApp(t, nil)
	ctx := e.ctxAt(2, 4*time.Second)
	_, err := e.app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.Error(t, err, "no plan exists before the proposal executes")

	timelockedProposal(t, ctx, e.app, 1, housetypes.ProposalContent{SoftwareUpgrade: &housetypes.SoftwareUpgrade{Name: "v2", Height: 1_000, Info: upgradeInfo()}})
	require.NoError(t, enact(ctx, e.app, 1))

	plan, err := e.app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2", plan.Name)
	require.Equal(t, int64(1_000), plan.Height)
	require.Equal(t, upgradeInfo(), plan.Info, "the plan carries the checksums the proposal named")
}

// wasmStoreMsg has the shape of wasmd's MsgStoreCode as far as x/wasmpolicy reads it.
type wasmStoreMsg struct{ code []byte }

func (wasmStoreMsg) Reset()                       {}
func (wasmStoreMsg) String() string               { return "store" }
func (wasmStoreMsg) ProtoMessage()                {}
func (wasmStoreMsg) XXX_MessageName() string      { return "cosmwasm.wasm.v1.MsgStoreCode" }
func (m wasmStoreMsg) GetWASMByteCode() []byte    { return m.code }
func (m wasmStoreMsg) sha256Hex() string          { s := sha256.Sum256(m.code); return hex.EncodeToString(s[:]) }
func gzipped(t *testing.T, b []byte) wasmStoreMsg { return wasmStoreMsg{code: gz(t, b)} }

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestEnactment_uploadAllowListOpensOneCodeHashBeforeTheSunset(t *testing.T) {
	e := newEnactmentApp(t, nil)
	ctx := e.ctxAt(2, 4*time.Second)
	wasm := []byte("\x00asm\x01\x00\x00\x00 an approved contract")
	approved := wasmStoreMsg{code: wasm}
	other := wasmStoreMsg{code: []byte("\x00asm\x01\x00\x00\x00 another contract")}
	const height = 100

	require.ErrorIs(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, approved), wasmpolicytypes.ErrUploadClosed)

	timelockedProposal(t, ctx, e.app, 1, housetypes.ProposalContent{AllowList: &housetypes.AllowListChange{
		CodeUploadAdd: []string{approved.sha256Hex()},
	}})
	require.NoError(t, enact(ctx, e.app, 1))

	require.NoError(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, approved), "the enacted hash is allowed")
	require.NoError(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, gzipped(t, wasm)), "a gzip upload is hashed uncompressed")
	require.ErrorIs(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, other), wasmpolicytypes.ErrUploadClosed, "any other hash stays closed")
	require.ErrorIs(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, wasmStoreMsg{}), wasmpolicytypes.ErrUploadClosed, "an empty upload has no hash")

	timelockedProposal(t, ctx, e.app, 2, housetypes.ProposalContent{AllowList: &housetypes.AllowListChange{
		CodeUploadRemove: []string{approved.sha256Hex()},
	}})
	require.NoError(t, enact(ctx, e.app, 2))
	require.ErrorIs(t, e.app.WasmPolicyKeeper.CheckMsg(ctx, height, approved), wasmpolicytypes.ErrUploadClosed, "removal closes it again")
}

// upgradeInfo is a valid SoftwareUpgrade.Info naming one binary and one verifier checksum.
func upgradeInfo() string {
	return `{"binaries":{"linux/amd64":"https://releases.example/oramad?checksum=sha256:` + strings.Repeat("a", 64) +
		`"},"verifiers":{"linux/amd64":"https://releases.example/orama-orchard-verifier?checksum=sha256:` + strings.Repeat("b", 64) + `"}}`
}
