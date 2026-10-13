//go:build e2e_fleet

package oramaos

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const pathEnroll = "/v1/node/enroll"

// enrollBody is `orama maint node enroll`'s request (core/pkg/gateway/handlers/enroll
// EnrollRequest), with a token no invite ever had.
func enrollBody(t *testing.T, nodeIP string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"code": strings.Repeat("ab", codeHex/2), "token": "e2e-not-an-invite", "node_ip": nodeIP})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestOramaOSEnroll_nodeIPMustBePublicIPv4: the gateway stores node_ip as
// every other node's WireGuard Endpoint and pushes the configuration to it,
// so anything but a public IPv4 is refused (400) before the invite is looked
// at; a public address with a token no invite has is 401 and pushes nothing
// (website/src/docs/operator/orama-os.mdx "Step 3"; core/pkg/gateway/handlers/enroll
// handler.go, nodeip.go).
func TestOramaOSEnroll_nodeIPMustBePublicIPv4(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, ip := range []string{"127.0.0.1", "10.0.0.9", "192.168.1.1", "172.16.0.1", "100.64.0.1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "::1", "2001:db8::1", "1.2.3.4\nEndpoint = 6.6.6.6", "not-an-ip"} {
		r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathEnroll, Header: http.Header{"Content-Type": {"application/json"}}, Body: enrollBody(t, ip)})
		if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "node_ip") {
			t.Errorf("node_ip %q: HTTP %d %.200s, want 400 naming node_ip", ip, r.Status, r.Body)
		}
	}
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathEnroll, Header: http.Header{"Content-Type": {"application/json"}}, Body: enrollBody(t, "203.0.113.7")})
	if r.Status != http.StatusUnauthorized {
		t.Errorf("a public node_ip with no invite: HTTP %d %.200s, want 401", r.Status, r.Body)
	}
	for name, body := range map[string][]byte{"empty": []byte(`{}`), "not json": []byte("{"), "no token": []byte(`{"code":"x","node_ip":"203.0.113.7"}`)} {
		if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathEnroll, Body: body}); r.Status != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", name, r.Status)
		}
	}
}

// TestOramaOSNodeRoutes_refuseNonOperators: status, logs, command and leave
// reach a node's agent and can stop it or drop it from the mesh; with no
// credential, or as a signed-in wallet that is not an operator, each is
// refused before anything is proxied (website/src/docs/operator/orama-os.mdx "Node
// Management"; core/pkg/gateway/route_policy.go operator domain).
func TestOramaOSNodeRoutes_refuseNonOperators(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	wallet := gw.NewUser(t, f, gw.LobbyNamespace)
	// An overlay address no node holds: were a refusal missing, the leave and
	// command would still reach nothing of the fleet.
	const target = "10.0.0.250"
	reqs := []gw.Req{
		{Path: "/v1/node/status", Query: map[string][]string{"wg_ip": {target}}},
		{Path: "/v1/node/logs", Query: map[string][]string{"wg_ip": {target}, "service": {"gateway"}}},
		{Method: http.MethodPost, Path: "/v1/node/command", Query: map[string][]string{"wg_ip": {target}}, Body: []byte(`{"action":"restart","service":"rqlite"}`)},
		{Method: http.MethodPost, Path: "/v1/node/leave", Body: []byte(`{"wg_ip":"` + target + `"}`)},
	}
	for _, req := range reqs {
		for who, bearer := range map[string]string{"nobody": "", "a wallet": wallet.Token()} {
			req.Bearer = bearer
			if req.Body != nil {
				req.Header = http.Header{"Content-Type": {"application/json"}}
			}
			tenancy.ExpectDenied(t, c.MustSend(t, req), who+" at "+req.Path)
		}
	}
}

// TestOramaOS_luksShamirUnlockAndABUpdates records what a single VM cannot
// reach, so the report shows it uncovered rather than silently absent: the
// Shamir unlock needs K = max(2, floor(N/3)) peer vault-guardians on the
// overlay, and enrollment of a node with no peers fails ("no peers available
// for key distribution", website/src/docs/operator/orama-os.mdx "Genesis Node"); an A/B
// update is fetched only from https://updates.orama.network/v1/latest
// (os/agent/internal/update/manager.go UpdateURL), which the harness cannot
// serve a signed test release on.
func TestOramaOS_luksShamirUnlockAndABUpdates(t *testing.T) {
	harness.Fleet(t)
	harness.SkipNotApplicable(t, "needs an enrolled OramaOS cluster of K+ nodes (Shamir unlock) and an update-URL override in os/agent (A/B signature and 3-boot rollback); neither exists")
}
