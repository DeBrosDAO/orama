//go:build e2e_fleet

package install

import (
	"reflect"
	"testing"
)

const statusDuringTeardown = `SERVICE                                      STATE     WHAT IT IS
orama-namespace-caddy@index                  active    caddy for namespace "index"
orama-namespace-gateway@e2e-623dfe74-3jyeg7  inactive  gateway for namespace "e2e-623dfe74-3jyeg7"
orama-namespace-gateway@index                active    gateway for namespace "index"
orama-namespace-ipfs-gc@index.timer          active    ipfs-gc timer for namespace "index"
orama-node                                   active    the supervisor: it runs the index stack and the gateway

4 of 5 running
`

// A tenant namespace torn down by another package while status runs is not
// the install failing (stagenet 2026-10-04).
func TestInstallUnitsDown_aNamespaceBeingTornDownIsNotTheInstall(t *testing.T) {
	down, listed := installUnitsDown(statusDuringTeardown)
	if !listed || len(down) != 0 {
		t.Fatalf("down %v listed %v, want nothing down and orama-node listed", down, listed)
	}
}

func TestInstallUnitsDown_aStoppedIndexUnitIsDown(t *testing.T) {
	out := "orama-namespace-olric@index  inactive  olric for namespace \"index\"\norama-node  active  the supervisor\n"
	down, _ := installUnitsDown(out)
	if want := []string{"orama-namespace-olric@index"}; !reflect.DeepEqual(down, want) {
		t.Fatalf("down %v, want %v", down, want)
	}
}

func TestInstallUnitsDown_noServicesListsNothing(t *testing.T) {
	if _, listed := installUnitsDown("No Orama services are installed on this machine.\n"); listed {
		t.Fatal("orama-node counted as listed in an empty status")
	}
}
