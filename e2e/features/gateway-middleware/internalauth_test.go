//go:build e2e_fleet

package gatewaymiddleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// dropLog is what the internal-auth gate logs when it deletes headers that
// arrived without a valid MAC (core/pkg/gateway/internal_auth_hop.go).
const dropLog = "dropped unauthenticated internal-auth headers"

// journalLag bounds how long a log line takes to reach journald.
const journalLag = 30 * time.Second

// forged is every X-Internal-Auth-* header a namespace gateway would believe
// on a valid hop — the namespace, the subject, admin scopes, token times —
// with MACs that are not valid (core/pkg/gateway/internal_auth_hop.go).
func forged(namespace, subject string) http.Header {
	now := fmt.Sprint(time.Now().Unix())
	h := http.Header{}
	for k, v := range map[string]string{
		"X-Internal-Auth-Validated": "true", "X-Internal-Auth-Namespace": namespace,
		"X-Internal-Auth-Jwt-Sub": subject, "X-Internal-Auth-Jwt-Custom": `{"role":"owner"}`,
		"X-Internal-Auth-Scopes": "admin", "X-Internal-Auth-Jwt-Exp": fmt.Sprint(time.Now().Add(time.Hour).Unix()),
		"X-Internal-Auth-Jwt-Iat": now, "X-Internal-Auth-Jwt-Jti": "e2e-forged", "X-Internal-Auth-Jwt-Sid": "1",
		"X-Internal-Auth-Jwt-Did": "e2e", "X-Internal-Auth-Mac": now + "." + strings.Repeat("00", 32),
		"X-Internal-Auth-Mac-V2": now + "." + strings.Repeat("ab", 32), "X-Internal-Auth-Mac-V3": "garbage",
	} {
		h.Set(k, v)
	}
	h.Set("Content-Type", "application/json")
	return h
}

// TestInternalAuth_forgedHeadersGrantNothingFromTheInternet: the headers
// that tell a namespace gateway "the cluster gateway validated this caller as
// the owner, with admin" are deleted by the first middleware unless a valid
// MAC came with them, so from the internet they are exactly no credential:
// 401 AUTH_MISSING on the namespace host and on the cluster gateway
// (docs/whitepaper/technical-reference/vol1/12-gateway-architecture.md "Inter-gateway trust"; Caddy strips six of them as a
// second layer, the gateway all of them).
func TestInternalAuth_forgedHeadersGrantNothingFromTheInternet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	calls := []gw.Req{
		{Method: http.MethodPost, Path: "/v1/rqlite/query", Body: []byte(`{"sql":"SELECT 1"}`)},
		{Method: http.MethodGet, Path: "/v1/namespace/list"},
		{Method: http.MethodGet, Path: "/v1/auth/whoami"},
	}
	for _, c := range []*gw.Client{n.Client, harness.GW(t)} {
		for _, req := range calls {
			req.Header = forged(n.Name, n.Owner.Wallet.Address())
			resp := c.MustSend(t, req)
			if resp.Status != http.StatusUnauthorized || resp.ErrorCode() != tenancy.CodeMissing {
				t.Errorf("%s%s with forged internal-auth headers: %d %q, want 401 %s: %.200s",
					c.BaseURL, req.Path, resp.Status, resp.ErrorCode(), tenancy.CodeMissing, resp.Body)
			}
		}
	}
}

// TestInternalAuth_forgedHeadersLoggedAndDropped: the node's gateway records
// that it dropped them — the headers reached the gate and were removed there,
// not merely ignored further down. The request is sent from the node's shell
// to its loopback gateway: from the internet Caddy strips
// X-Internal-Auth-Validated first (core/pkg/install/installers/caddy.go), and
// the gate logs only when that header arrives (internal_auth_hop.go).
func TestInternalAuth_forgedHeadersLoggedAndDropped(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	node := f.State.Nodes[0]
	start := time.Now().Add(-time.Second)
	p := edge.NodeCurl{URL: edge.LocalGateway(gw.PathWhoami),
		Headers: curlHeaders(forged(gw.LobbyNamespace, "0x0000000000000000000000000000000000000001"))}.Run(t, f, node)
	if p.Exit != 0 || p.Status != http.StatusUnauthorized {
		t.Fatalf("%s: whoami on loopback with forged internal-auth headers: HTTP %d (curl exit %d), want 401: %.200s %s",
			node.Name, p.Status, p.Exit, p.Body, p.Stderr)
	}
	eventually.Require(t, time.Second, journalLag, node.Name+"'s gateway to log the drop", func() (bool, error) {
		if strings.Contains(f.Journal(t, node, edge.IndexGatewayUnit, start), dropLog) {
			return true, nil
		}
		return false, fmt.Errorf("no %q in the journal since %s", dropLog, start.Format(time.RFC3339))
	})
}

// TestInternalAuth_forgedHeadersGrantNothingOnTheNode: the same from a node
// shell — loopback to the cluster gateway, and the namespace gateway on its
// WireGuard address from another node, where the old source-IP check would
// have believed them. The source address is not consulted.
func TestInternalAuth_forgedHeadersGrantNothingOnTheNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	to := tenancy.Members(t, f, n.Name)[0]
	from := f.State.Nodes[0]
	if from.Name == to.Name {
		from = f.State.Nodes[1]
	}
	headers := curlHeaders(forged(n.Name, n.Owner.Wallet.Address()))
	port := namespaceGatewayPort(t, n.Name, to)
	for what, url := range map[string]string{
		"cluster gateway on loopback":   edge.LocalGateway("/v1/rqlite/query"),
		"namespace gateway via overlay": fmt.Sprintf("http://%s:%d/v1/rqlite/query", to.WGIP, port),
	} {
		p := edge.NodeCurl{Method: http.MethodPost, URL: url, Headers: headers, Body: `{"sql":"SELECT 1"}`}.Run(t, f, from)
		if p.Status != http.StatusUnauthorized && p.Status != http.StatusForbidden {
			t.Errorf("%s with forged internal-auth headers: %d, want 401/403: %.200s", what, p.Status, p.Body)
		}
	}
}

// curlHeaders renders h as NodeCurl headers, "Key: value".
func curlHeaders(h http.Header) []string {
	out := make([]string, 0, len(h))
	for k, v := range h {
		out = append(out, k+": "+v[0])
	}
	return out
}

// namespaceGatewayPort is the port name's gateway listens on at n's
// WireGuard address, from its YAML's listen_addr (only that key is read:
// the YAML holds credentials).
func namespaceGatewayPort(t *testing.T, name string, n fleet.Node) int {
	t.Helper()
	f := harness.Fleet(t)
	cfg := tenancy.NamespacesDir + "/" + name + "/configs/gateway-*.yaml"
	out := f.MustExec(t, n, "grep -hE '^listen_addr:' "+cfg).Stdout
	line := strings.Trim(strings.TrimSpace(out), `"'`)
	port, err := strconv.Atoi(strings.Trim(line[strings.LastIndex(line, ":")+1:], `"' `))
	if err != nil || port <= 0 {
		t.Fatalf("%s: cannot read %s's gateway port from %q", n.Name, name, out)
	}
	return port
}
