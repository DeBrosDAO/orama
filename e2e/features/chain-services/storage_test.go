//go:build e2e_fleet

package chainservices

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// x/storage genesis defaults and limits (x/storage/types/params.go, keys.go).
const (
	minDealBytes  = 1024
	dealFee       = 1000
	minReplicas   = 3
	maxReplicas   = 32
	maxDuration   = 1_000_000
	leafSize      = 1024
	nonceLen      = 32
	pricePerEpoch = 1_000_000
)

// piece is a well-formed PieceCommitment of n bytes (1 KiB leaves padded to a
// power of two, chain/piece) with a random root: CreateDeal checks the shape,
// not the bytes, which a provider proves later.
func piece(t *testing.T, n uint64) map[string]any {
	t.Helper()
	root := make([]byte, 32)
	if _, err := rand.Read(root); err != nil {
		t.Fatal(err)
	}
	leaves := (n + leafSize - 1) / leafSize
	padded := uint64(1)
	for padded < leaves {
		padded <<= 1
	}
	return map[string]any{"root": base64.StdEncoding.EncodeToString(root), "real_leaf_count": fmt.Sprint(leaves),
		"padded_leaf_count": fmt.Sprint(padded), "piece_bytes": fmt.Sprint(n)}
}

// dealSpec is a MsgCreateDeal body.
type dealSpec struct {
	signer, granter, class, delegate string
	replicas                         uint32
	price                            string
	duration                         uint64
	pieces                           []map[string]any
	nonceLen                         int
}

func (d dealSpec) msg(t *testing.T) chain.Msg {
	t.Helper()
	n := d.nonceLen
	if n == 0 {
		n = nonceLen
	}
	nonce := make([]byte, n)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	pieces := make([]any, 0, len(d.pieces))
	for _, p := range d.pieces {
		pieces = append(pieces, p)
	}
	return chain.NewMsg("/orama.storage.v1.MsgCreateDeal", map[string]any{
		"signer": d.signer, "granter": d.granter, "class": d.class, "deal_nonce": base64.StdEncoding.EncodeToString(nonce),
		"repair_delegate": d.delegate, "replicas": d.replicas, "price_per_epoch": d.price,
		"duration_epochs": fmt.Sprint(d.duration), "pieces": pieces,
	})
}

// publicPin is a valid PUBLIC_PIN deal for signer: 3 replicas of one 1 KiB piece.
func publicPin(t *testing.T, signer string) dealSpec {
	return dealSpec{signer: signer, class: "DEAL_CLASS_PUBLIC_PIN", replicas: minReplicas,
		price: fmt.Sprint(pricePerEpoch), duration: 2, pieces: []map[string]any{piece(t, minDealBytes)}}
}

// TestStorageDeal_unfundedClientRefused: a well-formed deal passes every check
// and is refused only when the fee and escrow (fee + price x replicas x
// duration) are pulled from the client's BANK balance (x/storage/keeper
// deals.go pullDealFunds), which no run account holds (funds.go); the
// refusal reverts the whole message, and the escrow invariants still hold.
func TestStorageDeal_unfundedClientRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	d := publicPin(t, k.Address)
	need := dealFee + pricePerEpoch*int64(d.replicas)*int64(d.duration)
	r := c.Submit(t, k, chain.TxOptions{}, d.msg(t))
	chain.RequireRefused(t, "unfunded deal", r, fmt.Sprintf("insufficient funds: need %dnorama", need))
	c.RequireInvariants(t, "a refused deal")
}

// TestStorageDeal_shapeRefusals: the stateless and parameter checks of
// MsgCreateDeal (x/storage/types/msgs.go ValidateBasic, keeper MinBytes),
// with the boundary values on both sides.
func TestStorageDeal_shapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	base := func(mut func(*dealSpec)) dealSpec {
		d := publicPin(t, k.Address)
		mut(&d)
		return d
	}
	cases := map[string]struct {
		d    dealSpec
		want string
	}{
		"piece one byte under the minimum": {base(func(d *dealSpec) { d.pieces = []map[string]any{piece(t, minDealBytes-1)} }), "minimum is 1024"},
		"archive class from a user":        {base(func(d *dealSpec) { d.class = "DEAL_CLASS_ARCHIVE" }), "user deals must be PRIVATE or PUBLIC_PIN"},
		"nonce of 31 bytes":                {base(func(d *dealSpec) { d.nonceLen = nonceLen - 1 }), "deal_nonce must be 32 bytes"},
		"two replicas":                     {base(func(d *dealSpec) { d.replicas = minReplicas - 1 }), "replicas must be in [3, 32]"},
		"33 replicas":                      {base(func(d *dealSpec) { d.replicas = maxReplicas + 1 }), "replicas must be in [3, 32]"},
		"zero price":                       {base(func(d *dealSpec) { d.price = "0" }), "price_per_epoch must be positive"},
		"zero duration":                    {base(func(d *dealSpec) { d.duration = 0 }), "duration_epochs must be in"},
		"duration over the maximum":        {base(func(d *dealSpec) { d.duration = maxDuration + 1 }), "duration_epochs must be in"},
		"public pin with two pieces":       {base(func(d *dealSpec) { d.pieces = append(d.pieces, piece(t, minDealBytes)) }), "exactly one piece commitment"},
		"private with one piece for three": {base(func(d *dealSpec) { d.class = "DEAL_CLASS_PRIVATE" }), "one piece commitment per replica"},
		"repair delegate with a space":     {base(func(d *dealSpec) { d.delegate = "bad delegate" }), "repair_delegate contains"},
		"leaf count that lies":             {base(func(d *dealSpec) { d.pieces[0]["real_leaf_count"] = "2" }), "implies 1 leaves"},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, tc.d.msg(t)), tc.want)
	}
	exact := base(func(d *dealSpec) { d.pieces = []map[string]any{piece(t, minDealBytes)} })
	chain.RequireRefused(t, "exactly the minimum passes the size check", c.Submit(t, k, chain.TxOptions{}, exact.msg(t)), "insufficient funds")
}

