package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
)

// validatorsRPC serves CometBFT's validators method for a set of n validators, a page at a time,
// and returns the set's addresses in the order it lists them.
func validatorsRPC(t *testing.T, n int, wantHeight string) (*httptest.Server, [][]byte) {
	t.Helper()
	all := make([]*cmttypes.Validator, n)
	addrs := make([][]byte, n)
	for i := range all {
		pk := cmted25519.GenPrivKeyFromSecret([]byte{byte(i), byte(i >> 8)}).PubKey()
		all[i] = &cmttypes.Validator{Address: pk.Address(), PubKey: pk, VotingPower: 1}
		addrs[i] = pk.Address().Bytes()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var params map[string]string
		require.NoError(t, json.Unmarshal(req.Params, &params))
		require.Equal(t, wantHeight, params["height"])
		page, err := strconv.Atoi(params["page"])
		require.NoError(t, err)
		perPage, err := strconv.Atoi(params["per_page"])
		require.NoError(t, err)
		lo := min((page-1)*perPage, n)
		hi := min(lo+perPage, n)
		res := &coretypes.ResultValidators{BlockHeight: 5, Validators: all[lo:hi], Count: hi - lo, Total: n}
		require.NoError(t, json.NewEncoder(w).Encode(rpctypes.NewRPCSuccessResponse(req.ID, res)))
	}))
	t.Cleanup(srv.Close)
	return srv, addrs
}

func TestValidatorAddresses_readsEveryPageInCommitOrder(t *testing.T) {
	for _, n := range []int{0, 1, validatorsPerPage, validatorsPerPage + 1, 2*validatorsPerPage + 50} {
		srv, want := validatorsRPC(t, n, "5")
		c, err := Dial(srv.URL)
		require.NoError(t, err)
		got, err := c.ValidatorAddresses(context.Background(), 5)
		require.NoError(t, err)
		require.Len(t, got, n)
		for i := range want {
			require.Equal(t, want[i], got[i], "member %d of %d", i, n)
		}
	}
}

func TestValidatorAddresses_reportsAnRPCFailureWithTheHeight(t *testing.T) {
	srv := fakeRPC(t, map[string]any{})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	_, err = c.ValidatorAddresses(context.Background(), 9)
	require.ErrorContains(t, err, "validator set at 9")
}
