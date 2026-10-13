package deployments

import "testing"

func TestNodeOverlayIP_prefersWireGuard(t *testing.T) {
	if got := nodeOverlayIP("10.0.0.1", "37.59.116.212"); got != "10.0.0.1" {
		t.Fatalf("overlay = %s, want the WireGuard address", got)
	}
}

func TestNodeOverlayIP_fallsBackWhenInternalIsUnset(t *testing.T) {
	if got := nodeOverlayIP("", "10.0.0.5"); got != "10.0.0.5" {
		t.Fatalf("overlay = %s, want the recorded address", got)
	}
	if got := nodeOverlayIP("  ", "10.0.0.5"); got != "10.0.0.5" {
		t.Fatalf("blank internal = %s, want the recorded address", got)
	}
}
