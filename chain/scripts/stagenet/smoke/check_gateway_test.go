package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGatewayNode(t *testing.T) {
	require.NoError(t, parseGatewayNode([]byte(`{"node":{"node_id":"stagenet-athena","operator":"orama1x","status":"NODE_STATUS_ACTIVE"}}`), "stagenet-athena"))
	require.ErrorContains(t, parseGatewayNode([]byte(`{"node":{"node_id":"other","operator":"orama1x"}}`), "stagenet-athena"), "names node")
	require.ErrorContains(t, parseGatewayNode([]byte(`{"node":{"node_id":"a"}}`), "a"), "no operator")
	require.ErrorContains(t, parseGatewayNode([]byte(`{}`), "a"), "names node")
	require.Error(t, parseGatewayNode([]byte(`<html>`), "a"))
}

func TestGatewayNodeURL(t *testing.T) {
	got := gatewayNodeURL("https://stagenet.dbrsteting.bid/", "stagenet-athena")
	require.Equal(t, "https://stagenet.dbrsteting.bid/v1/chain/query/orama.nodes.v1.Query/Node?json=%7B%22node_id%22%3A%22stagenet-athena%22%7D", got)
}

func TestGatewayClient_refusesAMissingOrEmptyCAFile(t *testing.T) {
	_, err := gatewayClient(filepath.Join(t.TempDir(), "none.pem"))
	require.Error(t, err)
	empty := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(empty, []byte("not pem"), 0o600))
	_, err = gatewayClient(empty)
	require.ErrorContains(t, err, "no PEM certificate")
}

func TestCheckGateway_readsEveryNodeOverTLSWithTheGivenCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chain/query/orama.nodes.v1.Query/Node", r.URL.Path)
		id := r.URL.Query().Get("json")
		switch {
		case id == `{"node_id":"stagenet-athena"}`:
			_, _ = w.Write([]byte(`{"node":{"node_id":"stagenet-athena","operator":"orama1a"}}`))
		case id == `{"node_id":"stagenet-superman"}`:
			_, _ = w.Write([]byte(`{"node":{"node_id":"stagenet-superman","operator":"orama1b"}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	cert := srv.TLS.Certificates[0].Certificate[0]
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0o600))

	e := &env{gateway: srv.URL, caFile: ca, nodes: []nodeRef{{Name: "athena"}, {Name: "superman"}}}
	r := checkGateway(context.Background(), e)
	require.Equal(t, Pass, r.Status, r.Detail)

	e.nodes = append(e.nodes, nodeRef{Name: "poseidon"})
	r = checkGateway(context.Background(), e)
	require.Equal(t, Fail, r.Status)
	require.Contains(t, r.Detail, "HTTP 404")

	e.caFile = ""
	r = checkGateway(context.Background(), e)
	require.Equal(t, Fail, r.Status, "a certificate no trusted CA signed is refused")
}
