package app_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/cosmos/gogogateway"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
)

// restMux serves the app's REST routes the way the API server does: every module's
// RegisterGRPCGatewayRoutes, over a real gRPC connection to the app's query services.
func (f *flow) restMux() *gwruntime.ServeMux {
	f.t.Helper()
	grpcCodec := codec.NewProtoCodec(f.app.InterfaceRegistry()).GRPCCodec()
	server := grpc.NewServer(grpc.ForceServerCodec(grpcCodec))
	f.app.RegisterGRPCServer(server)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(f.t, err)
	go func() { _ = server.Serve(lis) }()
	f.t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(grpcCodec)))
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = conn.Close() })

	mux := gwruntime.NewServeMux(gwruntime.WithMarshalerOption(gwruntime.MIMEWildcard, &gateway.JSONPb{
		EmitDefaults: true, OrigName: true, AnyResolver: f.app.InterfaceRegistry(),
	}))
	clientCtx := client.Context{}.WithGRPCClient(conn).WithInterfaceRegistry(f.app.InterfaceRegistry())
	f.app.BasicModuleManager.RegisterGRPCGatewayRoutes(clientCtx, mux)
	return mux
}

func (f *flow) get(mux http.Handler, path string) (int, map[string]any) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// Every Orama module serves its queries over REST on the node's API port: the paths are the
// google.api.http annotations of its query.proto, and the answers are the same as the gRPC ones.
func TestRESTGateway_everyModuleServesItsQueries(t *testing.T) {
	f := newFlow(t)
	mux := f.restMux()
	for _, path := range []string{
		"/orama/archive/v1/params",
		"/orama/cnft/v1/collection/1",
		"/orama/emission/v1/params",
		"/orama/emission/v1/current-epoch",
		"/orama/fees/v1/params",
		"/orama/fees/v1/base-fee",
		"/orama/houses/v1/params",
		"/orama/market/v1/invariants",
		"/orama/nodes/v1/params",
		"/orama/power/v1/params",
		"/orama/power/v1/lambda",
		"/orama/relay/v1/params",
		"/orama/shielded/v1/params",
		"/orama/storage/v1/params",
		"/orama/token/v1/params",
	} {
		code, body := f.get(mux, path)
		if path == "/orama/cnft/v1/collection/1" {
			require.Equal(t, http.StatusNotFound, code, "a collection that does not exist is a gRPC NotFound: %v", body)
			continue
		}
		require.Equal(t, http.StatusOK, code, "%s: %v", path, body)
		require.NotEmpty(t, body, path)
	}
}

func TestRESTGateway_answersCarryTheChainsValues(t *testing.T) {
	f := newFlow(t)
	mux := f.restMux()

	code, body := f.get(mux, "/orama/fees/v1/base-fee")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, body["base_fee"], "an Int is a decimal string")

	code, body = f.get(mux, "/orama/token/v1/params")
	require.Equal(t, http.StatusOK, code)
	params, ok := body["params"].(map[string]any)
	require.True(t, ok, "%v", body)
	require.Equal(t, "10000000000", params["creation_fee"])
}

func TestRESTGateway_pathAndQueryParametersReachTheQuery(t *testing.T) {
	f := newFlow(t)
	mux := f.restMux()

	code, body := f.get(mux, "/orama/emission/v1/schedule-at/3")
	require.Equal(t, http.StatusOK, code, "%v", body)
	require.Equal(t, "3", body["epoch"], "the {epoch} path segment is the request's epoch")

	code, _ = f.get(mux, "/orama/token/v1/token?denom=factory/orama1x/none")
	require.Equal(t, http.StatusNotFound, code, "a denom, which has slashes, is a query parameter")

	code, _ = f.get(mux, "/orama/emission/v1/schedule-at/notanumber")
	require.Equal(t, http.StatusBadRequest, code)

	code, _ = f.get(mux, "/orama/token/v1/nonsense")
	require.Equal(t, http.StatusNotFound, code)
}
