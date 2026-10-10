package lifecycle

import (
	"slices"
	"testing"
)

// The GC timer fell due in the middle of orama node stop, started its oneshot,
// and the stop of ipfs@index killed it before it could handle SIGTERM: the
// timers are stopped before any service.
func TestNamespaceStopOrder_timersBeforeServices(t *testing.T) {
	listing := "orama-namespace-caddy@index.service loaded active running Orama Namespace Caddy (index)\n" +
		"orama-namespace-ipfs-cluster@index.service loaded active running Orama Namespace IPFS Cluster (index)\n" +
		"orama-namespace-ipfs@index.service loaded active running Orama Namespace IPFS (index)\n" +
		"orama-namespace-ipfs-gc@index.timer loaded active waiting Schedule periodic IPFS repo garbage collection (index)\n"
	want := []string{
		"orama-namespace-ipfs-gc@index.timer",
		"orama-namespace-caddy@index.service",
		"orama-namespace-ipfs-cluster@index.service",
		"orama-namespace-ipfs@index.service",
	}
	if got := namespaceStopOrder(listing); !slices.Equal(got, want) {
		t.Errorf("namespaceStopOrder = %v, want %v", got, want)
	}
}

func TestNamespaceStopOrder_emptyAndForeignLines(t *testing.T) {
	if got := namespaceStopOrder(""); len(got) != 0 {
		t.Errorf("an empty listing gave %v", got)
	}
	listing := "\n   \nsshd.service loaded active running OpenSSH\n0 loaded units listed.\n"
	if got := namespaceStopOrder(listing); len(got) != 0 {
		t.Errorf("a listing without namespace units gave %v", got)
	}
}

// A failed unit is listed too (--plain drops the bullet that hid it), and a
// listing of only timers, or only services, keeps every unit.
func TestNamespaceStopOrder_failedUnitsAndOneKindOnly(t *testing.T) {
	timers := "orama-namespace-ipfs-gc@a.timer loaded active waiting x\norama-namespace-ipfs-gc@b.timer loaded active waiting x\n"
	if got := namespaceStopOrder(timers); len(got) != 2 {
		t.Errorf("timers only: %v", got)
	}
	failed := "orama-namespace-ipfs-gc@index.service loaded failed failed x\n"
	if got := namespaceStopOrder(failed); !slices.Equal(got, []string{"orama-namespace-ipfs-gc@index.service"}) {
		t.Errorf("a failed unit was dropped: %v", got)
	}
}
