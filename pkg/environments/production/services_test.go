package production

import (
	"strings"
	"testing"
)

// TestGenerateRQLiteService verifies RQLite service generation with advertise IP and join address
func TestGenerateRQLiteService(t *testing.T) {
	tests := []struct {
		name              string
		nodeType          string
		joinAddr          string
		advertiseIP       string
		expectJoinInUnit  bool
		expectAdvertiseIP string
	}{
		{
			name:              "bootstrap with localhost advertise",
			nodeType:          "bootstrap",
			joinAddr:          "",
			advertiseIP:       "",
			expectJoinInUnit:  false,
			expectAdvertiseIP: "127.0.0.1",
		},
		{
			name:              "bootstrap with public IP advertise",
			nodeType:          "bootstrap",
			joinAddr:          "",
			advertiseIP:       "10.0.0.1",
			expectJoinInUnit:  false,
			expectAdvertiseIP: "10.0.0.1",
		},
		{
			name:              "node joining cluster",
			nodeType:          "node",
			joinAddr:          "10.0.0.1:7001",
			advertiseIP:       "10.0.0.2",
			expectJoinInUnit:  true,
			expectAdvertiseIP: "10.0.0.2",
		},
		{
			name:              "node with localhost (should still include join)",
			nodeType:          "node",
			joinAddr:          "localhost:7001",
			advertiseIP:       "127.0.0.1",
			expectJoinInUnit:  true,
			expectAdvertiseIP: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ssg := &SystemdServiceGenerator{
				debrosHome: "/home/debros",
				debrosDir:  "/home/debros/.debros",
			}

			unit := ssg.GenerateRQLiteService(tt.nodeType, 5001, 7001, tt.joinAddr, tt.advertiseIP)

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
		debrosHome: "/home/debros",
		debrosDir:  "/home/debros/.debros",
	}

	unit := ssg.GenerateRQLiteService("node", 5001, 7001, "10.0.0.1:7001", "10.0.0.2")

	// Verify essential flags are present
	if !strings.Contains(unit, "-http-addr 0.0.0.0:5001") {
		t.Error("missing -http-addr 0.0.0.0:5001")
	}
	if !strings.Contains(unit, "-http-adv-addr 10.0.0.2:5001") {
		t.Error("missing -http-adv-addr 10.0.0.2:5001")
	}
	if !strings.Contains(unit, "-raft-addr 0.0.0.0:7001") {
		t.Error("missing -raft-addr 0.0.0.0:7001")
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
