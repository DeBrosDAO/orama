//go:build cgo && !nowasm

package app_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
)

// bindingChain is a chain with three funded users and a relay contract holding 100 ORAMA.
type bindingChain struct {
	*wasmChain
	alice, bob, carol wasmUser
	relay             sdk.AccAddress
}

func newBindingChain(t *testing.T) *bindingChain {
	t.Helper()
	c := newWasmChain(t, wasmChainOptions{users: 3})
	id := c.storeRelay()
	relay := c.instantiate(c.users[0], id, map[string]any{}, nil)
	c.fund(relay, 100*params.NoramaPerOrama)
	return &bindingChain{wasmChain: c, alice: c.users[0], bob: c.users[1], carol: c.users[2], relay: relay}
}

// custom wraps a binding message as the relay's Dispatch of one CosmosMsg::Custom.
func custom(msg any) map[string]any {
	return map[string]any{"dispatch": map[string]any{"msgs": []any{map[string]any{"custom": msg}}}}
}

// bind sends msg through the relay as user and requires the transaction to succeed.
func (b *bindingChain) bind(user wasmUser, msg any) {
	b.t.Helper()
	res := b.exec(user, b.relay, custom(msg), nil)
	require.Zero(b.t, res.Code, "binding failed: %s", res.Log)
}

// bindFails sends msg through the relay and requires a failure whose log mentions want.
func (b *bindingChain) bindFails(user wasmUser, msg any, want string) {
	b.t.Helper()
	res := b.exec(user, b.relay, custom(msg), nil)
	require.NotZero(b.t, res.Code, "binding unexpectedly succeeded")
	require.Contains(b.t, res.Log, want)
}

// ask runs a custom query through the relay and returns the answer.
func (b *bindingChain) ask(query any) ([]byte, error) {
	b.t.Helper()
	raw, err := json.Marshal(map[string]any{"forward": map[string]any{"request": map[string]any{"custom": query}}})
	require.NoError(b.t, err)
	return b.app.WasmKeeper().QuerySmart(b.ctx(), b.relay, raw)
}

func (b *bindingChain) mustAsk(query any) []byte {
	b.t.Helper()
	out, err := b.ask(query)
	require.NoError(b.t, err)
	return out
}

