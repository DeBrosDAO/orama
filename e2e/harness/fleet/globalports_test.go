package fleet

import (
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const colocatedUFW = `Status: active

     To                         Action      From
     --                         ------      ----
22/tcp                     ALLOW IN    Anywhere                   # orama
Anywhere                   ALLOW FWD   198.18.0.2 on ogl-host     # orama-global
198.18.0.2 31000/tcp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31000/udp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31010/tcp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31010/udp       ALLOW FWD   Anywhere                   # orama-global
198.18.0.2 31013/tcp       ALLOW FWD   Anywhere                   # orama-global
`

func TestGlobalPublicPorts_colocatedForwardRules(t *testing.T) {
	got := GlobalPublicPorts(colocatedUFW)
	for _, want := range []string{"tcp/31000", "udp/31000", "tcp/31010", "udp/31010", "tcp/31013"} {
		if !got[want] {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	if len(got) != 5 {
		t.Errorf("got %v, want exactly the five global ports", got)
	}
}

// The forwarded ports are served inside the orama-global namespace; a process
// on the host listening on one must still fail the listener audit.
func TestGlobalHostPorts_forwardRulesExcuseNoHostListener(t *testing.T) {
	if got := GlobalHostPorts(colocatedUFW); len(got) != 0 {
		t.Errorf("a forward rule excused a host listener: %v", got)
	}
	got := GlobalHostPorts(colocatedUFW + "31000/tcp                   ALLOW IN    Anywhere                   # orama-global\n")
	if len(got) != 1 || !got["tcp/31000"] {
		t.Errorf("a direct allow is a host port, got %v", got)
	}
}

// The set is built from core's constants, so a port moved in core moves here;
// this pins the shape: chain P2P and Kubo swarm on both protocols, the rest TCP.
func TestGlobalPublic_followsCoreConstants(t *testing.T) {
	want := map[string]bool{
		"tcp/" + itoa(constants.ChainP2PPort): true, "udp/" + itoa(constants.ChainP2PPort): true,
		"tcp/" + itoa(constants.GlobalIPFSSwarmPort): true, "udp/" + itoa(constants.GlobalIPFSSwarmPort): true,
		"tcp/" + itoa(constants.GlobalProviderPort): true,
		"tcp/" + itoa(constants.GlobalTorORPort):    true, "tcp/" + itoa(constants.GlobalTorDirPort): true,
	}
	if len(globalPublic) != len(want) {
		t.Fatalf("globalPublic = %v, want %v", globalPublic, want)
	}
	for k := range want {
		if !globalPublic[k] {
			t.Errorf("%s missing from globalPublic %v", k, globalPublic)
		}
	}
}

func TestGlobalPublicPorts_globalOnlyDirectRules(t *testing.T) {
	got := GlobalPublicPorts("31000/tcp                   ALLOW IN    Anywhere                   # orama-global\n")
	if len(got) != 1 || !got["tcp/31000"] {
		t.Errorf("got %v, want tcp/31000 only", got)
	}
}

func TestGlobalPublicPorts_clusterNodeHasNone(t *testing.T) {
	if got := GlobalPublicPorts("22/tcp ALLOW IN Anywhere # orama\n80/tcp ALLOW IN Anywhere # orama\n"); len(got) != 0 {
		t.Errorf("a cluster node has no global ports, got %v", got)
	}
	if got := GlobalPublicPorts(""); len(got) != 0 {
		t.Errorf("empty status has none, got %v", got)
	}
}

func TestGlobalPublicPorts_neverExcusesWhatIsNotGlobalOrNotTagged(t *testing.T) {
	status := "31000/tcp ALLOW IN Anywhere\n" + // untagged
		"31050/tcp ALLOW IN Anywhere # orama-global\n" + // not a published global port
		"31000/tcp DENY IN Anywhere # orama-global\n" + // not an allow
		"10200/tcp ALLOW IN Anywhere # orama-global\n" // outside the block
	if got := GlobalPublicPorts(status); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
