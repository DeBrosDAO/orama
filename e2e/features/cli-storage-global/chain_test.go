//go:build e2e_fleet

package clistorageglobal

import (
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The chain's local APIs on a node (e2e/scripts/chain-deploy.sh API_PORT,
// RPC_PORT; loopback only, so these commands run on the node).
const (
	chainREST = "http://127.0.0.1:31003"
	// defaultChainRPC is where a fleet node's own chain answers.
	defaultChainRPC = "http://127.0.0.1:31001"
	// absentDeal is a deal id a fresh chain has not reached.
	absentDeal = "999999999"
	// putWait bounds how long put waits for an assignment that cannot come.
	putWait = "15s"
	// dealQuery is the ABCI query path storage put reads the deal through.
	dealQuery = "/orama.storage.v1.Query/Deal"
)

// onNode runs orama on n and returns its output.
func onNode(t testing.TB, f *fleet.Fleet, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	return infra.OnNode(t, f, n, args...)
}

// expectNodeRefused fails unless the command failed on n and said want.
func expectNodeRefused(t testing.TB, f *fleet.Fleet, out fleet.Output, want string) {
	t.Helper()
	text := out.Stdout + out.Stderr
	if out.Exit == exitOK || !strings.Contains(text, want) {
		t.Errorf("exit %d, want a refusal saying %q\n%s", out.Exit, want, f.Redact(text))
	}
}

// expectNodeFailure fails unless the command on n exited with the runtime
// failure class (not usage, not success) and said one of anyOf: a refusal
// for some other reason (a missing flag, a crash) is not the one under test.
func expectNodeFailure(t testing.TB, f *fleet.Fleet, out fleet.Output, anyOf ...string) {
	t.Helper()
	text := out.Stdout + out.Stderr
	if out.Exit != exitFailure {
		t.Fatalf("exit %d, want %d (a verification refusal)\n%s", out.Exit, exitFailure, f.Redact(text))
	}
	for _, want := range anyOf {
		if strings.Contains(text, want) {
			return
		}
	}
	t.Errorf("the refusal names none of %q\n%s", anyOf, f.Redact(text))
}

// TestChainTx_accountWithoutHistoryStopsBeforeSigning: with --node, the
// command reads the signer's account from the chain first; an account the
// chain has never seen stops there, before the agent is asked to sign and
// before anything is broadcast (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-cluster-register-onchain).
func TestChainTx_accountWithoutHistoryStopsBeforeSigning(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	args := append(withSigner("--operator", "global", "retire", "--id", nodeID), "--node", chainREST)
	args = replace(args, "--chain-id", f.State.ChainID)
	out := onNode(t, f, n, args...)
	expectNodeRefused(t, f, out, "read the chain account")
	if strings.Contains(out.Stdout, signDocHeader) {
		t.Error("with --node the command printed a sign document instead of stopping")
	}
}

// nodeSeeds writes owner and repair seed files (0600) on n for the storage
// transfer commands; the cleanup deletes them.
func nodeSeeds(t testing.TB, f *fleet.Fleet, n fleet.Node) (seed, repair string) {
	t.Helper()
	base := "/root/e2e-cli-storage-" + f.State.RunID + "-" + strings.ReplaceAll(t.Name(), "/", "-")
	seed, repair = base+"-seed", base+"-repair"
	f.WriteFile(t, n, seed, []byte(strings.Repeat("01", 32)+"\n"), 0o600)
	f.WriteFile(t, n, repair, []byte(strings.Repeat("02", 32)+"\n"), 0o600)
	return seed, repair
}

// TestStorageGet_absentDealWritesNothing: get looks the deal up on chain and
// fetches only a slot whose root matches it; a deal that does not exist
// writes no output (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-get).
func TestStorageGet_absentDealWritesNothing(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	seed, repair := nodeSeeds(t, f, n)
	out := seed + "-out"
	t.Cleanup(func() { edge.RunInCleanup(t, f, n, "rm -f -- "+fleet.ShellQuote(out)) })
	res := onNode(t, f, n, "storage", "get", "--deal-id", absentDeal, "--rpc", nodeChainRPC(f),
		"--storage-key-file", seed, "--repair-seed-file", repair, "--out", out)
	if res.Exit == exitOK {
		t.Fatalf("storage get of deal %s succeeded", absentDeal)
	}
	if f.Exec(t, n, "test -e "+fleet.ShellQuote(out)).Exit == 0 {
		t.Errorf("storage get of an absent deal wrote %s", out)
	}
}

// TestStoragePut_absentDealUploadsNothing: every file's root is checked
// against its slot on chain before any byte is sent, so a deal that does not
// exist uploads nothing (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-storage-put).
func TestStoragePut_absentDealUploadsNothing(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	dir := strings.TrimSpace(f.MustExec(t, n, "mktemp -d /root/e2e-cli-storage-put-XXXXXX").Stdout)
	t.Cleanup(func() { edge.RunInCleanup(t, f, n, "rm -rf -- "+fleet.ShellQuote(dir)) })
	f.WriteFile(t, n, dir+"/slot-0", []byte("not a sealed slot"), 0o600)
	res := onNode(t, f, n, "storage", "put", "--deal-id", absentDeal, "--dir", dir, "--rpc", nodeChainRPC(f), "--wait", putWait)
	// The chain answers the deal lookup with not-found before any slot is
	// waited for or sent (storageclient.Client.Put, parseABCIAnswer): a
	// runtime failure (not usage) naming the deal query, and no upload line.
	// An unreachable RPC ("query ... at ...: connection refused") does not pass.
	expectNodeFailure(t, f, res, "not found on chain: "+dealQuery, dealQuery+" failed with code")
	if strings.Contains(res.Stdout, "uploaded") {
		t.Errorf("storage put for absent deal %s reported an upload:\n%s", absentDeal, f.Redact(res.Stdout))
	}
}

// TestGlobalStageOramad_unverifiedBinaryRefused: stage-oramad places a binary
// only after it verifies against the adopted release root through TUF
// metadata; a binary with no metadata is refused and nothing is linked into
// the cosmovisor layout (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-global-stage-oramad).
// It stages into a root-owned scratch home, never the chain's own.
func TestGlobalStageOramad_unverifiedBinaryRefused(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	home := strings.TrimSpace(f.MustExec(t, n, "mktemp -d /root/e2e-stage-XXXXXX").Stdout)
	t.Cleanup(func() { edge.RunInCleanup(t, f, n, "rm -rf -- "+fleet.ShellQuote(home)) })
	meta := home + "/metadata"
	f.MustExec(t, n, "mkdir -m 0700 "+fleet.ShellQuote(meta))
	out := onNode(t, f, n, "global", "stage-oramad", "--binary", "/bin/true", "--release-metadata", meta,
		"--release-target", "oramad", "--upgrade", "e2e-bogus", "--home", home)
	// Refused by release verification (releaseverify.CheckFile): the node has
	// no adopted root, or the empty metadata dir lacks the TUF files.
	expectNodeFailure(t, f, out, "no release root adopted", "read release metadata")
	if strings.Contains(out.Stdout, "staged ") {
		t.Errorf("stage-oramad reported staging an unverified binary:\n%s", f.Redact(out.Stdout))
	}
	staged := home + "/cosmovisor/upgrades/e2e-bogus/bin/oramad"
	if f.Exec(t, n, "test -e "+fleet.ShellQuote(staged)).Exit == 0 {
		t.Errorf("a refused stage left %s", staged)
	}
}

// TestGlobalStageOramad_argumentChecks: the binary, metadata and target are
// required and exactly one of --upgrade and --genesis is given; as a user
// that is not root it is refused before touching anything.
func TestGlobalStageOramad_argumentChecks(t *testing.T) {
	t.Parallel()
	cli := cliNoWallet(t)
	full := []string{"global", "stage-oramad", "--binary", "/bin/true", "--release-metadata", t.TempDir(), "--release-target", "oramad"}
	infra.ExpectExit(t, run(t, cli, full[:4]...), exitUsage, "required")
	infra.ExpectExit(t, run(t, cli, full...), exitUsage, "exactly one of --upgrade")
	infra.ExpectExit(t, run(t, cli, append(full, "--genesis", "--upgrade", "x")...), exitUsage, "exactly one of --upgrade")
	res := run(t, cli, append(full, "--genesis")...)
	if os.Geteuid() != 0 {
		infra.ExpectExit(t, res, exitUsage, "must be run as root")
		return
	}
	infra.ExpectRefused(t, res)
}

// nodeChainRPC is where a node's chain answers, as the node sees it: the run's
// recorded endpoint when there is one (stagenet runs the chain co-located, in
// its own network namespace at 198.18.0.2), else the fleet node's localhost.
func nodeChainRPC(f *fleet.Fleet) string {
	if f.State.ChainRPC != "" {
		return f.State.ChainRPC
	}
	return defaultChainRPC
}