func TestBindings_tokenCreateMintBurnAndItsDenials(t *testing.T) {
	b := newBindingChain(t)
	denom := tokentypes.Denom(b.relay.String(), "gold")

	b.bind(b.alice, map[string]any{"token": map[string]any{"create": map[string]any{
		"subdenom": "gold", "name": "Gold", "symbol": "GLD", "description": "d", "mint": true,
	}}})
	var info tokentypes.Token
	require.NoError(t, json.Unmarshal(b.mustAsk(map[string]any{"token": map[string]any{"info": map[string]any{"denom": denom}}}), &info))
	require.Equal(t, b.relay.String(), info.Creator, "the contract administers the token")

	b.bind(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": denom, "recipient": b.bob.addr.String(), "amount": "500"}}})
	b.bind(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": denom, "recipient": b.relay.String(), "amount": "100"}}})
	require.True(t, b.balance(b.bob.addr, denom).Equal(math.NewInt(500)))

	b.bind(b.alice, map[string]any{"token": map[string]any{"burn": map[string]any{"denom": denom, "amount": "40"}}})
	require.True(t, b.balance(b.relay, denom).Equal(math.NewInt(60)))

	// Denials. Nobody but the contract holds its mint authority.
	res := b.deliver(b.alice, &tokentypes.MsgMint{Sender: b.alice.addr.String(), Denom: denom, Recipient: b.alice.addr.String(), Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "a user cannot mint the contract's token")

	b.mustDeliver(b.alice, &tokentypes.MsgCreateToken{Creator: b.alice.addr.String(), Subdenom: "silver", Name: "Silver", Symbol: "SLV", Description: "d", Mint: true})
	silver := tokentypes.Denom(b.alice.addr.String(), "silver")
	b.bindFails(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": silver, "recipient": b.relay.String(), "amount": "1"}}}, "mint authority")

	b.bindFails(b.alice, map[string]any{"token": map[string]any{"burn": map[string]any{"denom": denom, "amount": "1000"}}}, "exceeds issued supply")
}

// TestBindings_tokenBindingRefusesToWrapNorama covers the wrapper rule: a contract cannot create or
// administer a norama-named token, and cannot touch norama through the token binding.
func TestBindings_tokenBindingRefusesToWrapNorama(t *testing.T) {
	b := newBindingChain(t)
	b.bindFails(b.alice, map[string]any{"token": map[string]any{"create": map[string]any{"subdenom": "norama", "name": "N", "symbol": "N", "description": "d", "mint": true}}}, "cannot create or hold norama")
	b.bindFails(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": params.BaseDenom, "recipient": b.bob.addr.String(), "amount": "1"}}}, "cannot create or hold norama")
	b.bindFails(b.alice, map[string]any{"token": map[string]any{"burn": map[string]any{"denom": params.BaseDenom, "amount": "1"}}}, "cannot create or hold norama")
}

// TestBindings_aContractCanIssueAPublicIOUForOramaItHolds is the declared limit O-B
// (plans/open-network.md D7, docs/whitepaper/technical-reference/vol2/44-governance-and-contracts.md): a contract that holds ORAMA can mint its own
// publicly transferable token against it. The chain does not stop this; a token named norama is
// the only wrapper it refuses.
func TestBindings_aContractCanIssueAPublicIOUForOramaItHolds(t *testing.T) {
	b := newBindingChain(t)
	held := b.balance(b.relay, params.BaseDenom)
	require.True(t, held.IsPositive())

	b.bind(b.alice, map[string]any{"token": map[string]any{"create": map[string]any{"subdenom": "wnorama", "name": "Wrapped ORAMA", "symbol": "WORAMA", "description": "an IOU", "mint": true}}})
	iou := tokentypes.Denom(b.relay.String(), "wnorama")
	b.bind(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": iou, "recipient": b.bob.addr.String(), "amount": "1000000000"}}})
	b.mustDeliver(b.bob, &tokentypes.MsgTransfer{Sender: b.bob.addr.String(), From: b.bob.addr.String(), To: b.carol.addr.String(), Denom: iou, Amount: math.NewInt(400_000_000)})
	require.True(t, b.balance(b.carol.addr, iou).Equal(math.NewInt(400_000_000)), "the IOU moves between users publicly")
}

func TestBindings_earningsPayLandsInEarningsAndABankSendInTheBalance(t *testing.T) {
	b := newBindingChain(t)
	pay := int64(5 * params.NoramaPerOrama)
	before, err := b.app.FeesKeeper.GetEarnings(b.ctx(), b.carol.addr)
	require.NoError(t, err)

	b.bind(b.alice, map[string]any{"earnings": map[string]any{"pay": map[string]any{"recipient": b.carol.addr.String(), "amount": "5000000000"}}})

	after, err := b.app.FeesKeeper.GetEarnings(b.ctx(), b.carol.addr)
	require.NoError(t, err)
	require.True(t, after.Sub(before).Equal(math.NewInt(pay)), "the payment lands in the user's earnings")
	require.True(t, b.balance(b.relay, params.BaseDenom).Equal(math.NewInt(95*params.NoramaPerOrama)))
	inv, err := b.app.FeesKeeper.CheckInvariants(b.ctx())
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule && inv.DepositsMatchModule, inv.Detail)

	// A bank send pays the user's public balance instead, and leaves their earnings alone. More than
	// the contract holds is refused.
	balanceBefore := b.balance(b.carol.addr, params.BaseDenom)
	send := map[string]any{"bank": map[string]any{"send": map[string]any{"to_address": b.carol.addr.String(), "amount": []map[string]string{{"denom": params.BaseDenom, "amount": "1"}}}}}
	res := b.exec(b.alice, b.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{send}}}, nil)
	require.Zero(t, res.Code, res.Log)
	require.True(t, b.balance(b.carol.addr, params.BaseDenom).Sub(balanceBefore).Equal(math.NewInt(1)))
	unchanged, err := b.app.FeesKeeper.GetEarnings(b.ctx(), b.carol.addr)
	require.NoError(t, err)
	require.True(t, unchanged.Equal(after))
	b.bindFails(b.alice, map[string]any{"earnings": map[string]any{"pay": map[string]any{"recipient": b.carol.addr.String(), "amount": "999999999999999"}}}, "insufficient")
}

// TestBindings_bypassAttemptsAreRefused tries every way a contract could reach a module account or
// a raw message without a binding.
func TestBindings_bypassAttemptsAreRefused(t *testing.T) {
	b := newBindingChain(t)
	user := b.carol.addr.String()
	oneNorama := []map[string]string{{"denom": params.BaseDenom, "amount": "1"}}

	for name, msg := range map[string]struct {
		msg  map[string]any
		want string
	}{
		"any: a raw MsgSend": {map[string]any{"any": map[string]any{
			"type_url": "/cosmos.bank.v1beta1.MsgSend",
			"value":    "",
		}}, "disabled for contracts"},
		"bank send to a module": {map[string]any{"bank": map[string]any{"send": map[string]any{"to_address": moduleAddress("fees_deposits").String(), "amount": oneNorama}}}, "not allowed to receive"},
		"set withdraw address":  {map[string]any{"distribution": map[string]any{"set_withdraw_address": map[string]any{"address": user}}}, "set_withdraw_address"},
	} {
		t.Run(name, func(t *testing.T) {
			res := b.exec(b.alice, b.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{msg.msg}}}, nil)
			require.NotZero(t, res.Code)
			require.Contains(t, res.Log, msg.want)
		})
	}

	// A contract may pay a contract: the relay funds a second relay.
	id := b.storeRelay()
	other := b.instantiate(b.alice, id, map[string]any{}, nil)
	send := map[string]any{"bank": map[string]any{"send": map[string]any{"to_address": other.String(), "amount": oneNorama}}}
	res := b.exec(b.alice, b.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{send}}}, nil)
	require.Zero(t, res.Code, "a contract may pay another contract: %s", res.Log)
	require.True(t, b.balance(other, params.BaseDenom).Equal(math.NewInt(1)))

	// Shielded is not linked.
	b.bindFails(b.alice, map[string]any{"shielded": map[string]any{"shield": map[string]any{"amount": "1"}}}, wasmbindings.NotLinkedCode)
	_, err := b.ask(map[string]any{"shielded": map[string]any{}})
	require.ErrorContains(t, err, "codespace: "+wasmbindings.ModuleName+", code: 1", "wasmd redacts a query error to its codespace and code")
}

