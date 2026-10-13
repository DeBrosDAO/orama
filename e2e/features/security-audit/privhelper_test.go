//go:build e2e_fleet

package securityaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// rawHelperScript speaks the helper's protocol on its socket directly (one
// JSON request, one JSON response per connection; core/pkg/privhelper
// protocol.go), bypassing the client's own validation, for each argv in the
// JSON list argv[1]; it prints the responses as a JSON list.
const rawHelperScript = `import json,socket,sys
out=[]
for argv in json.loads(sys.argv[1]):
    s=socket.socket(socket.AF_UNIX);s.settimeout(30);s.connect("` + edge.PrivhelperSock + `")
    s.sendall(json.dumps({"argv":argv}).encode()+b"\n");buf=b""
    while True:
        c=s.recv(65536)
        if not c: break
        buf+=c
    out.append(json.loads(buf.decode()));s.close()
print(json.dumps(out))
`

type helperResponse struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// refusedArgv is what the helper's server must refuse whoever asks, root
// included: tools, units, ports, properties and env files outside its
// allow-list (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md "Root actions from unprivileged services").
var refusedArgv = [][]string{
	{}, {"bash", "-c", "id"}, {"systemctl"}, {"systemctl", "start", "ssh.service"},
	{"systemctl", "restart", "orama-node.service"}, {"systemctl", "start", "orama-namespace-gateway@../../x.service"},
	{"systemctl", "daemon-reload", "--now"}, {"systemctl", "set-property", "orama-node.service", "MemoryMax=1G"},
	{"systemctl", "set-property", "orama-deploy-node@e2e.service", "User=root"},
	{"ufw", "allow", "10100/tcp"}, {"ufw", "disable"}, {"ufw", "allow", "40000:49000/udp"},
	{"unitenv", "set", "index", "tor"}, {"unitenv", "set", "index", "ntfy"}, {"unitenv", "set", "index", "wireguard"},
	{"unitenv", "set", "../etc", "gateway"},
	{"deploy", "bind-port", "e2e-x", "node", "10104"}, {"deploy", "bind-port", "e2e-x", "node", "20000"},
	{"deploy", "bind-port", "e2e-x", "python", "12000"}, {"deploy", "set-env", "../../etc/x"},
	{"wireguard", "add-peer", "not-a-key", "1.2.3.4:51820", "10.0.0.9/32"}, {"wireguard", "rm", "-rf"},
	{"node-report", "extra"}, {"gateway-key", "get"},
}

// TestPrivhelper_serverRefusesOutsideTheAllowList: sent straight to the
// socket as root — the most privileged caller, which the unit check admits —
// every request outside the allow-list is refused (126, "refused") before a
// process starts, and the refusal is logged with the caller.
func TestPrivhelper_serverRefusesOutsideTheAllowList(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	since := time.Now().Add(-time.Second)
	raw, err := json.Marshal(refusedArgv)
	if err != nil {
		t.Fatal(err)
	}
	out := f.MustExec(t, n, "python3 -c "+fleet.ShellQuote(rawHelperScript)+" "+fleet.ShellQuote(string(raw)))
	var resps []helperResponse
	if err := json.Unmarshal([]byte(out.Stdout), &resps); err != nil || len(resps) != len(refusedArgv) {
		t.Fatalf("helper answers %q: %v", out.Stdout, err)
	}
	for i, r := range resps {
		if r.ExitCode != edge.ExitRefused || !strings.Contains(r.Output, "refused") {
			t.Errorf("%q: exit %d %q, want a refusal (%d)", refusedArgv[i], r.ExitCode, r.Output, edge.ExitRefused)
		}
	}
	if j := f.MustExec(t, n, "journalctl --no-pager -q -u 'orama-privhelper@*' --since @"+fmt.Sprint(since.Unix())).Stdout; !strings.Contains(j, "refused") {
		t.Errorf("%s: no refusal in the helper's journal", n.Name)
	}
}

// TestPrivhelper_socketAdmitsOnlyRootAndOrama: the socket is root:orama
// 0660, so another account cannot even connect, and the orama account from
// outside the two admitted units is refused by the unit check
// (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md: "authorised by the systemd unit the caller runs in"): an
// SSH session runs in no system.slice service, so the helper cannot name a
// unit for it at all (privhelper.UnitFromCgroup).
func TestPrivhelper_socketAdmitsOnlyRootAndOrama(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		infra.RequireStat(t, f, n, edge.PrivhelperSock, "root", "orama", "660")
		as := func(user, group string) fleet.Output {
			return f.Exec(t, n, fmt.Sprintf("setpriv --reuid=%s --regid=%s --init-groups %s call systemctl daemon-reload",
				user, group, edge.PrivhelperBin))
		}
		if o := as("nobody", "nogroup"); o.Exit != edge.ExitRefused || !strings.Contains(o.Stderr, "cannot reach") {
			t.Errorf("%s: nobody: exit %d %q, want a refused connection", n.Name, o.Exit, o.Stderr)
		}
		if o := as("orama", "orama"); o.Exit != edge.ExitRefused || !strings.Contains(o.Stdout+o.Stderr, "cannot identify the caller") {
			t.Errorf("%s: orama from a session: exit %d %q%q, want the refusal of a caller that runs in no system unit", n.Name, o.Exit, o.Stdout, o.Stderr)
		}
	}
}

