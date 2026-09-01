package production

import (
	"strings"
	"testing"
)

// TestGenerateRQLiteService verifies RQLite service generation with advertise IP and join address
func TestGenerateRQLiteService(t *testing.T) {
	tests := []struct {
		name              string
		joinAddr          string
		advertiseIP       string
		expectJoinInUnit  bool
		expectAdvertiseIP string
	}{
		{
			name:              "first node with localhost advertise",
			joinAddr:          "",
			advertiseIP:       "",
			expectJoinInUnit:  false,
			expectAdvertiseIP: "127.0.0.1",
		},
		{
			name:              "first node with public IP advertise",
			joinAddr:          "",
			advertiseIP:       "10.0.0.1",
			expectJoinInUnit:  false,
			expectAdvertiseIP: "10.0.0.1",
		},
		{
			name:              "node joining cluster",
			joinAddr:          "10.0.0.1:7001",
			advertiseIP:       "10.0.0.2",
			expectJoinInUnit:  true,
			expectAdvertiseIP: "10.0.0.2",
		},
		{
			name:              "node with localhost (should still include join)",
			joinAddr:          "localhost:7001",
			advertiseIP:       "127.0.0.1",
			expectJoinInUnit:  true,
			expectAdvertiseIP: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ssg := &SystemdServiceGenerator{
				oramaHome: "/opt/orama",
				oramaDir:  "/opt/orama/.orama",
			}

			unit := ssg.GenerateRQLiteService("/usr/local/bin/rqlited", 5001, 7001, tt.joinAddr, tt.advertiseIP)

			// Check advertise IP is present
			expectedAdvertise := tt.expectAdvertiseIP + ":5001"
			if !strings.Contains(unit, expectedAdvertise) {
				t.Errorf("expected advertise address %q in unit, got:\n%s", expectedAdvertise, unit)
			}

			// Check raft advertise IP is present
			expectedRaftAdvertise := tt.expectAdvertiseIP + ":7001"
			if !strings.Contains(unit, expectedRaftAdvertise) {
				t.Errorf("expected raft advertise address %q in unit, got:\n%s", expectedRaftAdvertise, unit)
			}

			// Check join flag presence
			hasJoin := strings.Contains(unit, "-join")
			if hasJoin != tt.expectJoinInUnit {
				t.Errorf("expected join in unit: %v, hasJoin: %v\nUnit:\n%s", tt.expectJoinInUnit, hasJoin, unit)
			}

			if tt.expectJoinInUnit && tt.joinAddr != "" && !strings.Contains(unit, tt.joinAddr) {
				t.Errorf("expected join address %q in unit, not found", tt.joinAddr)
			}
		})
	}
}

// TestGenerateCaddyService_GatewayReadinessCheck verifies Caddy waits for gateway before starting
func TestGenerateCaddyService_GatewayReadinessCheck(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}

	unit := ssg.GenerateCaddyService()

	// Must have ExecStartPre that polls gateway health
	if !strings.Contains(unit, "ExecStartPre=") {
		t.Error("missing ExecStartPre directive for gateway readiness check")
	}
	if !strings.Contains(unit, "localhost:10104/health") {
		t.Error("ExecStartPre should poll localhost:10104/health")
	}

	// Must use Requires= (hard dependency), not Wants= (soft dependency)
	if !strings.Contains(unit, "Requires=orama-node.service") {
		t.Error("missing Requires=orama-node.service (hard dependency)")
	}
	if strings.Contains(unit, "Wants=orama-node.service") {
		t.Error("should use Requires= not Wants= for orama-node.service dependency")
	}

	// ExecStartPre must appear before ExecStart
	preIdx := strings.Index(unit, "ExecStartPre=")
	startIdx := strings.Index(unit, "ExecStart=/usr/bin/caddy")
	if preIdx < 0 || startIdx < 0 || preIdx >= startIdx {
		t.Error("ExecStartPre must appear before ExecStart")
	}
}

// TestGenerateRQLiteServiceArgs verifies the ExecStart command arguments
func TestGenerateRQLiteServiceArgs(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}

	unit := ssg.GenerateRQLiteService("/usr/local/bin/rqlited", 5001, 7001, "10.0.0.1:7001", "10.0.0.2")

	// Verify essential flags are present (localhost binding for security)
	if !strings.Contains(unit, "-http-addr 127.0.0.1:5001") {
		t.Error("missing -http-addr 127.0.0.1:5001")
	}
	if !strings.Contains(unit, "-http-adv-addr 10.0.0.2:5001") {
		t.Error("missing -http-adv-addr 10.0.0.2:5001")
	}
	if !strings.Contains(unit, "-raft-addr 127.0.0.1:7001") {
		t.Error("missing -raft-addr 127.0.0.1:7001")
	}
	if !strings.Contains(unit, "-raft-adv-addr 10.0.0.2:7001") {
		t.Error("missing -raft-adv-addr 10.0.0.2:7001")
	}
	if !strings.Contains(unit, "-join 10.0.0.1:7001") {
		t.Error("missing -join 10.0.0.1:7001")
	}
	if !strings.Contains(unit, "-join-attempts 30") {
		t.Error("missing -join-attempts 30")
	}
}

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

// TestGenerateIPFSGCService verifies the one-shot GC unit: it must run
// `ipfs repo gc`, be ordered after (and require) the IPFS daemon, point at the
// repo via IPFS_PATH, and — being timer-triggered — must NOT install itself.
func TestGenerateIPFSGCService(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}

	unit := ssg.GenerateIPFSGCService("/usr/local/bin/ipfs")

	for _, want := range []string{
		"Type=oneshot",
		"ExecStart=/usr/local/bin/ipfs repo gc",
		"After=orama-ipfs.service",
		"Requires=orama-ipfs.service",
		"Environment=IPFS_PATH=/opt/orama/.orama/data/ipfs/repo",
		"SyslogIdentifier=orama-ipfs-gc",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("GC service missing %q, got:\n%s", want, unit)
		}
	}

	// Must carry the unprivileged hardening block (same as the daemon) — a GC
	// run never needs root, so NoNewPrivileges must hold.
	for _, want := range []string{"User=orama", "NoNewPrivileges=yes", "ProtectSystem=strict"} {
		if !strings.Contains(unit, want) {
			t.Errorf("GC service missing hardening directive %q, got:\n%s", want, unit)
		}
	}

	// A one-shot triggered by a timer must not enable itself on boot.
	if strings.Contains(unit, "[Install]") {
		t.Errorf("GC one-shot service must have no [Install] section, got:\n%s", unit)
	}
	// The daemon, not this unit, should never carry --enable-gc; this unit is the
	// GC mechanism, so it must not accidentally launch a daemon.
	if strings.Contains(unit, "daemon") {
		t.Errorf("GC service must not start a daemon, got:\n%s", unit)
	}
}

// TestGenerateIPFSGCTimer verifies the timer wiring: it triggers the GC one-shot,
// staggers across nodes, and installs into timers.target.
func TestGenerateIPFSGCTimer(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/opt/orama",
		oramaDir:  "/opt/orama/.orama",
	}

	unit := ssg.GenerateIPFSGCTimer()

	for _, want := range []string{
		"[Timer]",
		"OnBootSec=" + ipfsGCOnBootSec,
		"OnUnitActiveSec=" + ipfsGCInterval,
		"RandomizedDelaySec=" + ipfsGCRandomizedDelaySec,
		"Unit=orama-ipfs-gc.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("GC timer missing %q, got:\n%s", want, unit)
		}
	}
}
