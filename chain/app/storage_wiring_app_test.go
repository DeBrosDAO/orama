package app_test

import (
	stded25519 "crypto/ed25519"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const wiringCapacity = uint64(1 << 20)

// wiringChain is a real app driven through FinalizeBlock. State changes that a test makes through
// the keepers land in a block context that is committed as its own block, exactly as the funding
// step of TestApp_operatorBondsNodeFromEarnings does.
type wiringChain struct {
	t       *testing.T
	app     *app.OramaApp
	genesis time.Time
	height  int64
}

func newWiringChain(t *testing.T) *wiringChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genesis := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	// The network identity lock has its own tests; here a node's identity counts at once.
	var nodesGen nodestypes.GenesisState
	oramaApp.AppCodec().MustUnmarshalJSON(genState[nodestypes.ModuleName], &nodesGen)
	nodesGen.Params.NetworkIdentityLockSeconds = 0
	genState[nodestypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(&nodesGen)
	initChain(t, oramaApp, genState, 0, genesis)
	finalize(t, oramaApp, 1, genesis.Add(2*time.Second))
	return &wiringChain{t: t, app: oramaApp, genesis: genesis, height: 1}
}

func (c *wiringChain) at(height int64) time.Time {
	return c.genesis.Add(time.Duration(height) * 2 * time.Second)
}

// write runs fn in the next block's context and commits it as that block.
func (c *wiringChain) write(fn func(ctx sdk.Context)) {
	c.t.Helper()
	c.height++
	ctx := c.app.NewNextBlockContext(cmtproto.Header{Height: c.height, Time: c.at(c.height), ChainID: testChainID})
	fn(ctx)
	writeCache(c.t, ctx)
	_, err := c.app.Commit()
	require.NoError(c.t, err)
}

// blocks finalizes n empty blocks, running every module's BeginBlock and EndBlock.
func (c *wiringChain) blocks(n int) {
	c.t.Helper()
	for i := 0; i < n; i++ {
		c.height++
		finalize(c.t, c.app, c.height, c.at(c.height))
	}
}

type wiringNode struct {
	id       string
	operator sdk.AccAddress
	hot      sdk.AccAddress
	endpoint string
	asn      uint32
}

// addStorageNode registers an operator and a STORAGE node, bonds the minimum and declares capacity.
func (c *wiringChain) addStorageNode(id, endpoint string, asn uint32) wiringNode {
	c.t.Helper()
	n := wiringNode{
		id:       id,
		operator: sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()),
		endpoint: endpoint,
		asn:      asn,
	}
	hotPriv := secp256k1.GenPrivKey()
	n.hot = sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, n.operator.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(c.t, err)
	hotBinding := nodestypes.Binding{
		Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
	}
	pub, priv, err := stded25519.GenerateKey(nil)
	require.NoError(c.t, err)
	binding := nodestypes.Binding{
		Service: "ipfs", KeyType: nodestypes.KeyTypeEd25519, Pubkey: pub,
		Signature: stded25519.Sign(priv, nodestypes.BindingSignBytes(testChainID, n.operator.String(), "ipfs", pub)),
	}
	c.write(func(ctx sdk.Context) {
		funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(params.NoramaPerOrama).MulRaw(20)))
		require.NoError(c.t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, funds))
		require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, n.operator, funds))
		srv := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper)
		_, err := srv.RegisterOperator(ctx, &nodestypes.MsgRegisterOperator{Operator: n.operator.String()})
		require.NoError(c.t, err)
		_, err = srv.RegisterNode(ctx, &nodestypes.MsgRegisterNode{
			Operator: n.operator.String(), NodeId: id, Roles: []nodestypes.Role{nodestypes.RoleStorage},
			HotKey: n.hot.String(), Bindings: []nodestypes.Binding{binding, hotBinding},
			Endpoints: []string{endpoint}, Asn: asn,
		})
		require.NoError(c.t, err)
		_, err = srv.BondNode(ctx, &nodestypes.MsgBondNode{
			Operator: n.operator.String(), NodeId: id, Role: nodestypes.RoleStorage, Amount: math.NewInt(params.NoramaPerOrama),
		})
		require.NoError(c.t, err)
		_, err = srv.DeclareCapacity(ctx, &nodestypes.MsgDeclareCapacity{
			Operator: n.operator.String(), NodeId: id, CapacityBytes: wiringCapacity,
		})
		require.NoError(c.t, err)
	})
	return n
}

