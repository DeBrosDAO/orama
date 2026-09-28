package keeper_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestUpdateReportersRequiresCallerFlag(t *testing.T) {
	f := newTestFixture(t)
	original, next := acc(1), acc(2)
	operator := acc(3)
	f.init(t, 1, []sdk.AccAddress{original}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)

	msg := &types.MsgUpdateReporters{Signer: original.String(), Reporters: []string{next.String()}}
	_, err := f.Msg.UpdateReporters(f.Ctx, msg)
	require.ErrorIs(t, err, types.ErrReporterChangeForbidden)

	// A successful report does not flip the flag. The module never does.
	require.NoError(t, f.submit(t, original, 1, []types.RelayObservation{obs(rk, 1, "1", false)}))
	_, err = f.Msg.UpdateReporters(f.Ctx, msg)
	require.ErrorIs(t, err, types.ErrReporterChangeForbidden)

	// The signer is not an admin key: an unrelated address works once the
	// caller (x/houses, for a passed structural proposal) sets the flag.
	ctx := keeper.WithAllowReporterChange(f.Ctx)
	_, err = f.Msg.UpdateReporters(ctx, &types.MsgUpdateReporters{
		Signer:    acc(9).String(),
		Reporters: []string{next.String()},
	})
	require.NoError(t, err)

	err = f.submit(t, original, 2, []types.RelayObservation{obs(rk, 1, "1", false)})
	require.ErrorIs(t, err, types.ErrNotReporter)
	require.NoError(t, f.submit(t, next, 2, []types.RelayObservation{obs(rk, 1, "1", false)}))

	_, err = f.Msg.UpdateReporters(ctx, &types.MsgUpdateReporters{Signer: next.String(), Reporters: nil})
	require.Error(t, err)
}

func TestReporterSetMutationsAreGenesisOrUpdateOnly(t *testing.T) {
	root := relayRoot(t)
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		if strings.Contains(text, "Reporters.Set(") || strings.Contains(text, "Reporters.Remove(") {
			hits = append(hits, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	joined := strings.Join(hits, " ")
	require.Contains(t, joined, "genesis.go")
	require.Contains(t, joined, "reporters.go")
}

func TestModuleNeverSetsAllowReporterChange(t *testing.T) {
	root := relayRoot(t)
	var calls int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		calls += strings.Count(string(body), "WithAllowReporterChange(")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls, "only the function definition may mention the call form")
}

func relayRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(filepath.Dir(file))
}
