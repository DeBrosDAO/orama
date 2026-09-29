//go:build e2e_fleet

package edge

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// nodeDNSScript sends one recursion-desired query from the node itself (no
// dig on the nodes: core/systemd/orama-namespace-caddy@.service) and prints
// "<rcode> <answer count>". argv: server, name, qtype number.
const nodeDNSScript = `import os,socket,struct,sys
srv,name,qt=sys.argv[1],sys.argv[2],int(sys.argv[3])
q=os.urandom(2)+b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00"
q+=b"".join(bytes([len(p)])+p.encode() for p in name.strip(".").split("."))+b"\x00"+struct.pack(">HH",qt,1)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(10);s.sendto(q,(srv,53))
r=s.recv(65535);print(r[3]&15,struct.unpack(">H",r[6:8])[0])
`

// NodeAnswer is a reply seen from a node shell.
type NodeAnswer struct {
	RCode   int
	Answers int
}

// QueryFromNode asks server:53 for name/qtype from n itself, so the source
// address is the node's (loopback or its WireGuard address).
func QueryFromNode(t testing.TB, f *fleet.Fleet, n fleet.Node, server, name string, qtype uint16) NodeAnswer {
	t.Helper()
	cmd := fmt.Sprintf("python3 -c %s %s %s %d", fleet.ShellQuote(nodeDNSScript), fleet.ShellQuote(server), fleet.ShellQuote(name), qtype)
	out := f.Exec(t, n, cmd)
	fields := strings.Fields(out.Stdout)
	if out.Exit != 0 || len(fields) != 2 {
		t.Fatalf("%s: DNS query to %s for %s failed (exit %d): %s %s", n.Name, server, name, out.Exit, out.Stdout, out.Stderr)
	}
	rc, err1 := strconv.Atoi(fields[0])
	an, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("%s: unreadable DNS reply summary %q", n.Name, out.Stdout)
	}
	return NodeAnswer{RCode: rc, Answers: an}
}
