package enroll

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// fakeAgent is an OramaOS node's enrollment listener: it requires the
// registration code, opens the payload under it, and seals its answer.
func fakeAgent(t *testing.T, code, agentToken string, received *EnrollResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderEnrollmentCode) != code {
			http.Error(w, "registration code mismatch", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		plaintext, err := Open(code, string(raw))
		if err != nil {
			http.Error(w, "did not decrypt", http.StatusBadRequest)
			return
		}
		if received != nil {
			if err := json.Unmarshal(plaintext, received); err != nil {
				t.Errorf("the node could not parse the payload: %v", err)
			}
		}
		sealed, err := Seal(code, []byte(`{"status":"ok","agent_token":"`+agentToken+`"}`))
		if err != nil {
			http.Error(w, "seal failed", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, sealed)
	}))
}

const pushCode = "a1b2c3d4e5f60718293a"

// The payload carries the cluster secret. It used to be plaintext JSON over
// HTTP on the node's public IP.
func TestEnrollmentPush_theClusterSecretIsNotOnTheWire(t *testing.T) {
	var received EnrollResponse
	agent := fakeAgent(t, pushCode, "node-token", &received)
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	token, err := h.pushConfigTo(agent.URL, pushCode, &EnrollResponse{
		NodeID:        "node-10.0.0.4",
		ClusterSecret: "the-cluster-secret",
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if received.ClusterSecret != "the-cluster-secret" {
		t.Errorf("the node received cluster secret %q", received.ClusterSecret)
	}
	if token != "node-token" {
		t.Errorf("agent token %q", token)
	}
}

// A gateway that does not hold the registration code cannot configure the node,
// which is what stops anyone who reaches it first from enrolling it into
// another cluster.
func TestEnrollmentPush_theWrongCodeIsRefused(t *testing.T) {
	agent := fakeAgent(t, pushCode, "node-token", nil)
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	if _, err := h.pushConfigTo(agent.URL, "00000000000000000000",
		&EnrollResponse{NodeID: "node-10.0.0.4"}); err == nil {
		t.Fatal("a node accepted configuration from a caller with the wrong code")
	}
}

// The node's answer is sealed too, so the credential it mints is not readable
// by anyone watching the exchange.
func TestEnrollmentPush_theAgentTokenIsNotOnTheWire(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if _, err := Open(pushCode, string(raw)); err != nil {
			http.Error(w, "did not decrypt", http.StatusBadRequest)
			return
		}
		sealed, _ := Seal(pushCode, []byte(`{"status":"ok","agent_token":"the-node-token"}`))
		if strings.Contains(sealed, "the-node-token") {
			t.Error("the token is readable in what the node sends back")
		}
		_, _ = io.WriteString(w, sealed)
	}))
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	token, err := h.pushConfigTo(agent.URL, pushCode, &EnrollResponse{NodeID: "n"})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if token != "the-node-token" {
		t.Errorf("token %q", token)
	}
}

// The node id the token is stored against has to be the one HandleEnroll built,
// or the proxy looks up a row that does not exist and no node can be commanded.
func TestNodeIDForOverlayIP_matchesWhatEnrollmentWrites(t *testing.T) {
	if got := nodeIDForOverlayIP("10.0.0.4"); got != "node-10.0.0.4" {
		t.Errorf("node id %q, want node-10.0.0.4 — HandleEnroll builds it as "+
			`fmt.Sprintf("node-%%s", wgIP)`, got)
	}
}

// A node that returns no credential can never be commanded, so accepting an
// empty one would hide the failure until the first restart request.
func TestEnrollmentPush_refusesAnEmptyAgentToken(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if _, err := Open(pushCode, string(raw)); err != nil {
			http.Error(w, "did not decrypt", http.StatusBadRequest)
			return
		}
		sealed, _ := Seal(pushCode, []byte(`{"status":"ok","agent_token":"  "}`))
		_, _ = io.WriteString(w, sealed)
	}))
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	if _, err := h.pushConfigTo(agent.URL, pushCode, &EnrollResponse{NodeID: "n"}); err == nil {
		t.Fatal("enrollment reported success with no agent token")
	}
}

// The node's answer is opened under the code, which is what proves the thing
// that answered is the node the operator read the code from and not something
// that got in front of it.
func TestEnrollmentPush_refusesAnAnswerItCannotOpen(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","agent_token":"impostor"}`)
	}))
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	token, err := h.pushConfigTo(agent.URL, pushCode, &EnrollResponse{NodeID: "n"})
	if err == nil {
		t.Fatalf("an unsealed answer was accepted, yielding token %q", token)
	}
}

// The endpoint the real code builds has to be the one the agent listens on.
func TestPushConfigToNode_targetsTheEnrollmentPort(t *testing.T) {
	var gotPath string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer agent.Close()

	h := &Handler{logger: zap.NewNop()}
	_, _ = h.pushConfigTo(agent.URL+"/v1/agent/enroll/complete", pushCode, &EnrollResponse{})

	if gotPath != "/v1/agent/enroll/complete" {
		t.Errorf("pushed to %q", gotPath)
	}
}
