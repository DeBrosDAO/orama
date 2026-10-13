package install

import (
	"strings"
	"testing"
)

func TestGenerateNodeService_supervisorOnly(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}
	unit := ssg.GenerateNodeService()
	for _, want := range []string{
		"After=network-online.target",
		"Wants=network-online.target",
		"ExecStart=/opt/orama/bin/orama-node",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("node unit missing %q, got:\n%s", want, unit)
		}
	}
	for _, not := range []string{
		"Requires=wg-quick@wg0",
		"After=orama-ipfs-cluster",
		"After=orama-olric",
	} {
		if strings.Contains(unit, not) {
			t.Errorf("node unit must not depend on leftover host unit %q, got:\n%s", not, unit)
		}
	}
}

// The node no longer exits because the cluster is unreachable, so a restart
// means a real crash and systemd must keep restarting rather than park the unit
// in failed state. StartLimitIntervalSec belongs in [Unit]: systemd moved it out
// of [Service] in v229 and warns about the old placement.
func TestGenerateNodeService_disablesTheStartLimitInTheUnitSection(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}
	unit := ssg.GenerateNodeService()

	if !strings.Contains(unit, "StartLimitIntervalSec=0") {
		t.Fatalf("node unit does not disable the start limit, got:\n%s", unit)
	}

	unitSection := unit[strings.Index(unit, "[Unit]"):strings.Index(unit, "\n[Service]\n")]
	if !strings.Contains(unitSection, "StartLimitIntervalSec=0") {
		t.Errorf("StartLimitIntervalSec must be in [Unit], not [Service], got:\n%s", unit)
	}
}

// Shutdown announces maintenance, waits up to bootShutdownGrace (10s) for the
// boot supervisor, then tears services down and hands raft leadership over. If
// TimeoutStopSec matched the grace, systemd would SIGKILL exactly as the
// leadership transfer began.
func TestGenerateNodeService_stopTimeoutLeavesRoomForLeadershipTransfer(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}
	unit := ssg.GenerateNodeService()

	if !strings.Contains(unit, "TimeoutStopSec=60") {
		t.Errorf("node unit needs a stop timeout well above the 10s boot-supervisor grace, got:\n%s", unit)
	}
}

// wg-quick runs wg0.conf's PostUp lines as root, so orama-node must not be able
// to write /etc/wireguard; peer persistence goes through the privileged helper.
func TestGenerateNodeService_cannotWriteWireGuardConfig(t *testing.T) {
	ssg := &SystemdServiceGenerator{oramaHome: "/opt/orama", oramaDir: "/opt/orama/.orama"}
	unit := ssg.GenerateNodeService()
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "ReadWritePaths=") && strings.Contains(line, "/etc/wireguard") {
			t.Fatalf("orama-node can write /etc/wireguard: %s", line)
		}
	}
	if !strings.Contains(unit, "ReadWritePaths=/opt/orama/.orama\n") {
		t.Errorf("orama-node lost write access to its own tree:\n%s", unit)
	}
}
