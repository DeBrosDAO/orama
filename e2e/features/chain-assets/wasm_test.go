//go:build e2e_fleet

package chainassets

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// wasmMagic is the smallest well-formed wasm module header.
var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// genesisModules lists the app_state modules of n's genesis file.
func genesisModules(t *testing.T, c *chain.Chain, n fleet.Node) map[string]bool {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, "sudo -u "+chain.ServiceUser+" python3 -c "+fleet.ShellQuote(
		`import json;print(json.dumps(sorted(json.load(open("`+chain.Home+`/config/genesis.json"))["app_state"].keys())))`))
	var mods []string
	if out.Exit != 0 || json.Unmarshal([]byte(out.Stdout), &mods) != nil {
		t.Fatalf("%s: cannot read the genesis modules: %s %s", n.Name, out.Stdout, out.Stderr)
	}
	set := map[string]bool{}
	for _, m := range mods {
		set[m] = true
	}
	return set
}

func storeCodeMsg(sender string) chain.Msg {
	return chain.NewMsg("/cosmwasm.wasm.v1.MsgStoreCode", map[string]any{
		"sender": sender, "wasm_byte_code": base64.StdEncoding.EncodeToString(wasmMagic)})
}

// TestWasm_policyMatchesTheBuild: x/wasmpolicy is always wired; x/wasm only
// when oramad links libwasmvm (docs/CHAIN.md "Modules wired"). The run's
// oramad is built by chain-deploy.sh with CGO_ENABLED=0, a no-VM build: its
// genesis has wasmpolicy and no wasm, the wasm query and message types do not
// exist, and a MsgStoreCode cannot even be signed. A VM build instead
// refuses the upload in the ante chain until upload_sunset_height
// (x/wasmpolicy ErrUploadClosed).
func TestWasm_policyMatchesTheBuild(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	mods := genesisModules(t, c, k.Node)
	if !mods["wasmpolicy"] {
		t.Errorf("genesis has no wasmpolicy state: x/wasmpolicy must always be wired (modules: %v)", mods)
	}
	if mods["wasm"] {
		r := c.Submit(t, k, chain.TxOptions{}, storeCodeMsg(k.Address))
		chain.RequireRefused(t, "code upload before the sunset height", r, "code upload is closed until upload_sunset_height")
		return
	}
	q := c.QueryOut(t, k.Node, "wasm", "list-code")
	if q.Exit == 0 {
		t.Errorf("a no-VM build answers wasm queries: %s", q.Stdout)
	}
	e := c.SignExpectRefused(t, k, chain.TxOptions{}, storeCodeMsg(k.Address))
	if !strings.Contains(e, "cosmwasm.wasm.v1.MsgStoreCode") {
		t.Errorf("signing a MsgStoreCode on a no-VM build failed for another reason: %s", e)
	}
}
