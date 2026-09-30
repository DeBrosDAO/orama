package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
)

func TestParseNodes(t *testing.T) {
	got, err := parseNodes("athena=athena=37.59.116.212,superman=superman=141.227.165.168")
	require.NoError(t, err)
	require.Equal(t, []nodeRef{{"athena", "athena", "37.59.116.212"}, {"superman", "superman", "141.227.165.168"}}, got)
}

func TestParseNodes_refusesBadInput(t *testing.T) {
	for _, bad := range []string{
		"", "athena", "athena=athena", "a=b=c=d",
		"Athena=athena=1.2.3.4",                  // name is lowercase
		"athena=-x=1.2.3.4",                      // alias may not start with a dash (it goes to ssh)
		"athena=athena=1.2.3",                    // not an address
		"athena=athena;id=1.2.3.4",               // shell syntax in the alias
		"athena=athena=1.2.3.4,athena=b=1.2.3.5", // duplicate
	} {
		_, err := parseNodes(bad)
		require.Error(t, err, bad)
	}
}

func TestCommonFlagsValidate_refusesANonStagenetChain(t *testing.T) {
	const nodes = "a=a=1.2.3.4"
	_, err := commonFlags{chainID: "orama-mainnet-1", nodes: nodes}.validate()
	require.ErrorContains(t, err, "refusing to run")
	_, err = commonFlags{chainID: "Bad Chain", nodes: nodes}.validate()
	require.ErrorContains(t, err, "invalid --chain-id")
	_, err = commonFlags{chainID: "", nodes: nodes}.validate()
	require.Error(t, err)
	got, err := commonFlags{chainID: "orama-stagenet-1", nodes: nodes}.validate()
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, err = commonFlags{chainID: "orama-devnet-2", nodes: nodes}.validate()
	require.NoError(t, err)
}

func TestShieldedEnvLines(t *testing.T) {
	require.Equal(t,
		"ORAMA_SCENARIO_CHAIN_ID=orama-stagenet-1\nORAMA_SCENARIO_UNSHIELD_SIGNER=aabb\nORAMA_SCENARIO_SCALE=2000\nORAMA_SCENARIO_FEE=202001\n",
		shieldedEnvLines("orama-stagenet-1", "aabb", 2_000, math.NewInt(202001)))
}

func TestRun_refusesUnknownAndMissingCommands(t *testing.T) {
	code, err := run(context.Background(), nil)
	require.Error(t, err)
	require.Equal(t, 2, code)
	_, err = run(context.Background(), []string{"nope"})
	require.ErrorContains(t, err, "unknown command")
}

func TestRunSmoke_refusesAMainnetChainBeforeAnySSH(t *testing.T) {
	code, err := runSmoke(context.Background(), []string{"--chain-id", "orama-1", "--nodes", "a=a=1.2.3.4", "--orama", "/bin/sh", "--gateway", "https://x"})
	require.ErrorContains(t, err, "refusing to run")
	require.Equal(t, 2, code)
}
