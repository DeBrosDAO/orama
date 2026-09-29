//go:build e2e_fleet

package deployments

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	deploymentsDir = "/opt/orama/.orama/data/deployments"
	// bindInUse is the kernel refusing a port that is taken (EADDRINUSE).
	bindInUse = "address already in use"
	// bindDenied is the SocketBindDeny cgroup hook refusing a bind (EPERM).
	bindDenied = "operation not permitted"
)

// TestDeploySandbox_fromInside: a hostile app, asking from inside its own
// process, runs as its own non-root user, cannot read node secrets, another
// deployment, the node's config or PID 1's environment, cannot write its own
// code, can write its state directory, cannot bind any port but PORT, and
// cannot reach the WireGuard network (docs/DEPLOYMENT_GUIDE.md "What your app
// runs as"; docs/SECURITY.md "Tenant deployments").
func TestDeploySandbox_fromInside(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "sb"), "sandbox")
	other := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "sb2"), "neighbour")
	c, oc := tn.app(u), tn.app(other)
	serving(t, c, "/health", "")
	serving(t, oc, "/health", "")
	uid, otherUID := probe(t, c, "/uid", nil).Detail, probe(t, oc, "/uid", nil).Detail
	if strings.HasPrefix(uid, "0 ") || uid == otherUID {
		t.Errorf("uids: app %q, neighbour %q; want non-root and distinct", uid, otherUID)
	}
	tn.checkFiles(t, c)
	tn.checkNetwork(t, c)
}

// checkFiles: the app writes and reads back its state directory (the
// control: /read and /write work in the sandbox), and reads or writes nothing
// of the node's, a neighbour's or its own code.
func (tn *tenant) checkFiles(t testing.TB, c *gw.Client) {
	t.Helper()
	state := probe(t, c, "/getenv", url.Values{"k": {"ORAMA_STATE_DIR"}}).Detail
	if a := probe(t, c, "/write", url.Values{"p": {state + "/probe"}}); !a.OK {
		t.Fatalf("the app cannot write its state directory %s: %s", state, a.Detail)
	}
	if a := probe(t, c, "/read", url.Values{"p": {state + "/probe"}}); !a.OK {
		t.Fatalf("the app cannot read back what it wrote in %s, so a refused read proves nothing: %s", state, a.Detail)
	}
	for _, p := range []string{"/opt/orama/.orama/secrets/cluster-secret", "/opt/orama/.orama/configs/node.yaml",
		"/opt/orama/.orama/data/identity.key", deploymentsDir + "/" + tn.instance("neighbour") + "/app", "/etc/shadow",
		"/proc/1/environ", deploySecretsDir + "/orama-deploy-" + tn.instance("neighbour") + ".env"} {
		if a := probe(t, c, "/read", url.Values{"p": {p}}); a.OK {
			t.Errorf("the app read %s", p)
		}
	}
	if a := probe(t, c, "/list", url.Values{"p": {deploymentsDir}}); a.OK && strings.Contains(a.Detail, "neighbour") {
		t.Errorf("the app lists other deployments: %s", a.Detail)
	}
	for _, p := range []string{"app.probe", deploymentsDir + "/" + tn.instance("sandbox") + "/x", "/opt/orama/x", "/etc/x"} {
		if a := probe(t, c, "/write", url.Values{"p": {p}}); a.OK {
			t.Errorf("the app wrote %s", p)
		}
	}
}