func (c *wiringChain) tracked(id string) bool {
	c.t.Helper()
	has, err := c.app.StorageKeeper.Nodes.Has(c.app.NewContext(true), id)
	require.NoError(c.t, err)
	return has
}

func (c *wiringChain) privateDeal() uint64 {
	c.t.Helper()
	client := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	var id uint64
	c.write(func(ctx sdk.Context) {
		credit := math.NewInt(params.NoramaPerOrama).MulRaw(10)
		funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))
		require.NoError(c.t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, funds))
		require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, client, funds))
		pieces := make([]storagetypes.PieceCommitment, 3)
		for i := range pieces {
			data := make([]byte, piece.LeafSize)
			data[0] = byte(i + 1)
			cm, err := piece.Commit(data)
			require.NoError(c.t, err)
			pieces[i] = storagetypes.PieceCommitment{
				Root: cm.Root, RealLeafCount: cm.RealLeafCount, PaddedLeafCount: cm.PaddedLeafCount, PieceBytes: uint64(len(data)),
			}
		}
		nonce := make([]byte, storagetypes.NonceLen)
		nonce[0] = 7
		res, err := storagekeeper.NewMsgServer(c.app.StorageKeeper).CreateDeal(ctx, &storagetypes.MsgCreateDeal{
			Signer: client.String(), Class: storagetypes.DealClass_DEAL_CLASS_PRIVATE, DealNonce: nonce,
			Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 100, Pieces: pieces,
		})
		require.NoError(c.t, err)
		id = res.DealId
	})
	return id
}

func (c *wiringChain) slotHolders(dealID uint64) []string {
	c.t.Helper()
	var out []string
	for i := uint32(0); i < 3; i++ {
		slot, err := c.app.StorageKeeper.Slots.Get(c.app.NewContext(true), collectionsJoin(dealID, i))
		require.NoError(c.t, err)
		out = append(out, slot.NodeId)
	}
	return out
}

func TestApp_registeredAndBondedStorageNodesGetSlots(t *testing.T) {
	c := newWiringChain(t)
	c.addStorageNode("s1", "https://198.51.100.10:443", 15169)
	c.addStorageNode("s2", "https://203.0.113.10:443", 13335)
	c.addStorageNode("s3", "https://192.0.2.10:443", 16509)
	require.False(t, c.tracked("s1"), "x/storage has not seen the nodes before a block runs")

	c.blocks(1)
	for _, id := range []string{"s1", "s2", "s3"} {
		require.True(t, c.tracked(id), "%s is tracked after the next BeginBlock", id)
	}

	dealID := c.privateDeal()
	c.blocks(1)
	holders := c.slotHolders(dealID)
	require.ElementsMatch(t, []string{"s1", "s2", "s3"}, holders, "the private deal's slots go to the three storage nodes")
}

func TestApp_storageNodeThatRetiresOrUnbondsIsUntracked(t *testing.T) {
	c := newWiringChain(t)
	retiring := c.addStorageNode("retire-me", "https://198.51.100.10:443", 15169)
	shrinking := c.addStorageNode("unbond-me", "https://203.0.113.10:443", 13335)
	c.addStorageNode("stays", "https://192.0.2.10:443", 16509)
	c.blocks(1)
	for _, id := range []string{"retire-me", "unbond-me", "stays"} {
		require.True(t, c.tracked(id))
	}

	c.write(func(ctx sdk.Context) {
		srv := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper)
		_, err := srv.RetireNode(ctx, &nodestypes.MsgRetireNode{Operator: retiring.operator.String(), NodeId: "retire-me"})
		require.NoError(t, err)
		_, err = srv.DeclareCapacity(ctx, &nodestypes.MsgDeclareCapacity{Operator: shrinking.operator.String(), NodeId: "unbond-me"})
		require.NoError(t, err)
		_, err = srv.UnbondNode(ctx, &nodestypes.MsgUnbondNode{
			Operator: shrinking.operator.String(), NodeId: "unbond-me", Role: nodestypes.RoleStorage, Amount: math.NewInt(params.NoramaPerOrama),
		})
		require.NoError(t, err)
	})
	c.blocks(1)

	require.False(t, c.tracked("retire-me"), "a retired node is untracked")
	require.False(t, c.tracked("unbond-me"), "a node unbonded below the role bond is untracked")
	require.True(t, c.tracked("stays"))
}