func (b *bindingChain) treeRoot(id uint64) []byte {
	b.t.Helper()
	var info wasmbindings.TreeInfo
	require.NoError(b.t, json.Unmarshal(b.mustAsk(map[string]any{"cnft": map[string]any{"tree": map[string]any{"tree_id": id}}}), &info))
	return info.Root
}

func testAsset(seed byte) []byte {
	asset := make([]byte, cnfttypes.HashSize)
	asset[0] = seed
	return asset
}

// contractLeaf is the leaf x/cnft stores for the first mint of asset into a collection the relay created.
func (b *bindingChain) contractLeaf(asset []byte, owner sdk.AccAddress, cid string) cnfttypes.Leaf {
	return cnfttypes.Leaf{
		AssetId: asset, Owner: owner.String(), MetadataCid: cid,
		CreatorHash: cnfttypes.CreatorHash(b.relay), Nonce: 0, HashId: cnfttypes.HashIDSHA256,
	}
}

func TestBindings_cnftMintAndProofVerification(t *testing.T) {
	b := newBindingChain(t)
	const depth = 5
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"create_collection": map[string]any{"name": "Art", "royalty_bps": 250}}})
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"create_tree": map[string]any{"collection_id": 1, "depth": depth, "buffer": 8, "canopy": 0}}})

	leaf := b.contractLeaf(testAsset(1), b.bob.addr, "bafyone")
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"mint": map[string]any{
		"tree_id": 1, "root": b.treeRoot(1),
		"leaves": []map[string]any{{"asset_id": leaf.AssetId, "owner": leaf.Owner, "metadata_cid": leaf.MetadataCid}},
	}}})

	hash, err := cnfttypes.HashLeaf(leaf)
	require.NoError(t, err)
	siblings, root, err := cnfttypes.Proof([][]byte{hash}, 0, depth)
	require.NoError(t, err)
	require.Equal(t, root, b.treeRoot(1), "the tree root is the root of the minted leaves")

	verify := func(l cnfttypes.Leaf, p cnfttypes.MerkleProof) wasmbindings.ProofResult {
		var res wasmbindings.ProofResult
		require.NoError(t, json.Unmarshal(b.mustAsk(map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 1, "leaf": l, "proof": p}}}), &res))
		return res
	}
	proof := cnfttypes.MerkleProof{Root: root, Index: 0, Siblings: siblings}
	require.True(t, verify(leaf, proof).Valid)
	other := leaf
	other.MetadataCid = "bafyother"
	require.False(t, verify(other, proof).Valid, "a leaf that was not minted does not verify")

	// Denial: only the creator of a tree mints into it.
	b.mustDeliver(b.alice, &cnfttypes.MsgCreateCollection{Creator: b.alice.addr.String(), Name: "Mine", RoyaltyBps: 0})
	b.mustDeliver(b.alice, &cnfttypes.MsgCreateTree{Creator: b.alice.addr.String(), CollectionId: 2, Depth: depth, Buffer: 8, Canopy: 0})
	b.bindFails(b.alice, map[string]any{"cnft": map[string]any{"mint": map[string]any{
		"tree_id": 2, "root": b.treeRoot(2),
		"leaves": []map[string]any{{"asset_id": testAsset(9), "owner": b.bob.addr.String(), "metadata_cid": "bafynine"}},
	}}}, "only the tree creator can mint")
	b.bindFails(b.alice, map[string]any{"cnft": map[string]any{"create_tree": map[string]any{"collection_id": 2, "depth": depth, "buffer": 8, "canopy": 0}}}, "only the collection creator can create a tree")
}

