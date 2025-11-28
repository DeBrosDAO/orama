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
				oramaHome: "/home/debros",
				oramaDir:  "/home/debros/.orama",
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

// TestGenerateRQLiteServiceArgs verifies the ExecStart command arguments
func TestGenerateRQLiteServiceArgs(t *testing.T) {
	ssg := &SystemdServiceGenerator{
		oramaHome: "/home/debros",
		oramaDir:  "/home/debros/.orama",
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
