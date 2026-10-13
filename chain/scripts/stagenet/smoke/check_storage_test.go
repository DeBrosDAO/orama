package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

var (
	rootA = strings.Repeat("a", 64)
	rootB = strings.Repeat("b", 64)
)

func TestParseSealOutput(t *testing.T) {
	got, err := parseSealOutput("slot 0 root " + rootA + "\nslot 1 root " + rootB + "\nnoise\n")
	require.NoError(t, err)
	require.Equal(t, map[int]string{0: rootA, 1: rootB}, got)

	_, err = parseSealOutput("")
	require.Error(t, err)
	_, err = parseSealOutput("slot 0 root " + rootA + "\nslot 0 root " + rootB)
	require.ErrorContains(t, err, "twice")
	_, err = parseSealOutput("slot 0 root nothex")
	require.Error(t, err)
}

func TestPieceSpecs_inSlotOrder(t *testing.T) {
	specs, err := pieceSpecs(map[int]string{1: rootB, 0: rootA}, map[int]int64{0: 4200, 1: 4200})
	require.NoError(t, err)
	require.Equal(t, []string{rootA + ":4200", rootB + ":4200"}, specs)

	_, err = pieceSpecs(map[int]string{0: rootA}, map[int]int64{})
	require.Error(t, err)
	_, err = pieceSpecs(map[int]string{0: rootA}, map[int]int64{0: 0})
	require.Error(t, err)
}

func slot(i uint32, node, op string) storagetypes.Slot {
	return storagetypes.Slot{Index: i, NodeId: node, Operator: op}
}

func TestDistinctProviders(t *testing.T) {
	require.NoError(t, distinctProviders([]storagetypes.Slot{slot(0, "a", "opa"), slot(1, "b", "opb"), slot(2, "c", "opc")}))
	require.ErrorContains(t, distinctProviders([]storagetypes.Slot{slot(0, "a", "opa"), slot(1, "a", "opa")}), "two slots")
	require.ErrorContains(t, distinctProviders([]storagetypes.Slot{slot(0, "a", "op"), slot(1, "b", "op")}), "operator op")
	require.ErrorContains(t, distinctProviders([]storagetypes.Slot{slot(0, "", "")}), "not assigned")
	require.NoError(t, distinctProviders(nil))
}

func TestProofsAccepted(t *testing.T) {
	nodes := []string{"a", "b"}
	okViews := []challengeView{{Epoch: 3, NodeID: "a", Proved: true}, {Epoch: 3, NodeID: "b", Proved: true}}
	require.NoError(t, proofsAccepted(nodes, okViews, 4))

	require.ErrorContains(t, proofsAccepted(nodes, okViews[:1], 4), "node b has no accepted proof")
	require.ErrorContains(t, proofsAccepted(nodes, nil, 4), "no accepted proof")

	// An unproved challenge of the running epoch is not a miss yet; one of a closed epoch is.
	open := append([]challengeView{{Epoch: 4, NodeID: "a", Proved: false}}, okViews...)
	require.NoError(t, proofsAccepted(nodes, open, 4))
	closed := append([]challengeView{{Epoch: 3, NodeID: "a", Proved: false}}, okViews...)
	require.ErrorContains(t, proofsAccepted(nodes, closed, 4), "closed epoch 3")
}

func TestSlotSizes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slot-0"), make([]byte, 10), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slot-1"), make([]byte, 20), 0o600))
	got, err := slotSizes(dir, 2)
	require.NoError(t, err)
	require.Equal(t, map[int]int64{0: 10, 1: 20}, got)
	_, err = slotSizes(dir, 3)
	require.Error(t, err)
}

func TestNewStorageFixture_writesPrivateFilesOfTheRightShape(t *testing.T) {
	fx, err := newStorageFixture(t.TempDir(), 5000)
	require.NoError(t, err)
	require.Len(t, fx.plain, 5000)
	require.Len(t, fx.nonceBytes, 32)
	require.Len(t, fx.nonce, 64)
	for _, p := range []string{fx.keyFile, fx.seedFile, fx.plainFile} {
		fi, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the storage CLI refuses a key file others can read")
	}
	key, err := os.ReadFile(fx.keyFile)
	require.NoError(t, err)
	require.Len(t, strings.TrimSpace(string(key)), 64, "the storage key is exactly 32 bytes of hex")
}

func TestSameBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(p, []byte("abc"), 0o600))
	require.NoError(t, sameBytes(p, []byte("abc")))
	require.ErrorContains(t, sameBytes(p, []byte("abd")), "differs")
	require.Error(t, sameBytes(p+"x", []byte("abc")))
}

func TestRunOrama_reportsFailureWithItsOutput(t *testing.T) {
	out, err := runOrama(t.Context(), "sh", []string{"X=1"}, "-c", "echo out; echo err >&2; exit 3")
	require.Error(t, err)
	require.Contains(t, out, "out")
	require.Contains(t, err.Error(), "err")
}

// The deal's signer holds only earnings, so its fee is exactly gas times the base fee: at the idle
// floor of 1 norama, a fixed 1500000 fee for 600000 gas was a 900000 tip and was refused.
func TestStorageTxFee_isTheBaseFeeWithNoTip(t *testing.T) {
	for _, tc := range []struct{ base, want int64 }{{1, txGas}, {7, 7 * txGas}} {
		if got := storageTxFee(math.NewInt(tc.base)); !got.Equal(math.NewInt(tc.want)) {
			t.Errorf("base fee %d: fee = %s, want %d", tc.base, got, tc.want)
		}
	}
}
