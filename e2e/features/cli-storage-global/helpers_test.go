//go:build e2e_fleet

package clistorageglobal

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Accounts and keys for sign documents that are printed, never submitted.
// operator and pubKey are the vector pair core's own tests pin
// (core/pkg/clusterreg, core/pkg/globalbind: vectorAddress, vectorPubKey);
// hotKey is another canonical account: twenty 0x11 bytes in orama bech32.
const (
	operator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	pubKey   = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
	hotKey   = "orama1zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3fkx7z2"
	// signChainID names no real chain: nothing here is broadcast.
	signChainID = "orama-devnet-e2e-signdoc"
	fee         = "1000"
	gas         = "200000"
	nodeID      = "e2e-node-1"
	// signDocHeader is what a command without --node prints before the hex
	// (globalcmd/submit.go SubmitDirect, clustercmd/register.go).
	signDocHeader = "sign document (not submitted):"
)

// Exit codes (core/cmd/orama/internal/clierr).
const (
	exitOK       = infra.ExitOK
	exitFailure  = infra.ExitFailure
	exitUsage    = infra.ExitUsage
	exitNotFound = infra.ExitNotFound
)

// signerFlags are the flags every chain transaction command takes; the
// account flag is --operator or --signer depending on the command.
func signerFlags(accountFlag string) []string {
	return []string{accountFlag, operator, "--chain-id", signChainID, "--pubkey", pubKey,
		"--fee", fee, "--gas", gas, "--account-number", "7", "--sequence", "3"}
}

func run(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	return infra.Run(t, cli, args...)
}

func output(res oramacli.Result) string { return res.Stdout + res.Stderr }

// cliNoWallet is a machine without a wallet: no command here may sign.
func cliNoWallet(t testing.TB) *oramacli.Runner {
	t.Helper()
	return harness.CLI(t).NoWallet(t)
}

// signDoc returns the sign document a command printed, failing unless it
// printed exactly the header and one hex line.
func signDoc(t testing.TB, res oramacli.Result) []byte {
	t.Helper()
	infra.ExpectExit(t, res, exitOK, signDocHeader)
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 2 || lines[0] != signDocHeader {
		t.Fatalf("orama %v printed %q, want the header and one hex line", res.Args, res.Stdout)
	}
	doc, err := hex.DecodeString(lines[1])
	if err != nil || len(doc) == 0 {
		t.Fatalf("orama %v: sign document is not hex: %v", res.Args, err)
	}
	return doc
}

// expectContains fails unless doc holds every fragment (a protobuf string
// or bytes field carries its value verbatim).
func expectContains(t testing.TB, what string, doc []byte, fragments ...[]byte) {
	t.Helper()
	for _, frag := range fragments {
		if !bytes.Contains(doc, frag) {
			t.Errorf("%s: sign document lacks %q", what, frag)
		}
	}
}

// secretFile writes a hex secret of n random-looking bytes with mode.
func secretFile(t testing.TB, dir, name string, fill byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(bytes.Repeat([]byte{fill}, 32))+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
