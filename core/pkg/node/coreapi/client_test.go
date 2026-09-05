package coreapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
)

const (
	testSecret = "cluster-secret-for-tests"
	testNodeID = "12D3KooWEyoppNCUx8Yx66oV9fJnriXwCcXwDDUA2kj6vnc6iDEg"
)

// verifying is a gateway that checks the stamp the way the real handler does,
// so these tests prove the client and the handler agree about what is signed
// rather than proving the client agrees with itself.
func verifying(t *testing.T, answer func(w http.ResponseWriter, nodeID string, body []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		nodeID, ok := auth.VerifyNodeAPI(auth.SharedClusterKey(testSecret), r, body, time.Now())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		answer(w, nodeID, body)
	}))
}

func client(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(baseURL, testNodeID, testSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// A registration the client sends is one the gateway accepts, names this node,
// and carries the fields as sent.
func TestRegister_isAcceptedAndNamesThisNode(t *testing.T) {
	var seenNode string
	var seen nodeapi.RegisterRequest

	srv := verifying(t, func(w http.ResponseWriter, nodeID string, body []byte) {
		seenNode = nodeID
		if err := json.Unmarshal(body, &seen); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	sent := nodeapi.RegisterRequest{
		IPAddress:  "203.0.113.7",
		InternalIP: "10.0.0.7",
		Region:     "local",
		SSHUser:    "orama",
	}
	if err := client(t, srv.URL).Register(context.Background(), sent); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if seenNode != testNodeID {
		t.Errorf("the gateway read the node as %q, want %q", seenNode, testNodeID)
	}
	if seen != sent {
		t.Errorf("the gateway received %+v, want %+v", seen, sent)
	}
}

// The client signs what it sends. A stamp over a different body would be
// refused by the real handler, and this is what catches a future change that
// signs before the body is final.
func TestRegister_theStampCoversTheBodyThatIsSent(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	if err := client(t, srv.URL).Register(context.Background(), nodeapi.RegisterRequest{
		IPAddress:  "203.0.113.7",
		InternalIP: "10.0.0.7",
		Region:     "local",
	}); err != nil {
		t.Fatalf("a request the client signed was refused by a gateway checking the stamp: %v", err)
	}
}

// The heartbeat reports what the gateway said about the row.
func TestHeartbeat_reportsWhetherTheRowExists(t *testing.T) {
	for _, registered := range []bool{true, false} {
		srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nodeapi.HeartbeatResponse{Registered: registered})
		})

		got, err := client(t, srv.URL).Heartbeat(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		if got != registered {
			t.Errorf("Heartbeat = %v, want %v", got, registered)
		}
	}
}

// A refusal is surfaced with what the gateway said and where it was called, so
// the log line says what to fix rather than "request failed".
func TestPost_aRefusalSaysWhatAndWhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := client(t, srv.URL).Register(context.Background(), nodeapi.RegisterRequest{})
	if err == nil {
		t.Fatal("a refused registration was reported as success")
	}
	for _, want := range []string{"/v1/internal/node/register", srv.URL, "401", "unauthorized"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// An answer that is not the shape the gateway promises is an error, not a
// silent false.
func TestHeartbeat_anUnreadableAnswerIsReported(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		_, _ = w.Write([]byte("not json"))
	})
	defer srv.Close()

	if _, err := client(t, srv.URL).Heartbeat(context.Background()); err == nil {
		t.Error("an unreadable answer was reported as a heartbeat result")
	}
}

// A gateway that cannot be reached at all names the address it tried, because
// the fix is almost always that the local gateway is not up.
func TestPost_anUnreachableGatewayNamesTheAddress(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {})
	url := srv.URL
	srv.Close()

	err := client(t, url).Register(context.Background(), nodeapi.RegisterRequest{})
	if err == nil {
		t.Fatal("a call to a gateway that is not there succeeded")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("the error does not name the address it tried: %v", err)
	}
}

// A cancelled context stops the call rather than blocking the heartbeat loop.
func TestPost_respectsACancelledContext(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := client(t, srv.URL).Register(ctx, nodeapi.RegisterRequest{}); err == nil {
		t.Error("a call with a cancelled context succeeded")
	}
}

// A client that cannot sign is refused at construction rather than one request
// at a time, in a warning log.
func TestNew_refusesAClientThatCouldNotSign(t *testing.T) {
	cases := map[string]struct{ url, node, secret string }{
		"no gateway address": {"", testNodeID, testSecret},
		"no node id":         {"http://127.0.0.1:1", "  ", testSecret},
		"no cluster secret":  {"http://127.0.0.1:1", testNodeID, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(c.url, c.node, c.secret); err == nil {
				t.Errorf("a client with %s was built", name)
			}
		})
	}
}

// A trailing slash on the gateway address must not produce a double slash in
// the path, which is a different route and would 404.
func TestNew_toleratesATrailingSlashOnTheAddress(t *testing.T) {
	var path string
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		inner.ServeHTTP(w, r)
	})

	c, err := New(srv.URL+"/", testNodeID, testSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Register(context.Background(), nodeapi.RegisterRequest{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if path != "/v1/internal/node/register" {
		t.Errorf("called %q, want /v1/internal/node/register", path)
	}
}
