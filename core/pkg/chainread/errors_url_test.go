package chainread

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// closedServerURL is the address of a server that has been shut down, so a
// request to it fails at the dial.
func closedServerURL() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func assertErrorHasNoQuery(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a closed server answered")
	}
	for _, leaked := range []string{"SECRET-QUERY-VALUE", "api_key"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the error quotes the query (%q): %v", leaked, err)
		}
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the error lost its cause: %v", err)
	}
}

// The error of a failed read reaches the gateway log (a failed chain query is
// logged) and the CLI's output. The HTTP client quotes the request URL in it,
// query string included.
func TestRESTGet_transportErrorDropsTheQueryString(t *testing.T) {
	r := &Reader{REST: closedServerURL()}
	_, err := r.RESTGet(context.Background(), "/cosmos/bank/v1beta1/balances/orama1x?api_key=SECRET-QUERY-VALUE")
	assertErrorHasNoQuery(t, err)
	if !strings.Contains(err.Error(), "/cosmos/bank/v1beta1/balances/orama1x") {
		t.Errorf("the error lost the path: %v", err)
	}
}

func TestGatewayPostTx_transportErrorDropsTheQueryString(t *testing.T) {
	r := &Reader{Gateway: closedServerURL() + "?api_key=SECRET-QUERY-VALUE"}
	_, err := r.gatewayPostTx(context.Background(), simulateRoute, []byte{1})
	assertErrorHasNoQuery(t, err)
}

func TestRESTGet_cancelledContextStaysMatchable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Reader{REST: closedServerURL()}).RESTGet(ctx, "/x?api_key=SECRET-QUERY-VALUE")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cause was lost: %v", err)
	}
	if strings.Contains(err.Error(), "SECRET-QUERY-VALUE") {
		t.Errorf("the error quotes the query: %v", err)
	}
}

// A URL the client refuses to build a request for is quoted in the parse error
// too, query string included.
func TestRESTGet_unbuildableURLErrorDropsTheQueryString(t *testing.T) {
	r := &Reader{REST: "http://127.0.0.1:1"}
	_, err := r.RESTGet(context.Background(), "/x?api_key=SECRET-QUERY-VALUE\x7f")
	if err == nil {
		t.Fatal("an unbuildable URL was requested")
	}
	if strings.Contains(err.Error(), "SECRET-QUERY-VALUE") || strings.Contains(err.Error(), "api_key") {
		t.Errorf("the error quotes the query: %v", err)
	}
}

func TestGatewayPostTx_unbuildableURLErrorDropsTheQueryString(t *testing.T) {
	r := &Reader{Gateway: "http://127.0.0.1:1?api_key=SECRET-QUERY-VALUE\x7f"}
	_, err := r.gatewayPostTx(context.Background(), simulateRoute, []byte{1})
	if err == nil {
		t.Fatal("an unbuildable URL was requested")
	}
	if strings.Contains(err.Error(), "SECRET-QUERY-VALUE") || strings.Contains(err.Error(), "api_key") {
		t.Errorf("the error quotes the query: %v", err)
	}
}
