package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestFormatNodeStatus_absentNode(t *testing.T) {
	require.Equal(t, "exists=false\n", formatNodeStatus(nil))
}

func TestFormatNodeStatus_bondsAndCapacity(t *testing.T) {
	out := formatNodeStatus(&nodestypes.Node{
		Operator: "orama1op", HotKey: "orama1hot", Asn: 16276, DeclaredCapacityBytes: 10,
		Status: nodestypes.NodeStatusActive,
		Bonds: []nodestypes.RoleBond{
			{Role: nodestypes.RoleStorage, Amount: math.NewInt(7)},
		},
	})
	require.Contains(t, out, "exists=true\n")
	require.Contains(t, out, "storage_bond=7\n")
	require.Contains(t, out, "archiver_bond=0\n", "a role with no bond reads as zero")
	require.Contains(t, out, "capacity=10\n")
	require.Contains(t, out, "asn=16276\n")
	require.Contains(t, out, "hot_key=orama1hot\n")
}

func TestRun_refusesAnUnknownOrMissingCommand(t *testing.T) {
	var out bytes.Buffer
	require.Error(t, run(context.Background(), nil, &bytes.Buffer{}, &out))
	require.ErrorContains(t, run(context.Background(), []string{"nope"}, &bytes.Buffer{}, &out), "unknown command")
}

func TestCmdRegisterOperator_refusesWithoutAKeyBeforeAnyNetworkCall(t *testing.T) {
	err := run(context.Background(), []string{"register-operator", "--rpc", "tcp://127.0.0.1:1"}, bytes.NewBufferString("no key"), &bytes.Buffer{})
	require.ErrorContains(t, err, "no 64-digit hex private key")
}

func TestCmdFundHotKey_refusesABadAmountBeforeReadingTheKey(t *testing.T) {
	err := run(context.Background(), []string{"fund-hot-key", "--node-id", "n", "--amount", "abc"}, &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorContains(t, err, "not an integer")
}

func TestCmdNodeStatus_requiresAnID(t *testing.T) {
	require.Error(t, run(context.Background(), []string{"node-status"}, &bytes.Buffer{}, &bytes.Buffer{}))
}

func TestCmdAddress_requiresAKeyFile(t *testing.T) {
	require.Error(t, run(context.Background(), []string{"address"}, &bytes.Buffer{}, &bytes.Buffer{}))
}

func TestCmdFeeBalance_andEarnings_requireAnAddress(t *testing.T) {
	for _, cmd := range []string{"fee-balance", "earnings"} {
		require.ErrorContains(t, run(context.Background(), []string{cmd}, &bytes.Buffer{}, &bytes.Buffer{}), "--address is required", cmd)
	}
}

func TestCmdAgent_refusesANegativeTTLAndNeedsASocket(t *testing.T) {
	require.ErrorContains(t, run(context.Background(), []string{"agent", "--ttl", "-1s"}, &bytes.Buffer{}, &bytes.Buffer{}), "--ttl")
	key := "0101010101010101010101010101010101010101010101010101010101010101\n"
	require.ErrorContains(t, run(context.Background(), []string{"agent"}, bytes.NewBufferString(key), &bytes.Buffer{}), "at least one --listen")
}

func TestCmdAgent_stopsAtItsTTL(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "a.sock")
	key := "0101010101010101010101010101010101010101010101010101010101010101\n"
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"agent", "--ttl", "300ms", "--listen", sock + ":" + strconv.Itoa(os.Getuid())}, bytes.NewBufferString(key), &bytes.Buffer{})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop at its ttl")
	}
}
