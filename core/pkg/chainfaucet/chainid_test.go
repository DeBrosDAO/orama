package chainfaucet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func nodeInfoServer(t *testing.T, status int, body string) RESTChainID {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != nodeInfoPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return RESTChainID{Base: srv.URL}
}

func TestRESTChainID_readsTheNodesNetwork(t *testing.T) {
	ids := nodeInfoServer(t, http.StatusOK, `{"default_node_info":{"network":"orama-stagenet-6","moniker":"x"}}`)
	got, err := ids.ChainID(context.Background())
	if err != nil || got != "orama-stagenet-6" {
		t.Fatalf("ChainID = %q, %v", got, err)
	}
}

func TestRESTChainID_refusesWhatIsNotAChainID(t *testing.T) {
	for name, body := range map[string]string{
		"none":           `{"default_node_info":{}}`,
		"empty":          `{"default_node_info":{"network":""}}`,
		"with a space":   `{"default_node_info":{"network":"orama stagenet"}}`,
		"with an escape": `{"default_node_info":{"network":"orama-\u001b[2J-1"}}`,
		"too long":       `{"default_node_info":{"network":"` + strings.Repeat("a", 65) + `"}}`,
		"not json":       `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			ids := nodeInfoServer(t, http.StatusOK, body)
			if _, err := ids.ChainID(context.Background()); err == nil {
				t.Fatal("ChainID accepted it")
			}
		})
	}
}

func TestRESTChainID_aNodeThatIsDownOrFailingIsAnError(t *testing.T) {
	if _, err := nodeInfoServer(t, http.StatusInternalServerError, `{"message":"boom"}`).ChainID(context.Background()); err == nil {
		t.Error("an HTTP 500 gave a chain id")
	}
	if _, err := (RESTChainID{Base: "http://127.0.0.1:1"}).ChainID(context.Background()); err == nil {
		t.Error("a node that cannot be reached gave a chain id")
	}
}