func TestApp_jailedStorageNodeIsUntrackedAndReplacedInNewDeals(t *testing.T) {
	c := newWiringChain(t)
	c.addStorageNode("s1", "https://198.51.100.10:443", 15169)
	c.addStorageNode("s2", "https://203.0.113.10:443", 13335)
	c.addStorageNode("s3", "https://192.0.2.10:443", 16509)
	c.blocks(1)

	c.write(func(ctx sdk.Context) { require.NoError(t, c.app.NodesKeeper.Jail(ctx, "s2")) })
	c.blocks(1)
	require.False(t, c.tracked("s2"))

	dealID := c.privateDeal()
	c.blocks(1)
	for _, holder := range c.slotHolders(dealID) {
		require.NotEqual(t, "s2", holder)
	}
}

func TestApp_protocolDealSlotsNeedDistinctNetworksFromTheirEndpoints(t *testing.T) {
	c := newWiringChain(t)
	c.addStorageNode("a", "https://198.51.100.10:443", 15169)
	c.addStorageNode("b", "https://198.51.7.7:443", 13335) // same /16 as a
	c.addStorageNode("c", "https://203.0.113.10:443", 16509)
	c.blocks(1)

	var refused uint64
	c.write(func(ctx sdk.Context) {
		id, err := c.app.StorageKeeper.CreateProtocolDeal(ctx, storagetypes.DealClass_DEAL_CLASS_ARCHIVE, protocolPayload(1), math.NewInt(1000), 100)
		require.NoError(t, err)
		refused = id
	})
	c.blocks(1)
	deal, err := c.app.StorageKeeper.Deals.Get(c.app.NewContext(true), refused)
	require.NoError(t, err)
	require.Equal(t, storagetypes.DealStatus_DEAL_STATUS_REFUNDED, deal.Status,
		"a and b share a /16, so only two distinct networks exist for three slots")

	c.addStorageNode("d", "https://192.0.2.10:443", 20940)
	c.blocks(1)
	var placed uint64
	c.write(func(ctx sdk.Context) {
		id, err := c.app.StorageKeeper.CreateProtocolDeal(ctx, storagetypes.DealClass_DEAL_CLASS_ARCHIVE, protocolPayload(2), math.NewInt(1000), 100)
		require.NoError(t, err)
		placed = id
	})
	c.blocks(1)
	nets := map[string]struct{}{}
	asns := map[uint32]struct{}{}
	for i := uint32(0); i < 3; i++ {
		slot, err := c.app.StorageKeeper.Slots.Get(c.app.NewContext(true), collectionsJoin(placed, i))
		require.NoError(t, err)
		require.NotEmpty(t, slot.NodeId)
		require.NotEmpty(t, slot.Network16)
		nets[slot.Network16] = struct{}{}
		asns[slot.Asn] = struct{}{}
	}
	require.Len(t, nets, 3)
	require.Len(t, asns, 3)
}

func protocolPayload(fill byte) []byte {
	data := make([]byte, piece.LeafSize)
	data[0] = fill
	return data
}

func collectionsJoin(dealID uint64, slot uint32) collections.Pair[uint64, uint32] {
	return collections.Join(dealID, slot)
}

