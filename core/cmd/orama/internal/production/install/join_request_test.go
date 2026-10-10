package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
)

const (
	joinSite   = "cluster.example"
	validWGKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

// caddyLike answers the way a node's Caddy does: the gateway's JSON for a Host
// it has a site for, and an empty 200 for any other Host.
func caddyLike(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != joinSite {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(joinhandlers.JoinResponse{WGIP: "10.0.0.2",
			ArchiveSigners: []string{testSigner}, ArchiveSignersRotatedAt: "2026-09-20T10:00:00Z"})
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return srv, hex.EncodeToString(sum[:])
}

func joinOrchestrator(addr, fingerprint, sni string) *Orchestrator {
	return &Orchestrator{flags: &Flags{
		Token:         "invite-token",
		VpsIP:         "203.0.113.7",
		JoinAddress:   addr,
		CAFingerprint: fingerprint,
		JoinSNI:       sni,
	}}
}

// The invite reaches the minting node by IP, so the URL's host is an IP. Caddy
// routes by Host, not SNI, and had no site for the IP: the join got an empty
// 200 and failed with "unexpected end of JSON input".
func TestCallJoinEndpoint_sendsTheInviteSiteAsHost(t *testing.T) {
	srv, fingerprint := caddyLike(t)

	resp, err := joinOrchestrator(srv.URL, fingerprint, joinSite).callJoinEndpoint(validWGKey, "")
	if err != nil {
		t.Fatalf("callJoinEndpoint: %v", err)
	}
	if resp.WGIP != "10.0.0.2" {
		t.Errorf("WGIP = %q, want the gateway's answer", resp.WGIP)
	}
	if len(resp.ArchiveSigners) != 1 || resp.ArchiveSigners[0] != testSigner || resp.ArchiveSignersRotatedAt == "" {
		t.Errorf("the join response's archive signers were lost: %+v", resp)
	}
}

func TestCallJoinEndpoint_saysWhenTheGatewayNeverSawIt(t *testing.T) {
	srv, fingerprint := caddyLike(t)

	_, err := joinOrchestrator(srv.URL, fingerprint, "").callJoinEndpoint(validWGKey, "")
	if err == nil {
		t.Fatal("an empty 200 from a Host with no site was taken as a join")
	}
	if !strings.Contains(err.Error(), "did not reach the Orama gateway") {
		t.Errorf("the error does not say the request missed the gateway: %v", err)
	}
}

func TestNewJoinRequest_leavesHostAloneWithoutAServerName(t *testing.T) {
	req, err := newJoinRequest("https://198.51.100.1/v1/internal/join", "", []byte("{}"))
	if err != nil {
		t.Fatalf("newJoinRequest: %v", err)
	}
	if req.Host != "198.51.100.1" {
		t.Errorf("Host = %q, want the URL's host", req.Host)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestNewJoinRequest_refusesAnUnparsableURL(t *testing.T) {
	if _, err := newJoinRequest("https://[::1/v1/internal/join", joinSite, nil); err == nil {
		t.Error("an unparsable join address was accepted")
	}
}

// The minting node checks the expectation before it spends the invite, so the
// request has to carry it.
func TestCallJoinEndpoint_sendsTheExpectedArchiveSigners(t *testing.T) {
	var got joinhandlers.JoinRequest
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(joinhandlers.JoinResponse{WGIP: "10.0.0.2"})
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	o := joinOrchestrator(srv.URL, hex.EncodeToString(sum[:]), "")
	o.flags.expectedArchiveSigners = []string{testSigner}

	if _, err := o.callJoinEndpoint(validWGKey, ""); err != nil {
		t.Fatalf("callJoinEndpoint: %v", err)
	}
	if len(got.ExpectedArchiveSigners) != 1 || got.ExpectedArchiveSigners[0] != testSigner {
		t.Fatalf("the join request carried %v", got.ExpectedArchiveSigners)
	}
}
