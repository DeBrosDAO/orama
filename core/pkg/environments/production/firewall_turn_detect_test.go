package production

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHostRunsTURN is the bugboard #846 guard: Phase 6b must detect a TURN node
// even when the TURN systemd unit is STOPPED (the upgrade stops it before the
// firewall reset). Detection keys on the persisted turn.env file, which
// survives a stop + `ufw --force reset`, so it never produces the false
// negative that would close the relay on the upgrade path.
func TestHostRunsTURN(t *testing.T) {
	tmp := t.TempDir()
	ps := &ProductionSetup{oramaDir: tmp}

	// No namespaces provisioned → not a TURN node.
	if ps.hostRunsTURN() {
		t.Fatal("expected hostRunsTURN=false when no turn.env exists")
	}

	// A gateway-only namespace (no turn.env) → still not a TURN node.
	gwOnly := filepath.Join(tmp, "data", "namespaces", "gw-only")
	if err := os.MkdirAll(gwOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gwOnly, "gateway.env"), []byte("X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ps.hostRunsTURN() {
		t.Fatal("expected hostRunsTURN=false for a gateway-only namespace")
	}

	// A namespace with a provisioned (but possibly stopped) TURN → TURN node.
	turnNS := filepath.Join(tmp, "data", "namespaces", "anchat-test")
	if err := os.MkdirAll(turnNS, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(turnNS, "turn.env"), []byte("TURN_CONFIG=/x.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ps.hostRunsTURN() {
		t.Fatal("expected hostRunsTURN=true when a namespace turn.env exists (even if the unit is stopped)")
	}
}