func TestApp_operatorFundsOwnNodesHotKeyFromEarnings(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	opKey := ed25519.GenPrivKey()
	op := sdk.AccAddress(opKey.PubKey().Address())
	addAuthAccount(t, oramaApp, genState, opKey)
	initChain(t, oramaApp, genState, 2_000_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	credit := math.NewInt(2_000_000_000)
	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, op, sdk.NewCoin(params.BaseDenom, credit)))
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)

	torPub, torPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)
	hotPriv := secp256k1.GenPrivKey()
	hot := sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, op.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(t, err)
	gas := uint64(300_000)
	register := signedTx(t, oramaApp, opKey, 3, gas,
		&nodestypes.MsgRegisterOperator{Operator: op.String()},
		&nodestypes.MsgRegisterNode{
			Operator: op.String(), NodeId: "node-1", Roles: []nodestypes.Role{nodestypes.RoleRelay}, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: torPub,
				Signature: stded25519.Sign(torPriv, nodestypes.BindingSignBytes(testChainID, op.String(), "tor", torPub)),
			}, {
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}},
			Endpoints: []string{"https://node.example:443"},
		})
	resp := finalize(t, oramaApp, 3, genesisTime.Add(6*time.Second), register)
	require.Zero(t, resp.TxResults[0].Code, "register failed: %s", resp.TxResults[0].Log)

	before, err := oramaApp.FeesKeeper.GetEarnings(oramaApp.NewContext(true), op)
	require.NoError(t, err)
	amount := math.NewInt(500_000_000)
	fund := signedTx(t, oramaApp, opKey, 4, gas, &nodestypes.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: amount})
	resp = finalize(t, oramaApp, 4, genesisTime.Add(8*time.Second), fund)
	require.Zero(t, resp.TxResults[0].Code, "fund hot key failed: %s", resp.TxResults[0].Log)

	ctx := oramaApp.NewContext(true)
	hotFee, err := oramaApp.FeesKeeper.GetFeeBalance(ctx, hot)
	require.NoError(t, err)
	require.True(t, hotFee.Equal(amount), "hot key fee balance = %s, want %s", hotFee, amount)
	hotEarnings, err := oramaApp.FeesKeeper.GetEarnings(ctx, hot)
	require.NoError(t, err)
	require.True(t, hotEarnings.IsZero(), "the hot key gets no earnings it could bond or shield")
	after, err := oramaApp.FeesKeeper.GetEarnings(ctx, op)
	require.NoError(t, err)
	require.True(t, before.Sub(after).GTE(amount), "operator earnings fell by %s, want at least %s", before.Sub(after), amount)
	require.True(t, oramaApp.BankKeeper.GetBalance(ctx, hot, params.BaseDenom).Amount.IsZero(), "the hot key gets a fee balance, not a public one")

	feesInv, err := oramaApp.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)

	ghost := signedTx(t, oramaApp, opKey, 5, gas, &nodestypes.MsgFundHotKey{Operator: op.String(), NodeId: "ghost", Amount: amount})
	resp = finalize(t, oramaApp, 5, genesisTime.Add(10*time.Second), ghost)
	require.NotZero(t, resp.TxResults[0].Code, "funding a node that does not exist must fail")
	stillHot, err := oramaApp.FeesKeeper.GetFeeBalance(oramaApp.NewContext(true), hot)
	require.NoError(t, err)
	require.True(t, stillHot.Equal(amount))
}

func TestApp_bondNodeForAnUnknownNodeDoesNotMoveEarningsToTheBankBalance(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	opKey := ed25519.GenPrivKey()
	op := sdk.AccAddress(opKey.PubKey().Address())
	addAuthAccount(t, oramaApp, genState, opKey)
	initChain(t, oramaApp, genState, 2_000_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	credit := math.NewInt(2_000_000_000)
	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, op, sdk.NewCoin(params.BaseDenom, credit)))
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)

	bond := signedTx(t, oramaApp, opKey, 3, 300_000, &nodestypes.MsgBondNode{
		Operator: op.String(), NodeId: "ghost", Role: nodestypes.RoleRelay, Amount: math.NewInt(1_000_000_000),
	})
	resp := finalize(t, oramaApp, 3, genesisTime.Add(6*time.Second), bond)
	require.NotZero(t, resp.TxResults[0].Code, "bonding a node that does not exist must fail")

	ctx := oramaApp.NewContext(true)
	require.True(t, oramaApp.BankKeeper.GetBalance(ctx, op, params.BaseDenom).Amount.IsZero(),
		"a failed bond must not leave the operator's earnings sitting in its bank balance")
	left, err := oramaApp.FeesKeeper.GetEarnings(ctx, op)
	require.NoError(t, err)
	require.True(t, credit.Sub(left).LT(math.NewInt(200_000_000)), "only the fee left the earnings, not the bond amount")
}