// inUnitCgroup runs cmd as orama from inside unit's cgroup, so the helper
// reads that unit from /proc/<pid>/cgroup, as it would for the unit's own
// process. The shell leaves the cgroup when it exits.
func inUnitCgroup(t *testing.T, f *fleet.Fleet, n fleet.Node, unit, cmd string) fleet.Output {
	t.Helper()
	// The unit sits in its template's slice (system-orama\x2dnamespace\x2dgateway.slice),
	// so its directory is whatever systemd reports as its ControlGroup.
	cg := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p ControlGroup --value "+fleet.ShellQuote(unit)).Stdout)
	if !strings.HasPrefix(cg, "/system.slice/") {
		t.Fatalf("%s: %s has ControlGroup %q, want one under /system.slice", n.Name, unit, cg)
	}
	procs := "/sys/fs/cgroup" + cg + "/cgroup.procs"
	script := "echo $$ > " + fleet.ShellQuote(procs) + " && exec setpriv --reuid=orama --regid=orama --init-groups " + cmd
	return f.Exec(t, n, "sh -c "+fleet.ShellQuote(script))
}

// TestPrivhelper_tenantGatewayRefusedIndexGatewayNarrowed: a process in a
// tenant's gateway unit is refused everything, even a daemon-reload; the
// cluster gateway may reload but may not rewrite the mesh (persist-peers is
// orama-node's) (docs/whitepaper/technical-reference/vol1/29-build-signing-and-release.md "And by which process asks").
func TestPrivhelper_tenantGatewayRefusedIndexGatewayNarrowed(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := tenancy.Members(t, f, n.Name)[0]
	call := edge.PrivhelperBin + " call "
	tenant := inUnitCgroup(t, f, node, tenancy.UnitGateway(n.Name), call+"systemctl daemon-reload")
	if tenant.Exit != edge.ExitRefused || !strings.Contains(tenant.Stdout+tenant.Stderr, "may not use the privileged helper") {
		t.Errorf("from %s: exit %d %q %q, want refused", tenancy.UnitGateway(n.Name), tenant.Exit, tenant.Stdout, tenant.Stderr)
	}
	mesh := inUnitCgroup(t, f, node, edge.IndexGatewayUnit, call+"wireguard persist-peers </dev/null")
	if mesh.Exit != edge.ExitRefused || !strings.Contains(mesh.Stdout+mesh.Stderr, "orama-node") {
		t.Errorf("persist-peers from the cluster gateway: exit %d %q %q, want refused as orama-node's", mesh.Exit, mesh.Stdout, mesh.Stderr)
	}
	if ok := inUnitCgroup(t, f, node, edge.IndexGatewayUnit, call+"systemctl daemon-reload"); ok.Exit != 0 {
		t.Errorf("daemon-reload from the cluster gateway: exit %d %q %q, want allowed", ok.Exit, ok.Stdout, ok.Stderr)
	}
}

// TestPrivhelper_symlinkedDeployDirRefused: a deployment directory that is a
// symlink is refused before systemd would bind it (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md "The bind
// source is checked by root before the unit starts").
func TestPrivhelper_symlinkedDeployDirRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	instance, real := edge.RandomLabel(t, "e2e-sym-"), edge.RandomLabel(t, "e2e-real-")
	dir := "/opt/orama/.orama/data/deployments/"
	link, target := dir+instance, dir+real
	t.Cleanup(func() {
		edge.RunInCleanup(t, f, n, "rm -f "+fleet.ShellQuote(link)+" && rmdir "+fleet.ShellQuote(target)+" && ! test -e "+fleet.ShellQuote(link)+" && ! test -e "+fleet.ShellQuote(target))
	})
	// The target is a real directory owned by the orama user: the one thing
	// the check accepts. A symlink to /etc would also be refused for being
	// root's, whether or not the check looks for symlinks; a symlink to a
	// directory that passes by itself is refused for being a symlink only.
	f.MustExec(t, n, "install -d -o orama -g orama -m 0755 "+fleet.ShellQuote(target))
	if ok := f.Exec(t, n, edge.PrivhelperBin+" verify-deploy-dir "+real); ok.Exit != 0 {
		t.Fatalf("verify-deploy-dir refused a real directory owned by orama (exit %d): %s%s", ok.Exit, ok.Stdout, f.Redact(ok.Stderr))
	}
	f.MustExec(t, n, "ln -s "+fleet.ShellQuote(target)+" "+fleet.ShellQuote(link))
	o := f.Exec(t, n, edge.PrivhelperBin+" verify-deploy-dir "+instance)
	if o.Exit == 0 {
		t.Fatalf("verify-deploy-dir accepted a symlink to a directory that passes by itself: %s", o.Stdout)
	}
}
