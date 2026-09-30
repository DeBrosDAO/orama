package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// pathRPC answers abci_query by the query path it asks for.
func pathRPC(t *testing.T, byPath map[string]gogoproto.Message) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var p struct {
			Path string `json:"path"`
		}
		require.NoError(t, json.Unmarshal(req.Params, &p))
		for path, msg := range byPath {
			if strings.Trim(p.Path, `"`) == path {
				require.NoError(t, json.NewEncoder(w).Encode(rpctypes.NewRPCSuccessResponse(req.ID, abciAnswer(t, 0, "", msg))))
				return
			}
		}
		require.NoError(t, json.NewEncoder(w).Encode(rpctypes.NewRPCSuccessResponse(req.ID, abciAnswer(t, 22, "rpc error: code = NotFound desc = not found", nil))))
	}))
}

// A hot key has no bank balance and pays base fees from its fee-only balance: the stagenet
// monitor raised "hot key balance is 0" on funded providers because it read the bank alone.
func TestFeeFunds_isBankPlusTheFeeOnlyBalance(t *testing.T) {
	coin := sdk.NewCoin(params.BaseDenom, math.NewInt(7))
	srv := pathRPC(t, map[string]gogoproto.Message{
		"/cosmos.bank.v1beta1.Query/Balance": &banktypes.QueryBalanceResponse{Balance: &coin},
		"/orama.fees.v1.Query/FeeBalance":    &feestypes.QueryFeeBalanceResponse{Balance: math.NewInt(2_000_000_000)},
	})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	got, err := c.FeeFunds(context.Background(), "orama1hotkey")
	require.NoError(t, err)
	require.Equal(t, "2000000007", got.String())
}

func TestFeeFunds_aFailedFeeBalanceReadIsAnError(t *testing.T) {
	coin := sdk.NewCoin(params.BaseDenom, math.ZeroInt())
	srv := pathRPC(t, map[string]gogoproto.Message{"/cosmos.bank.v1beta1.Query/Balance": &banktypes.QueryBalanceResponse{Balance: &coin}})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	_, err = c.FeeFunds(context.Background(), "orama1hotkey")
	require.ErrorContains(t, err, "fee balance")
}