func TestBindings_marketListBidCancelAndSettle(t *testing.T) {
	b := newBindingChain(t)
	const depth = 5
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"create_collection": map[string]any{"name": "Art", "royalty_bps": 0}}})
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"create_tree": map[string]any{"collection_id": 1, "depth": depth, "buffer": 8, "canopy": 0}}})
	mine := b.contractLeaf(testAsset(1), b.relay, "bafymine")
	theirs := b.contractLeaf(testAsset(2), b.bob.addr, "bafytheirs")
	b.bind(b.alice, map[string]any{"cnft": map[string]any{"mint": map[string]any{
		"tree_id": 1, "root": b.treeRoot(1),
		"leaves": []map[string]any{
			{"asset_id": mine.AssetId, "owner": mine.Owner, "metadata_cid": mine.MetadataCid},
			{"asset_id": theirs.AssetId, "owner": theirs.Owner, "metadata_cid": theirs.MetadataCid},
		},
	}}})
	hashes := [][]byte{}
	for _, l := range []cnfttypes.Leaf{mine, theirs} {
		h, err := cnfttypes.HashLeaf(l)
		require.NoError(t, err)
		hashes = append(hashes, h)
	}
	proofOf := func(index int) cnfttypes.MerkleProof {
		siblings, root, err := cnfttypes.Proof(hashes, index, depth)
		require.NoError(t, err)
		return cnfttypes.MerkleProof{Root: root, Index: uint32(index), Siblings: siblings}
	}

	// The contract lists the leaf it owns, then cancels.
	b.bind(b.alice, map[string]any{"market": map[string]any{"list": map[string]any{"tree_id": 1, "leaf": mine, "proof": proofOf(0), "price": "1000"}}})
	require.Contains(t, string(b.mustAsk(map[string]any{"market": map[string]any{"listing": map[string]any{"id": 1}}})), b.relay.String())
	b.bind(b.alice, map[string]any{"market": map[string]any{"cancel_listing": map[string]any{"listing_id": 1}}})

	// It cannot list a leaf someone else owns, and cannot bid on its own listing.
	b.bindFails(b.alice, map[string]any{"market": map[string]any{"list": map[string]any{"tree_id": 1, "leaf": theirs, "proof": proofOf(1), "price": "1000"}}}, "")
	b.bind(b.alice, map[string]any{"market": map[string]any{"list": map[string]any{"tree_id": 1, "leaf": mine, "proof": proofOf(0), "price": "1000"}}})
	b.bindFails(b.alice, map[string]any{"market": map[string]any{"bid": map[string]any{"listing_id": 2, "amount": "5"}}}, "seller cannot bid on their own listing")

	// A user bids; the contract, as seller, accepts and settles. The proceeds are credited to the
	// contract's earnings, and the leaf moves to the bidder.
	b.mustDeliver(b.bob, &markettypes.MsgBid{Bidder: b.bob.addr.String(), ListingId: 2, Amount: math.NewInt(1_000)})
	b.bind(b.alice, map[string]any{"market": map[string]any{"settle": map[string]any{"listing_id": 2, "bid_id": 1, "leaf": mine, "proof": proofOf(0)}}})
	earned, err := b.app.FeesKeeper.GetEarnings(b.ctx(), b.relay)
	require.NoError(t, err)
	require.True(t, earned.Equal(math.NewInt(1_000)), "the sale proceeds are credited to the contract's earnings")

	// The contract bids on a user's listing and cancels the bid: the escrow comes back.
	b.mustDeliver(b.bob, &markettypes.MsgList{Seller: b.bob.addr.String(), TreeId: 1, Leaf: theirs, Proof: proofOf(1), Price: math.NewInt(9_000)})
	before := b.balance(b.relay, params.BaseDenom)
	b.bind(b.alice, map[string]any{"market": map[string]any{"bid": map[string]any{"listing_id": 3, "amount": "777"}}})
	require.True(t, b.balance(b.relay, params.BaseDenom).Equal(before.SubRaw(777)))
	b.bind(b.alice, map[string]any{"market": map[string]any{"cancel_bid": map[string]any{"listing_id": 3, "bid_id": 2}}})
	require.True(t, b.balance(b.relay, params.BaseDenom).Equal(before), "a cancelled bid is refunded to the contract")
}

