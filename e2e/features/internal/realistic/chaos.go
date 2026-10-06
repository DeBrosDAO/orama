//go:build e2e_fleet

package realistic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Units of the index plane edge does not name (core/pkg/namespace/index.go).
const (
	IndexOlricUnit = "orama-namespace-olric@index.service"
	IndexIPFSUnit  = "orama-namespace-ipfs@index.service"
	NodeUnit       = "orama-node.service"
)

// ServiceClass is one kind of daemon a node runs, and its unit.
type ServiceClass struct {
	Name string
	Unit string
	// NameserverOnly: the unit runs only on nameserver nodes.
	NameserverOnly bool
}

// ServiceClasses are the daemons a core node runs for the cluster itself:
// the index plane the IndexSupervisor keeps (core/pkg/namespace/index.go,
// index_host.go: every unit is <service>@index, CoreDNS @nameserver) and the
// node supervisor. SIGKILL of any one must be recovered from.
var ServiceClasses = []ServiceClass{
	{Name: "rqlite", Unit: edge.IndexRQLiteUnit},
	{Name: "olric", Unit: IndexOlricUnit},
	{Name: "gateway", Unit: edge.IndexGatewayUnit},
	{Name: "ipfs", Unit: IndexIPFSUnit},
	{Name: "coredns", Unit: edge.CoreDNSUnit, NameserverOnly: true},
	{Name: "caddy", Unit: edge.CaddyUnit},
	{Name: "orama-node", Unit: NodeUnit},
}

// ruleAbsent is `iptables -C`'s exit code when the rule does not exist.
const ruleAbsent = 1

// newTag is a random suffix that makes one call's firewall rules its own.
func newTag(t testing.TB) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// insertRule inserts spec with tool on n, tagged for this call, after
// registering the cleanup that deletes every copy and proves it is gone.
func insertRule(t testing.TB, f *fleet.Fleet, n fleet.Node, tool, spec string) {
	t.Helper()
	spec += " -m comment --comment " + fleet.ShellQuote("e2e-"+f.State.RunID+"-"+newTag(t))
	check := tool + " -C " + spec + " 2>/dev/null"
	remove := fmt.Sprintf("while %s; do %s -D %s || exit 1; done; %s; test $? -eq %d", check, tool, spec, check, ruleAbsent)
	t.Cleanup(func() { edge.RunInCleanup(t, f, n, remove) })
	f.MustExec(t, n, tool+" -I "+spec)
}

// DropFrom makes on drop every packet arriving from from's public and
// WireGuard addresses, while on's own packets to from still leave: half of
// a partition (on hears nothing from from; from still hears on). SSH from
// the runner is untouched.
func DropFrom(t testing.TB, f *fleet.Fleet, on, from fleet.Node) {
	t.Helper()
	for _, addr := range []string{from.PublicIP, from.WGIP} {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		tool := "ip6tables"
		if ip.To4() != nil {
			tool = "iptables"
		}
		insertRule(t, f, on, tool, "INPUT -s "+ip.String()+" -j DROP")
	}
}

// wgIface is the overlay every inter-node byte crosses.
const wgIface = "wg0"

// Degrade makes n's overlay lossy and slow: netem delay and loss on wg0
// when tc is installed, else a random iptables drop of the same share of
// inbound overlay packets. It returns which was used. The cleanup removes it
// and proves it is gone.
func Degrade(t testing.TB, f *fleet.Fleet, n fleet.Node, delayMS, lossPct int) string {
	t.Helper()
	if f.Exec(t, n, "command -v tc").Exit == 0 {
		t.Cleanup(func() {
			edge.RunInCleanup(t, f, n, "tc qdisc del dev "+wgIface+" root 2>/dev/null; ! tc qdisc show dev "+wgIface+" | grep -q netem")
		})
		f.MustExec(t, n, fmt.Sprintf("tc qdisc add dev %s root netem delay %dms loss %d%%", wgIface, delayMS, lossPct))
		return "tc netem"
	}
	insertRule(t, f, n, "iptables", fmt.Sprintf("INPUT -i %s -m statistic --mode random --probability %.2f -j DROP", wgIface, float64(lossPct)/100))
	return "iptables statistic drop"
}
