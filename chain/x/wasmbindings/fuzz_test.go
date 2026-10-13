package wasmbindings_test

import (
	"encoding/json"
	"errors"
	"testing"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
)

// signerOf is the account a decoded message acts as. A binding that lets a contract name someone
// else here would let it spend another account's tokens.
func signerOf(t *testing.T, msg sdk.Msg) string {
	t.Helper()
	switch m := msg.(type) {
	case *tokentypes.MsgCreateToken:
		return m.Creator
	case *tokentypes.MsgMint:
		return m.Sender
	case *tokentypes.MsgBurn:
		return m.Sender
	case *cnfttypes.MsgCreateCollection:
		return m.Creator
	case *cnfttypes.MsgCreateTree:
		return m.Creator
	case *cnfttypes.MsgMint:
		return m.Creator
	case *markettypes.MsgList:
		return m.Seller
	case *markettypes.MsgCancelListing:
		return m.Seller
	case *markettypes.MsgBid:
		return m.Bidder
	case *markettypes.MsgCancelBid:
		return m.Bidder
	case *markettypes.MsgSettle:
		return m.Signer
	case *storagetypes.MsgCreateDeal:
		return m.Signer
	}
	t.Fatalf("Decode produced a message type the bindings do not list: %T", msg)
	return ""
}

var messageSeeds = []string{
	`{"token":{"create":{"subdenom":"gold","name":"Gold","symbol":"GLD","description":"d","mint":true}}}`,
	`{"token":{"mint":{"denom":"factory/x/gold","recipient":"orama1abc","amount":"5"}}}`,
	`{"token":{"burn":{"denom":"norama","amount":"1"}}}`,
	`{"token":{"create":{"subdenom":"norama"}}}`,
	`{"cnft":{"create_collection":{"name":"c","royalty_bps":250}}}`,
	`{"cnft":{"create_tree":{"collection_id":1,"depth":10,"buffer":8,"canopy":0}}}`,
	`{"cnft":{"mint":{"tree_id":1,"root":"AQID","leaves":[{"asset_id":"AQID","owner":"o","metadata_cid":"bafy"}]}}}`,
	`{"market":{"bid":{"listing_id":3,"amount":"9"}}}`,
	`{"market":{"settle":{"listing_id":3,"bid_id":1,"leaf":{},"proof":{}}}}`,
	`{"storage":{"create_deal":{"class":1,"replicas":3}}}`,
	`{"earnings":{"pay":{"recipient":"orama1abc","amount":"7"}}}`,
	`{"shielded":{"shield":{}}}`,
	`{}`, `{"token":{},"cnft":{}}`, `{"token":{"mint":{}}} trailing`, `null`, `[]`, ``,
}

// FuzzWasmBindingsDecode: whatever JSON a contract emits as a custom message, Decode never panics;
// a refusal is a typed error; an accepted message is exactly one action; and every SDK message it
// builds is signed by the contract itself and never touches norama or a norama wrapper.
func FuzzWasmBindingsDecode(f *testing.F) {
	for _, s := range messageSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		action, err := wasmbindings.Decode(contract, json.RawMessage(raw))
		if err != nil {
			if len(action.Msgs) != 0 || action.Pay != nil {
				t.Fatal("a refused message still carries an action")
			}
			return
		}
		if (action.Pay != nil) == (len(action.Msgs) != 0) {
			t.Fatalf("an accepted message is neither one payment nor SDK messages: pay=%v msgs=%d", action.Pay != nil, len(action.Msgs))
		}
		for _, msg := range action.Msgs {
			if got := signerOf(t, msg); got != contract.String() {
				t.Fatalf("%T is signed by %q, not by the contract", msg, got)
			}
			switch m := msg.(type) {
			case *tokentypes.MsgMint:
				if err := wasmpolicy.RefuseNoramaWrapper(m.Denom, []string{m.Denom}); err != nil {
					t.Fatalf("minting %q was allowed", m.Denom)
				}
			case *tokentypes.MsgBurn:
				if err := wasmpolicy.RefuseNoramaWrapper(m.Denom, []string{m.Denom}); err != nil {
					t.Fatalf("burning %q was allowed", m.Denom)
				}
			case *tokentypes.MsgCreateToken:
				if err := wasmpolicy.RefuseNoramaWrapper(tokentypes.Denom(m.Creator, m.Subdenom), nil); err != nil {
					t.Fatalf("creating %q was allowed", m.Subdenom)
				}
			}
		}
	})
}

// FuzzWasmBindingsQuery: whatever JSON a contract sends as a custom query, Querier.Query never
// panics (other than the gas meter aborting a query that cannot pay), a shielded query is always
// NOT_LINKED, and an answer is valid JSON.
func FuzzWasmBindingsQuery(f *testing.F) {
	for _, s := range []string{
		`{"token":{"info":{"denom":"factory/x/gold"}}}`,
		`{"cnft":{"tree":{"tree_id":1}}}`,
		`{"cnft":{"verify_proof":{"tree_id":1,"leaf":{},"proof":{"siblings":["AA=="],"index":0}}}}`,
		`{"market":{"listing":{"id":5}}}`,
		`{"shielded":{"balance":{}}}`, `{}`, `{"bank":{}}`, `{"token":{}}`, `null`,
	} {
		f.Add([]byte(s))
	}
	key := storetypes.NewKVStoreKey("fuzzq")
	trees := map[uint64]cnfttypes.Tree{}
	q := newQuerier(trees)
	f.Fuzz(func(t *testing.T, raw []byte) {
		ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_fuzzq")).Ctx.WithGasMeter(storetypes.NewGasMeter(2_000_000))
		var (
			out []byte
			err error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, isGas := r.(storetypes.ErrorOutOfGas); !isGas {
						t.Fatalf("Query panicked: %v", r)
					}
					err = errors.New("out of gas")
				}
			}()
			out, err = q.Query(ctx, json.RawMessage(raw))
		}()
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("an answer that is not JSON: %q", out)
		}
		var probe struct {
			Shielded json.RawMessage `json:"shielded"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Shielded != nil {
			t.Fatal("a shielded query was answered")
		}
	})
}
