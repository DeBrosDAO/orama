//go:build e2e_fleet

package chainnodenames

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// zoneLabel is the dedicated sub-zone below the cluster's base domain the names are served in.
	zoneLabel = "nodes"
	// syncBudget is how long a claim or a release may take to reach the nameserver: one sync
	// interval (nodenames.SyncInterval, 60 s) plus a pass and the plugin's 30 s cache.
	syncBudget = 3 * time.Minute
	pollEvery  = 5 * time.Second
)

// aQueryScript sends one query for name/A to server:53 from the node and prints the rcode, then
// every A address in the answer, one per line. argv: server, name.
const aQueryScript = `import os,socket,struct,sys
srv,name=sys.argv[1],sys.argv[2]
q=os.urandom(2)+b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00"
q+=b"".join(bytes([len(p)])+p.encode() for p in name.strip(".").split("."))+b"\x00"+struct.pack(">HH",1,1)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(10);s.sendto(q,(srv,53))
r=s.recv(65535);print(r[3]&15)
an=struct.unpack(">H",r[6:8])[0]
def skip(i):
    while r[i]:
        i+=2 if r[i]&0xc0==0xc0 else 1+r[i]
        if r[i-2]&0xc0==0xc0: return i
    return i+1
i=skip(12)+4
for _ in range(an):
    i=skip(i) if r[i]&0xc0!=0xc0 else i+2
    t,_,_,l=struct.unpack(">HHIH",r[i:i+10]);i+=10
    if t==1: print(".".join(str(b) for b in r[i:i+4]))
    i+=l
`

// answersFor is the A addresses server answers for fqdn, asked from n itself. A query that cannot
// be made or answered is an error for the poll to retry, not a failed test: the nameserver may be
// restarting.
func answersFor(f *fleet.Fleet, t *testing.T, n fleet.Node, server, fqdn string) ([]string, error) {
	t.Helper()
	out := f.Exec(t, n, fmt.Sprintf("python3 -c %s %s %s", fleet.ShellQuote(aQueryScript), fleet.ShellQuote(server), fleet.ShellQuote(fqdn)))
	lines := strings.Fields(out.Stdout)
	if out.Exit != 0 || len(lines) == 0 {
		return nil, fmt.Errorf("%s: DNS query to %s for %s failed (exit %d): %s %s", n.Name, server, fqdn, out.Exit, out.Stdout, f.Redact(out.Stderr))
	}
	return lines[1:], nil
}

// waitForAnswer polls until the answer for fqdn contains want (or, when present is false, no
// longer does), and fails after syncBudget.
func waitForAnswer(t *testing.T, f *fleet.Fleet, n fleet.Node, server, fqdn, want string, present bool) {
	t.Helper()
	state := map[bool]string{true: "present", false: "gone"}[present]
	eventually.Require(t, pollEvery, syncBudget, fmt.Sprintf("%s answers %s for %s (%s)", server, want, fqdn, state), func() (bool, error) {
		got, err := answersFor(f, t, n, server, fqdn)
		if err != nil {
			return false, err
		}
		has := slices.Contains(got, want)
		if has == present {
			return true, nil
		}
		return false, fmt.Errorf("%s answers %v for %s", server, got, fqdn)
	})
}

// publicIP is a literal public address no node of the run holds: x/nodes refuses one that a live
// node does, and the sync refuses a private one.
func publicIP() string {
	return fmt.Sprintf("104.%d.%d.%d", 1+rand.IntN(250), 1+rand.IntN(250), 1+rand.IntN(250))
}

// TestNodeNames_theZoneServesAClaimAndDropsARelease: with dns.node_names_zone set on a node of the
// cluster, a node name claimed on the chain appears as an A record of <name>.<zone> on that node's
// nameserver within a sync interval, and goes again within one when the name is released. The
// zone is a sub-zone below the base domain (the config check refuses the base domain itself), the
// test restores node.yaml and restarts the node when it ends, and it runs alone because it
// restarts a node of the cluster.
func TestNodeNames_theZoneServesAClaimAndDropsARelease(t *testing.T) {
	f := harness.Fleet(t)
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the test writes node.yaml and restarts a node of the run's own fleet; it does not touch stagenet")
	}
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)

	baseDomain := f.State.BaseDomain
	if baseDomain == "" {
		t.Fatal("the run has no base domain, so its cluster answers no zone")
	}
	zone := zoneLabel + "." + baseDomain
	setZone(t, f, n, zone)

	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(5))
	c.EnsureOperator(t, op)
	ip := publicIP()
	id := c.RegisterTestNodeAt(t, op, []string{chain.RoleStorage}, []string{"https://" + ip + ":31013"}, "ipfs")
	name := uniqueName(t)
	fqdn := name + "." + zone

	chain.RequireOK(t, "claim a node name", c.Submit(t, op, chain.TxOptions{}, claimMsg(op.Address, id, name)))
	waitForAnswer(t, f, n, n.PublicIP, fqdn, ip, true)

	chain.RequireOK(t, "release the node name", c.Submit(t, op, chain.TxOptions{}, releaseMsg(op.Address, id)))
	waitForAnswer(t, f, n, n.PublicIP, fqdn, ip, false)
	c.RequireInvariants(t, "a node name claimed, served and released")
}

// setZone writes dns.node_names_zone into n's node.yaml, restarts the node with the orama CLI, and
// restores the file and restarts again when the test ends.
func setZone(t *testing.T, f *fleet.Fleet, n fleet.Node, zone string) {
	t.Helper()
	backup := infra.NodeConfigPath + ".e2e-names"
	cmds := []string{
		fmt.Sprintf("cp -p %s %s", infra.NodeConfigPath, backup),
		fmt.Sprintf("printf '\\ndns:\\n  node_names_zone: \"%s\"\\n' >> %s", zone, infra.NodeConfigPath),
	}
	for _, cmd := range cmds {
		if out := f.Exec(t, n, "sudo sh -c "+fleet.ShellQuote(cmd)); out.Exit != 0 {
			t.Fatalf("%s: %q failed: %s", n.Name, cmd, f.Redact(out.Stdout+out.Stderr))
		}
	}
	t.Cleanup(func() {
		restore := fmt.Sprintf("mv -f %s %s", backup, infra.NodeConfigPath)
		if out := f.Exec(t, n, "sudo sh -c "+fleet.ShellQuote(restore)); out.Exit != 0 {
			t.Errorf("%s: could not restore node.yaml: %s", n.Name, f.Redact(out.Stdout+out.Stderr))
			return
		}
		restart(t, f, n)
	})
	restart(t, f, n)
}

func restart(t *testing.T, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	out := infra.OnNode(t, f, n, "node", "restart")
	if out.Exit != infra.ExitOK {
		t.Fatalf("orama node restart on %s: exit %d\n%s", n.Name, out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" after orama node restart")
}
