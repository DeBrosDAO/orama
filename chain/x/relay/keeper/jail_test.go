package keeper_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestJailOnlyThroughJailRelay(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)

	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.False(t, relay.Jailed)

	// Low uptime pays nothing and does not jail.
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 80, "0.1", false)}))
	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.IsZero())
	relay, err = f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.False(t, relay.Jailed)

	require.NoError(t, f.Keeper.JailRelay(f.Ctx, rk.fp))
	relay, err = f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.True(t, relay.Jailed)
	require.NoError(t, f.Keeper.JailRelay(f.Ctx, rk.fp))

	f.Emission.setCeiling(3, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 3, []types.RelayObservation{obs(rk, 80, "1", false)}))
	result, err = f.Keeper.SettleEpoch(f.Ctx, 3)
	require.NoError(t, err)
	require.True(t, result.Minted.IsZero())
	require.True(t, f.Earnings.balance(operator).IsZero())

	err = f.Keeper.JailRelay(f.Ctx, bytesRepeat(types.RSAFingerprintLen))
	require.Error(t, err)
}

func bytesRepeat(n int) []byte {
	out := make([]byte, n)
	out[0] = 0xff
	return out
}

func TestNoMsgOrSettlementJails(t *testing.T) {
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
		if strings.Contains(string(body), "Jailed = true") || strings.Contains(string(body), "Jailed: true") {
			hits = append(hits, path)
		}
		if strings.Contains(string(body), "MsgJail") || strings.Contains(string(body), "SlashRelay") {
			hits = append(hits, path+"#slash")
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Contains(t, hits[0], filepath.Join("keeper", "jail.go"))
}