// TestStorageDeal_extendRefusals: MsgExtendDeal needs a deal id, 1..1e6
// extra epochs, and an existing deal (no deal can be funded on the run
// chain, so extending one that exists is blocked).
func TestStorageDeal_extendRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	extend := func(id string, extra uint64) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, chain.NewMsg("/orama.storage.v1.MsgExtendDeal", map[string]any{
			"signer": k.Address, "deal_id": id, "extra_epochs": fmt.Sprint(extra)}))
	}
	chain.RequireRefused(t, "deal id 0", extend("0", 1), "deal_id is required")
	chain.RequireRefused(t, "zero extra epochs", extend("1", 0), "extra_epochs must be in")
	chain.RequireRefused(t, "extra over the maximum", extend("1", maxDuration+1), "extra_epochs must be in")
	chain.RequireRefused(t, "unknown deal", extend("987654321", 1), "deal 987654321 does not exist")
	if out := c.QueryFails(t, k.Node, "storage", "deal", "987654321"); !strings.Contains(out, "does not exist") {
		t.Errorf("deal query of an unknown id: %s", out)
	}
}

// storageParams is the part of orama.storage.v1.Params these tests pin.
type storageParams struct {
	Params struct {
		MinDealBytes        chain.Int `json:"min_deal_bytes"`
		DealFee             chain.Int `json:"deal_fee"`
		MaxDealsPerBlock    chain.Int `json:"max_deals_per_block"`
		AcceptWindowBlocks  chain.Int `json:"accept_window_blocks"`
		MaxReleasesPerEpoch chain.Int `json:"max_releases_per_epoch"`
	} `json:"params"`
}

// TestStorageQueries_paramsQueueAndCeilings: the genesis parameters these
// tests rely on, a well-formed assignment queue, and the storage mint of the
// last closed epoch: nothing minted (no provider proved anything) against a
// ceiling of exactly 25% of that epoch's schedule (docs/whitepaper/technical-reference/vol2/40-economics.md "The split").
func TestStorageQueries_paramsQueueAndCeilings(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	var p storageParams
	c.Query(t, n, &p, "storage", "params")
	if p.Params.MinDealBytes.Int64() != minDealBytes || p.Params.DealFee.Int64() != dealFee || p.Params.MaxReleasesPerEpoch.Int64() != 2 ||
		p.Params.AcceptWindowBlocks.Int64() != 50 || p.Params.MaxDealsPerBlock.Int64() != 100 {
		t.Errorf("storage params %+v differ from the genesis defaults", p.Params)
	}
	var q struct {
		Pending chain.Int `json:"pending"`
		Head    chain.Int `json:"head"`
		Tail    chain.Int `json:"tail"`
	}
	c.Query(t, n, &q, "storage", "queue")
	if q.Tail.Cmp(q.Head) < 0 {
		t.Errorf("queue head %s after tail %s", q.Head.String(), q.Tail.String())
	}
	closed := uint64(c.Epoch(t, n, 0).CurrentEpoch.Int64()) - 1
	a := c.ABCIQuery(t, n, "/orama.storage.v1.Query/EpochMint", chain.PB{}.Uint(1, closed))
	if a.Code != 0 {
		t.Fatalf("EpochMint(%d): code %d %s", closed, a.Code, a.Log)
	}
	f, err := chain.DecodePB(a.Value)
	if err != nil {
		t.Fatal(err)
	}
	var sched struct {
		StorageCeiling chain.Int `json:"storage_ceiling"`
	}
	c.Query(t, n, &sched, "emission", "schedule-at", fmt.Sprint(closed))
	if minted, ceiling := f.Str(1), f.Str(2); (minted != "0" && minted != "") || ceiling != sched.StorageCeiling.String() {
		t.Errorf("epoch %d storage mint %q of ceiling %q, want 0 of %s", closed, minted, ceiling, sched.StorageCeiling.String())
	}
}
