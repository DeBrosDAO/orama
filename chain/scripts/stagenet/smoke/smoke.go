package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"cosmossdk.io/math"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// runSmoke runs every check and prints its verdict. The exit code is 1 when any check failed.
func runSmoke(ctx context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	var common commonFlags
	common.register(fs)
	orama := fs.String("orama", "", "path of the orama CLI (linux or darwin, for this machine) [required]")
	caFile := fs.String("ca-file", "", "PEM bundle that signs the gateway's certificate")
	gateway := fs.String("gateway", "", "gateway base URL, e.g. https://stagenet.dbrsteting.bid [required]")
	voteExt := fs.Int64("vote-ext-height", 2, "the genesis vote_extensions_enable_height")
	scenario := fs.String("scenario", "", "shielded wallet scenario JSON from `deploy.sh gen-shielded`")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	nodes, err := common.validate()
	if err != nil {
		return 2, err
	}
	if *orama == "" || *gateway == "" {
		return 2, errors.New("--orama and --gateway are required")
	}
	if _, err := exec.LookPath(*orama); err != nil {
		return 2, fmt.Errorf("--orama: %w", err)
	}
	e := newEnv()
	e.voteExtHeight = *voteExt
	e.chainID, e.nodes, e.orama, e.caFile, e.gateway, e.scenario = common.chainID, nodes, *orama, *caFile, *gateway, *scenario
	if e.ssh, err = newSSHRunner(); err != nil {
		return 2, err
	}
	defer e.ssh.close()
	if e.workDir, err = os.MkdirTemp("/tmp", "stagenet-smoke"); err != nil {
		return 2, err
	}
	defer os.RemoveAll(e.workDir)

	sess, err := e.ssh.startAgent(ctx, nodes[0].Alias, filepath.Join(e.workDir, "agent.sock"))
	if err != nil {
		return 2, fmt.Errorf("the signing agent on %s: %w", nodes[0].Name, err)
	}
	defer sess.stop()
	e.agentSocket = sess.socket
	if e.signer, err = dialSigner(ctx, sess.socket); err != nil {
		return 2, err
	}
	fmt.Printf("smoke on %s as operator %s\n", common.chainID, e.signer.AccountAddress())
	results := runChecks(ctx, e)
	if failed := Report(os.Stdout, results); failed > 0 {
		return 1, nil
	}
	return 0, nil
}

// runChecks runs the checks in order. A chain that is not producing blocks makes every later chain
// check meaningless, so they are skipped with that reason rather than each timing out.
func runChecks(ctx context.Context, e *env) []Result {
	results := checkBlocks(ctx, e)
	for _, r := range results {
		if r.Name == "blocks" && r.Status == Fail {
			for _, name := range []string{"invariants", "wasm-codes", "wasm-cw20", storageName, archiveName, shieldedName, gatewayName} {
				results = append(results, skip(name, "not run: the chain is not producing blocks"))
			}
			return results
		}
	}
	c, err := e.client(ctx, e.nodes[0])
	if err != nil {
		return append(results, fail("wasm-codes", "%v", err))
	}
	results = append(results, checkInvariants(ctx, e)...)
	results = append(results, checkCodes(ctx, c), checkCW20(ctx, e, c), checkStoragePrivate(ctx, e),
		checkArchive(ctx, e), checkShielded(ctx, e), checkGateway(ctx, e))
	return results
}

// runShieldedEnv prints the environment the wallet builder needs for this chain: its id, the
// operator the unshield is signed for, and amounts and a transfer fee sized to the chain's own
// shielded and fee parameters. deploy.sh gen-shielded evals it around `cargo run --example gen_scenario`.
func runShieldedEnv(ctx context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("shielded-env", flag.ContinueOnError)
	var common commonFlags
	common.register(fs)
	operator := fs.String("operator", "", "orama address of the operator that signs the unshield [required]")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	nodes, err := common.validate()
	if err != nil {
		return 2, err
	}
	signerHex, err := addressBytesHex(*operator)
	if err != nil {
		return 2, err
	}
	e := newEnv()
	e.chainID, e.nodes = common.chainID, nodes
	if e.ssh, err = newSSHRunner(); err != nil {
		return 2, err
	}
	defer e.ssh.close()
	c, err := e.client(ctx, nodes[0])
	if err != nil {
		return 2, err
	}
	var sp shieldedtypes.QueryParamsResponse
	if err := c.Query(ctx, "/orama.shielded.v1.Query/Params", &shieldedtypes.QueryParamsRequest{}, &sp); err != nil {
		return 2, fmt.Errorf("read the shielded params: %w", err)
	}
	var bf feestypes.QueryBaseFeeResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/BaseFee", &feestypes.QueryBaseFeeRequest{}, &bf); err != nil {
		return 2, fmt.Errorf("read the base fee: %w", err)
	}
	const transferActions = 2
	fee := suggestedTransferFee(transferActions, sp.Params.ActionGas, bf.BaseFee, sp.Params.NullifierFee)
	fmt.Print(shieldedEnvLines(common.chainID, signerHex, scenarioScale, fee))
	return 0, nil
}

// shieldedEnvLines are the variables gen_scenario reads, as shell assignments.
func shieldedEnvLines(chainID, signerHex string, scale uint64, fee math.Int) string {
	return fmt.Sprintf("ORAMA_SCENARIO_CHAIN_ID=%s\nORAMA_SCENARIO_UNSHIELD_SIGNER=%s\nORAMA_SCENARIO_SCALE=%d\nORAMA_SCENARIO_FEE=%s\n",
		chainID, signerHex, scale, fee.String())
}
