package systemd

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// allServiceTypes lists every ServiceType; a new one added without a place
// here will not be checked below, so keep them together.
var allServiceTypes = []ServiceType{
	ServiceTypeRQLite, ServiceTypeOlric, ServiceTypeGateway, ServiceTypeSFU,
	ServiceTypeTURN, ServiceTypePubsub, ServiceTypeWireGuard, ServiceTypeIPFS,
	ServiceTypeIPFSCluster, ServiceTypeIPFSGC, ServiceTypeVault, ServiceTypeCaddy,
	ServiceTypeNtfy, ServiceTypeTor, ServiceTypeSNIRouter, ServiceTypeCoreDNS,
}

// Every unit this package starts, stops or enables as the orama user goes
// through orama-privhelper. A service type or namespace name the helper
// refuses would fail at the moment the node needs it — a namespace that does
// not start — with a permission error far from its cause.
func TestPrivHelper_allowsEveryNamespaceUnitTheManagerDrives(t *testing.T) {
	m := &Manager{}
	for _, st := range allServiceTypes {
		for _, ns := range []string{"index", "nameserver", "anchat-v2", "a1"} {
			unit := m.serviceName(ns, st)
			for _, verb := range []string{"start", "stop", "restart", "enable", "disable"} {
				if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, verb, unit}); err != nil {
					t.Errorf("helper refuses systemctl %s %s: %v", verb, unit, err)
				}
			}
		}
	}
	for _, verb := range []string{"start", "stop", "restart", "enable"} {
		if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, verb, HostTURNServiceName}); err != nil {
			t.Errorf("helper refuses systemctl %s %s: %v", verb, HostTURNServiceName, err)
		}
	}
}

// The index migration stops and disables the pre-namespace units; the helper
// must allow exactly that and never let the orama user start one again.
func TestPrivHelper_retiresLeftoverUnitsButNeverStartsThem(t *testing.T) {
	leftovers := append(append([]string{}, LeftoverHostUnits...), LeftoverNameserverUnit)
	// wg-quick@wg0 is only ever disabled: stopping it would bounce wg0.
	if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "disable", LeftoverWireGuardUnit}); err != nil {
		t.Errorf("helper refuses disabling %s: %v", LeftoverWireGuardUnit, err)
	}
	for _, unit := range leftovers {
		for _, verb := range []string{"stop", "disable"} {
			if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, verb, unit}); err != nil {
				t.Errorf("helper refuses systemctl %s %s: %v", verb, unit, err)
			}
		}
		if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "start", unit}); err == nil {
			t.Errorf("helper must not let the orama user start leftover unit %s", unit)
		}
	}
}
