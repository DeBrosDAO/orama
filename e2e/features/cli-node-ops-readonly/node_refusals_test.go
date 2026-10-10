//go:build e2e_fleet

package clinodeopsreadonly

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Enrollment (core/pkg/gateway/handlers/enroll/handler.go).
const (
	enrollPath = "/v1/node/enroll"
	// enrollBodyLimit is the handler's MaxBytesReader.
	enrollBodyLimit = 1 << 20
	// garbageCode and garbageToken were never issued by the cluster.
	garbageCode  = "E2E-NOT-A-CODE"
	garbageToken = "e2e-not-an-invite-token"
)

// TestNodeUninstall_refusedOffANode: uninstall needs root and asks before
// removing anything (production/uninstall Handle). The runner is not a node:
// as a user it is refused as a usage error; as root, with no answer on
// stdin, the confirmation is declined (exit 7). Either way nothing happens.
func TestNodeUninstall_refusedOffANode(t *testing.T) {
	t.Parallel()
	res := run(t, harness.CLI(t).NoWallet(t), "node", "uninstall")
	if os.Geteuid() == 0 {
		infra.ExpectExit(t, res, infra.ExitAborted)
		return
	}
	infra.ExpectExit(t, res, exitUsage, "must be run as root")
}

// TestNodeUnlock_refusals: unlock needs --genesis, --node-ip and a readable,
// non-empty --key-file; each missing piece is refused before any decryption
// or network call (production/unlock Run).
func TestNodeUnlock_refusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.key")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cli := harness.CLI(t).NoWallet(t)
	cases := []struct {
		args []string
		exit int
		want string
	}{
		{[]string{"--node-ip", "10.0.0.9"}, exitUsage, "key-file"},
		{[]string{"--key-file", empty, "--genesis"}, exitUsage, "--node-ip is required"},
		{[]string{"--node-ip", "10.0.0.9", "--key-file", empty}, exitUsage, "--genesis is required"},
		{[]string{"--node-ip", "10.0.0.9", "--key-file", filepath.Join(dir, "absent.key"), "--genesis"}, exitNotFound, "could not read the key file"},
		{[]string{"--node-ip", "10.0.0.9", "--key-file", empty, "--genesis"}, exitUsage, "is empty"},
	}
	for _, c := range cases {
		res := run(t, cli, append([]string{"node", "unlock"}, c.args...)...)
		infra.ExpectExit(t, res, c.exit, c.want)
	}
}

// TestNodeEnroll_refusals: every flag is required (a missing one is usage),
// and a code and invite token the cluster never issued are refused by the
// gateway, enrolling nothing.
func TestNodeEnroll_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t).NoWallet(t)
	full := map[string]string{"--node-ip": f.State.Nodes[0].PublicIP, "--code": garbageCode,
		"--token": garbageToken, "--gateway": f.State.GatewayURL}
	for missing := range full {
		var args []string
		for k, v := range full {
			if k != missing {
				args = append(args, k, v)
			}
		}
		res := run(t, cli, append([]string{"node", "enroll"}, args...)...)
		infra.ExpectExit(t, res, exitUsage, strings.TrimPrefix(missing, "--"))
	}
	var args []string
	for k, v := range full {
		args = append(args, k, v)
	}
	res := run(t, cli, append([]string{"node", "enroll"}, args...)...)
	infra.ExpectRefused(t, res, "enrollment failed")
}

// TestNodeEnroll_plainHTTPGatewayRefused: an invite token is a credential, so
// the CLI must not send it to a gateway over plain http.
func TestNodeEnroll_plainHTTPGatewayRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := run(t, harness.CLI(t).NoWallet(t), "node", "enroll", "--node-ip", f.State.Nodes[0].PublicIP,
		"--code", garbageCode, "--token", garbageToken, "--gateway", "http://"+f.State.BaseDomain)
	infra.ExpectExit(t, res, exitUsage, "https")
}

// TestNodeEnrollRoute_refusesBadRequests: the enrollment route is public
// (the node has no credential yet), so it validates everything itself:
// method, body, required fields, a public IPv4 node address and the invite
// token, and never answers a garbage request with a server error.
func TestNodeEnrollRoute_refusesBadRequests(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	jsonCT := http.Header{"Content-Type": {"application/json"}}
	body := func(ip, token string) []byte {
		return []byte(`{"code":"` + garbageCode + `","token":"` + token + `","node_ip":"` + ip + `"}`)
	}
	public := f.State.Nodes[0].PublicIP
	cases := []struct {
		label string
		req   gw.Req
		want  int
	}{
		{"wrong method", gw.Req{Method: http.MethodGet, Path: enrollPath}, http.StatusMethodNotAllowed},
		{"not json", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: []byte("{")}, http.StatusBadRequest},
		{"empty object", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: []byte("{}")}, http.StatusBadRequest},
		{"not an ip", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body("not-an-ip", garbageToken)}, http.StatusBadRequest},
		{"overlay ip", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body("10.0.0.1", garbageToken)}, http.StatusBadRequest},
		{"loopback", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body("127.0.0.1", garbageToken)}, http.StatusBadRequest},
		{"ipv6", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body("2001:db8::1", garbageToken)}, http.StatusBadRequest},
		{"garbage token", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body(public, garbageToken)}, http.StatusUnauthorized},
		{"hostile token", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT, Body: body(public, "' OR 1=1 --\u202e")}, http.StatusUnauthorized},
		{"over the limit", gw.Req{Method: http.MethodPost, Path: enrollPath, Header: jsonCT,
			Body: append(append([]byte(`{"code":"`), bytes.Repeat([]byte("A"), enrollBodyLimit+1)...), `"}`...)}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if resp := c.MustSend(t, tc.req); resp.Status != tc.want {
			t.Errorf("%s: POST %s answered %d, want %d: %.200s", tc.label, enrollPath, resp.Status, tc.want, resp.Body)
		}
	}
}

// TestNodeMigrateConf_refusedWithoutCredential: migrate-conf registers nodes
// with the wallet through the gateway and needs `orama auth login` first
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-node-migrate-conf); an environment that is not
// configured is refused. Neither registers anything.
func TestNodeMigrateConf_refusedWithoutCredential(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t).Isolated(t)
	res := run(t, cli, "node", "migrate-conf", "--env", f.State.Env)
	if res.Exit != exitAuth || !strings.Contains(output(res), "orama auth login") {
		t.Errorf("migrate-conf with no credential: exit %d, want %d\n%s", res.Exit, exitAuth, output(res))
	}
	infra.ExpectRefused(t, run(t, cli, "node", "migrate-conf", "--env", "e2e-cli-absent"))
}