// checkNetwork: only PORT may be bound (SocketBindDeny=any plus the
// per-instance allow), and no node's WireGuard address answers. The controls:
// binding PORT reaches the kernel's address check (it is in use by the app
// itself, so "address already in use", not a policy refusal), every other
// bind is refused by the policy ("operation not permitted"), and the app
// dials its own gateway, the address it is given to reach.
func (tn *tenant) checkNetwork(t testing.TB, c *gw.Client) {
	t.Helper()
	port, err := strconv.Atoi(probe(t, c, "/getenv", url.Values{"k": {"PORT"}}).Detail)
	if err != nil {
		t.Fatalf("PORT is not a number: %v", err)
	}
	if a := probe(t, c, "/bind", url.Values{"addr": {":" + strconv.Itoa(port)}}); a.OK || !strings.Contains(a.Detail, bindInUse) {
		t.Fatalf("binding the app's own PORT %d answered ok=%v %q, want %q: the bind probe does not reach the kernel", port, a.OK, a.Detail, bindInUse)
	}
	for _, addr := range []string{":" + strconv.Itoa(port+1), "127.0.0.1:10104", ":0", "0.0.0.0:8080"} {
		if a := probe(t, c, "/bind", url.Values{"addr": {addr}}); a.OK || !strings.Contains(a.Detail, bindDenied) {
			t.Errorf("binding %s answered ok=%v %q, want a policy refusal %q (its PORT is %d)", addr, a.OK, a.Detail, bindDenied, port)
		}
	}
	gateway := gatewayAddr(t, probe(t, c, "/getenv", url.Values{"k": {"ORAMA_GATEWAY_URL"}}).Detail)
	if a := probe(t, c, "/dial", url.Values{"addr": {gateway}}); !a.OK {
		t.Fatalf("the app cannot dial its own gateway %s, so a refused dial proves nothing: %s", gateway, a.Detail)
	}
	for _, node := range tn.f.State.Nodes {
		for _, p := range []string{"10104", "10100", "22"} {
			addr := net.JoinHostPort(node.WGIP, p)
			if a := probe(t, c, "/dial", url.Values{"addr": {addr}}); a.OK {
				t.Errorf("the app reached %s on the WireGuard network", addr)
			}
		}
	}
}

// gatewayAddr is host:port of the gateway URL the app is given.
func gatewayAddr(t testing.TB, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		t.Fatalf("ORAMA_GATEWAY_URL %q is not a URL: %v", raw, err)
	}
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}

// TestDeploySandbox_unitConfinement: host-side, the unit runs with a dynamic
// user, a strict read-only system, private /tmp, no new privileges, the
// recorded limits, its bind allow-list, and a 0600 env file
// (docs/SECURITY.md "Tenant deployments").
func TestDeploySandbox_unitConfinement(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "unit"), "confined")
	serving(t, tn.app(u), "/health", "")
	port := probe(t, tn.app(u), "/getenv", url.Values{"k": {"PORT"}}).Detail
	unit := "orama-deploy-go@" + tn.instance("confined") + ".service"
	nodes := unitNodes(t, tn.f, unit)
	if len(nodes) == 0 {
		t.Fatal("no node runs the app")
	}
	for _, node := range nodes {
		props := tn.f.MustExec(t, node, "systemctl show "+unit+" -p DynamicUser,ProtectSystem,PrivateTmp,NoNewPrivileges,MemoryMax,TasksMax,SocketBindAllow,SocketBindDeny,UMask").Stdout
		for _, want := range []string{"DynamicUser=yes", "ProtectSystem=strict", "PrivateTmp=yes", "NoNewPrivileges=yes", "UMask=0077", "SocketBindAllow=tcp:" + port} {
			if !strings.Contains(props, want) {
				t.Errorf("%s: %s lacks %s:\n%s", node.Name, unit, want, props)
			}
		}
		for _, unlimited := range []string{"MemoryMax=infinity", "TasksMax=infinity"} {
			if strings.Contains(props, unlimited) {
				t.Errorf("%s: %s runs with %s", node.Name, unit, unlimited)
			}
		}
	}
	if r := tn.app(u).MustSend(t, gw.Req{Path: "/health"}); r.Status != http.StatusOK {
		t.Fatalf("the app stopped serving during the inspection: %d", r.Status)
	}
}

// TestDeployIdentity_workloadTokenRenews: the app is handed a token file and
// renews it at its gateway's /v1/auth/renew with the token it holds; the
// route refuses anything that is not a workload token (docs/DEPLOYMENT_GUIDE.md
// "Your app's own credential"; docs/API_SURFACE.md "/v1/auth/renew").
func TestDeployIdentity_workloadTokenRenews(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "id"), "identity")
	serving(t, tn.app(u), "/health", "")
	if a := probe(t, tn.app(u), "/renew", nil); !a.OK {
		t.Fatalf("the app could not renew its workload token: %s", a.Detail)
	}
	for who, bearer := range map[string]string{"an admin session": tn.admin.Bearer, "garbage": "a.b.c", "nothing": ""} {
		r := tn.n.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/auth/renew", Bearer: bearer})
		if r.Status == http.StatusOK {
			t.Errorf("/v1/auth/renew renewed %s", who)
		}
	}
}
