package development

import (
	"testing"
)

func TestPortChecker(t *testing.T) {
	checker := NewPortChecker()

	if checker == nil {
		t.Fatal("NewPortChecker returned nil")
	}

	// Verify all required ports are defined
	if len(checker.ports) == 0 {
		t.Fatal("No ports defined in checker")
	}

	// Check that required port counts match expectations
	// 5 nodes × 9 ports per node + 4 shared ports = 49
	expectedPortCount := 49 // Based on RequiredPorts
	if len(checker.ports) != expectedPortCount {
		t.Errorf("Expected %d ports, got %d", expectedPortCount, len(checker.ports))
	}
}

func TestPortMap(t *testing.T) {
	portMap := PortMap()

	if len(portMap) == 0 {
		t.Fatal("PortMap returned empty map")
	}

	// Check for key ports
	expectedPorts := []int{4001, 5001, 7001, 6001, 3320, 9050, 9094}
	for _, port := range expectedPorts {
		if _, exists := portMap[port]; !exists {
			t.Errorf("Expected port %d not found in PortMap", port)
		}
	}

	// Verify descriptions exist
	for port, desc := range portMap {
		if desc == "" {
			t.Errorf("Port %d has empty description", port)
		}
	}
}

func TestDependencyChecker(t *testing.T) {
	checker := NewDependencyChecker()

	if checker == nil {
		t.Fatal("NewDependencyChecker returned nil")
	}

	// Verify required dependencies are defined
	if len(checker.dependencies) == 0 {
		t.Fatal("No dependencies defined in checker")
	}

	// Expected minimum dependencies
	expectedDeps := []string{"ipfs", "rqlited", "olric-server", "npm"}
	for _, expected := range expectedDeps {
		found := false
		for _, dep := range checker.dependencies {
			if dep.Command == expected {
				found = true
				if dep.InstallHint == "" {
					t.Errorf("Dependency %s has no install hint", expected)
				}
				break
			}
		}
		if !found {
			t.Errorf("Expected dependency %s not found", expected)
		}
	}
}

func TestIsPortAvailable(t *testing.T) {
	// Test with a very high port that should be available
	highPort := 65432
	if !isPortAvailable(highPort) {
		t.Logf("Port %d may be in use (this is non-fatal for testing)", highPort)
	}

	// Port 0 should not be available (reserved)
	if isPortAvailable(0) {
		t.Error("Port 0 should not be available")
	}
}
