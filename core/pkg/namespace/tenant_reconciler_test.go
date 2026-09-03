package namespace

import (
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/olric"
)

func TestTenantReconcileCoordinator_isDeterministicAndSingular(t *testing.T) {
	// Cluster-wide writes need exactly one writer per sweep. Every node has to
	// compute the same answer from the same membership list, with no lock and
	// no leader lookup — concurrent writers are how these stores diverged.
	live := []string{"node-c", "node-a", "node-b"}

	got := tenantReconcileCoordinator(live)
	if got != "node-a" {
		t.Fatalf("got %q, want the lowest-sorted member", got)
	}

	// Order of the input must not change the answer.
	if other := tenantReconcileCoordinator([]string{"node-b", "node-c", "node-a"}); other != got {
		t.Fatalf("a different input order elected %q instead of %q", other, got)
	}

	if tenantReconcileCoordinator(nil) != "" {
		t.Fatal("an empty membership must elect nobody, not the zero value of a node id")
	}
}

func TestTenantReconcileCoordinator_doesNotMutateItsInput(t *testing.T) {
	live := []string{"node-c", "node-a"}
	tenantReconcileCoordinator(live)
	if !reflect.DeepEqual(live, []string{"node-c", "node-a"}) {
		t.Fatalf("the caller's membership slice was reordered: %v", live)
	}
}

func TestOlricConfigInSync_ignoresPeerOrder(t *testing.T) {
	// Peer order comes from a database query and means nothing to Olric.
	// Comparing slices directly would report drift on every sweep and restart
	// the cache in a loop.
	base := olric.InstanceConfig{
		BindAddr:       "10.0.0.2",
		HTTPPort:       10010,
		MemberlistPort: 10011,
		PeerAddresses:  []string{"10.0.0.3:10011", "10.0.0.4:10011"},
	}
	reordered := base
	reordered.PeerAddresses = []string{"10.0.0.4:10011", "10.0.0.3:10011"}

	if !olricConfigInSync(buildOlricConfig(base), buildOlricConfig(reordered)) {
		t.Fatal("a reordered peer list was reported as drift")
	}
}

func TestOlricConfigInSync_detectsRealDrift(t *testing.T) {
	base := olric.InstanceConfig{
		BindAddr:       "10.0.0.2",
		HTTPPort:       10010,
		MemberlistPort: 10011,
		PeerAddresses:  []string{"10.0.0.3:10011", "10.0.0.4:10011"},
	}

	tests := map[string]func(c *olric.InstanceConfig){
		// The case this exists for: a member was replaced and the survivors
		// still list the departed node.
		"a departed peer":  func(c *olric.InstanceConfig) { c.PeerAddresses = []string{"10.0.0.3:10011"} },
		"an added peer":    func(c *olric.InstanceConfig) { c.PeerAddresses = append(c.PeerAddresses, "10.0.0.5:10011") },
		"a changed peer":   func(c *olric.InstanceConfig) { c.PeerAddresses = []string{"10.0.0.3:10011", "10.0.0.9:10011"} },
		"a changed port":   func(c *olric.InstanceConfig) { c.HTTPPort = 10099 },
		"a changed bind":   func(c *olric.InstanceConfig) { c.BindAddr = "10.0.0.9" },
		"a changed mlport": func(c *olric.InstanceConfig) { c.MemberlistPort = 10099 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.PeerAddresses = append([]string(nil), base.PeerAddresses...)
			mutate(&changed)

			if olricConfigInSync(buildOlricConfig(base), buildOlricConfig(changed)) {
				t.Fatalf("%s was not detected as drift", name)
			}
		})
	}
}

func TestOlricConfigInSync_emptyPeerLists(t *testing.T) {
	single := olric.InstanceConfig{BindAddr: "10.0.0.2", HTTPPort: 10010, MemberlistPort: 10011}
	if !olricConfigInSync(buildOlricConfig(single), buildOlricConfig(single)) {
		t.Fatal("a single-node namespace was reported as drifted from itself")
	}

	withPeer := single
	withPeer.PeerAddresses = []string{"10.0.0.3:10011"}
	if olricConfigInSync(buildOlricConfig(single), buildOlricConfig(withPeer)) {
		t.Fatal("gaining a first peer was not detected as drift")
	}
}

func TestSameStringSet(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"identical", []string{"a", "b"}, []string{"a", "b"}, true},
		{"reordered", []string{"a", "b"}, []string{"b", "a"}, true},
		{"both empty", nil, nil, true},
		{"empty vs one", nil, []string{"a"}, false},
		{"one vs empty", []string{"a"}, nil, false},
		{"different member", []string{"a", "b"}, []string{"a", "c"}, false},
		{"extra member", []string{"a"}, []string{"a", "b"}, false},
		// A duplicate is not a set difference, but it IS a different list, and
		// treating them as equal would hide a config that lists a peer twice.
		{"duplicate", []string{"a", "a"}, []string{"a"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameStringSet(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameStringSet(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