func TestBindings_storageDealIsPaidFromTheContractsOwnFunds(t *testing.T) {
	b := newBindingChain(t)
	pieces := make([]storagetypes.PieceCommitment, 3)
	for i := range pieces {
		data := make([]byte, piece.LeafSize)
		data[0] = byte(i + 1)
		cm, err := piece.Commit(data)
		require.NoError(t, err)
		pieces[i] = storagetypes.PieceCommitment{Root: cm.Root, RealLeafCount: cm.RealLeafCount, PaddedLeafCount: cm.PaddedLeafCount, PieceBytes: uint64(len(data))}
	}
	nonce := make([]byte, storagetypes.NonceLen)
	nonce[0] = 9
	deal := map[string]any{"storage": map[string]any{"create_deal": map[string]any{
		"class": int32(storagetypes.DealClass_DEAL_CLASS_PRIVATE), "deal_nonce": nonce, "replicas": 3,
		"price_per_epoch": "1000", "duration_epochs": 100, "pieces": pieces,
	}}}
	before := b.balance(b.relay, params.BaseDenom)
	b.bind(b.alice, deal)
	spent := before.Sub(b.balance(b.relay, params.BaseDenom))
	require.True(t, spent.GTE(math.NewInt(3*1000*100)), "the escrow (price x replicas x duration) came out of the contract: %s", spent)

	got, err := b.app.StorageKeeper.Deals.Get(b.ctx(), 1)
	require.NoError(t, err)
	require.Equal(t, b.relay.String(), got.Client, "the contract is the deal's client")

	// A second deal from a contract with no funds fails, and a protocol class is refused.
	id := b.storeRelay()
	poor := b.instantiate(b.alice, id, map[string]any{}, nil)
	raw, err := json.Marshal(custom(deal))
	require.NoError(t, err)
	res := b.deliver(b.alice, &wasmtypes.MsgExecuteContract{Sender: b.alice.addr.String(), Contract: poor.String(), Msg: raw})
	require.NotZero(t, res.Code, "a contract without funds cannot open a deal")

	archive := map[string]any{"storage": map[string]any{"create_deal": map[string]any{
		"class": int32(storagetypes.DealClass_DEAL_CLASS_ARCHIVE), "deal_nonce": nonce, "replicas": 3,
		"price_per_epoch": "1000", "duration_epochs": 100, "pieces": pieces,
	}}}
	b.bindFails(b.alice, archive, "")
}
