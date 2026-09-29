package wasmbindings_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

var (
	contract = sdk.AccAddress("contract_____________")
	user     = sdk.AccAddress("user_________________")
)

func decode(t *testing.T, raw string) (wasmbindings.Action, error) {
	t.Helper()
	return wasmbindings.Decode(contract, json.RawMessage(raw))
}

func mustDecode(t *testing.T, raw string) wasmbindings.Action {
	t.Helper()
	a, err := decode(t, raw)
	require.NoError(t, err)
	return a
}

func TestDecode_tokenMessagesCarryTheContractAsSigner(t *testing.T) {
	create := mustDecode(t, `{"token":{"create":{"subdenom":"gold","name":"Gold","symbol":"GLD","description":"d","mint":true}}}`)
	require.Len(t, create.Msgs, 1)
	msg := create.Msgs[0].(*tokentypes.MsgCreateToken)
	require.Equal(t, contract.String(), msg.Creator)
	require.Equal(t, "gold", msg.Subdenom)
	require.True(t, msg.Mint)

	mint := mustDecode(t, `{"token":{"mint":{"denom":"factory/x/gold","recipient":"`+user.String()+`","amount":"5"}}}`)
	m := mint.Msgs[0].(*tokentypes.MsgMint)
	require.Equal(t, contract.String(), m.Sender)
	require.Equal(t, "5", m.Amount.String())

	burn := mustDecode(t, `{"token":{"burn":{"denom":"factory/x/gold","amount":"2"}}}`)
	require.Equal(t, contract.String(), burn.Msgs[0].(*tokentypes.MsgBurn).Sender)
}

func TestDecode_tokenBindingRefusesNorama(t *testing.T) {
	for name, raw := range map[string]string{
		"create norama wrapper": `{"token":{"create":{"subdenom":"norama"}}}`,
		"mint norama itself":    `{"token":{"mint":{"denom":"norama","recipient":"` + user.String() + `","amount":"1"}}}`,
		"mint a norama wrapper": `{"token":{"mint":{"denom":"factory/` + contract.String() + `/norama","recipient":"` + user.String() + `","amount":"1"}}}`,
		"burn norama":           `{"token":{"burn":{"denom":"norama","amount":"1"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decode(t, raw)
			require.ErrorIs(t, err, policytypes.ErrNoramaWrapper)
		})
	}
	// The declared limit (O-B): a differently named IOU is allowed.
	_, err := decode(t, `{"token":{"create":{"subdenom":"wnorama"}}}`)
	require.NoError(t, err)
}

func TestDecode_cnftAndMarketAndStorage(t *testing.T) {
	col := mustDecode(t, `{"cnft":{"create_collection":{"name":"c","royalty_bps":250}}}`)
	require.Equal(t, contract.String(), col.Msgs[0].(*cnfttypes.MsgCreateCollection).Creator)

	tree := mustDecode(t, `{"cnft":{"create_tree":{"collection_id":1,"depth":10,"buffer":8,"canopy":0}}}`)
	require.Equal(t, contract.String(), tree.Msgs[0].(*cnfttypes.MsgCreateTree).Creator)

	mint := mustDecode(t, `{"cnft":{"mint":{"tree_id":1,"root":"AQID","leaves":[{"asset_id":"AQID","owner":"`+user.String()+`","metadata_cid":"bafy"}]}}}`)
	mm := mint.Msgs[0].(*cnfttypes.MsgMint)
	require.Equal(t, contract.String(), mm.Creator)
	require.Equal(t, []byte{1, 2, 3}, mm.Root)
	require.Len(t, mm.Leaves, 1)

	list := mustDecode(t, `{"market":{"list":{"tree_id":1,"leaf":{"nonce":1},"proof":{"index":0},"price":"9"}}}`)
	require.Equal(t, contract.String(), list.Msgs[0].(*markettypes.MsgList).Seller)
	bid := mustDecode(t, `{"market":{"bid":{"listing_id":3,"amount":"7"}}}`)
	require.Equal(t, contract.String(), bid.Msgs[0].(*markettypes.MsgBid).Bidder)
	settle := mustDecode(t, `{"market":{"settle":{"listing_id":3,"bid_id":4,"leaf":{},"proof":{}}}}`)
	require.Equal(t, contract.String(), settle.Msgs[0].(*markettypes.MsgSettle).Signer)
	require.Equal(t, contract.String(), mustDecode(t, `{"market":{"cancel_listing":{"listing_id":1}}}`).Msgs[0].(*markettypes.MsgCancelListing).Seller)
	require.Equal(t, contract.String(), mustDecode(t, `{"market":{"cancel_bid":{"listing_id":1,"bid_id":2}}}`).Msgs[0].(*markettypes.MsgCancelBid).Bidder)

	deal := mustDecode(t, `{"storage":{"create_deal":{"class":1,"replicas":3,"price_per_epoch":"10","duration_epochs":5,"pieces":[{"piece_bytes":4}]}}}`)
	d := deal.Msgs[0].(*storagetypes.MsgCreateDeal)
	require.Equal(t, contract.String(), d.Signer)
	require.Empty(t, d.Granter, "a contract never spends a user's deal allowance")
}

func TestDecode_earningsPayment(t *testing.T) {
	a := mustDecode(t, `{"earnings":{"pay":{"recipient":"`+user.String()+`","amount":"12"}}}`)
	require.Empty(t, a.Msgs)
	require.NotNil(t, a.Pay)
	require.Equal(t, "12", a.Pay.Amount.String())
}

func TestDecode_shieldedIsNotLinked(t *testing.T) {
	_, err := decode(t, `{"shielded":{"shield":{"amount":"1"}}}`)
	require.ErrorIs(t, err, wasmbindings.ErrNotLinked)
}

func TestDecode_rejectsMalformedMessages(t *testing.T) {
	for name, raw := range map[string]string{
		"empty object":        `{}`,
		"not json":            `nope`,
		"trailing data":       `{"earnings":{"pay":{"recipient":"a","amount":"1"}}} {}`,
		"unknown top field":   `{"bank":{}}`,
		"unknown inner field": `{"token":{"burn":{"denom":"d","amount":"1","sender":"orama1attacker"}}}`,
		"two top variants":    `{"token":{"burn":{"denom":"d","amount":"1"}},"cnft":{"create_collection":{"name":"n"}}}`,
		"two token variants":  `{"token":{"burn":{"denom":"d","amount":"1"},"mint":{"denom":"d","recipient":"r","amount":"1"}}}`,
		"empty token":         `{"token":{}}`,
		"numeric amount":      `{"token":{"burn":{"denom":"d","amount":1}}}`,
		"empty earnings":      `{"earnings":{}}`,
		"empty storage":       `{"storage":{}}`,
		"empty market":        `{"market":{}}`,
		"empty cnft":          `{"cnft":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decode(t, raw)
			require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
		})
	}
}

func TestDecode_aSenderFieldCannotBeSmuggledIn(t *testing.T) {
	// The contract is always the signer. A message that names another creator or sender fails to parse.
	for _, raw := range []string{
		`{"token":{"create":{"creator":"` + user.String() + `","subdenom":"x"}}}`,
		`{"token":{"mint":{"sender":"` + user.String() + `","denom":"d","recipient":"r","amount":"1"}}}`,
		`{"storage":{"create_deal":{"granter":"` + user.String() + `"}}}`,
		`{"market":{"bid":{"bidder":"` + user.String() + `","listing_id":1,"amount":"1"}}}`,
	} {
		_, err := decode(t, raw)
		require.ErrorIs(t, err, wasmbindings.ErrBadMessage, raw)
	}
}
