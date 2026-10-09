//go:build e2e_fleet

package chain

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// EncodeTx turns a signed transaction in proto-JSON (what Sign returns) into the protobuf TxRaw
// bytes a wallet sends to the gateway's /v1/chain/simulate and /v1/chain/broadcast, with the
// node's own encoder (`oramad tx encode`).
func (c *Chain) EncodeTx(t testing.TB, n fleet.Node, signed []byte) []byte {
	t.Helper()
	dir, err := c.stageDir(t, n, "s.json", signed)
	if err != nil {
		t.Fatal(err)
	}
	encode := strings.ReplaceAll(c.OramadCmd("tx", "encode", "$D/s.json"), "'$D/", `"$D"'/`)
	out := c.Run(t, n, QueryBudget, "D="+fleet.ShellQuote(dir)+"\ntrap 'rm -rf -- \"$D\"' EXIT\n"+encode)
	if out.Exit != 0 {
		t.Fatalf("%s: oramad tx encode exited %d: %s", n.Name, out.Exit, c.F.Redact(out.Stderr))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.Stdout))
	if err != nil || len(raw) == 0 {
		t.Fatalf("%s: oramad tx encode printed %q, not base64 of a transaction: %v", n.Name, out.Stdout, err)
	}
	return raw
}
