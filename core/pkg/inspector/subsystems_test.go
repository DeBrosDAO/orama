package inspector

import (
	"strings"
	"testing"
)

func TestValidateSubsystems(t *testing.T) {
	SubsystemCheckers["test_sub_a"] = func(*ClusterData) []CheckResult { return nil }
	SubsystemCheckers["wireguard"] = func(*ClusterData) []CheckResult { return nil }
	defer delete(SubsystemCheckers, "test_sub_a")
	defer delete(SubsystemCheckers, "wireguard")

	for _, ok := range [][]string{nil, {}, {"all"}, {"test_sub_a"}, {"wg", "test_sub_a"}, {"wireguard"}} {
		if err := ValidateSubsystems(ok); err != nil {
			t.Errorf("ValidateSubsystems(%v) = %v, want nil", ok, err)
		}
	}
	for _, bad := range [][]string{{"e2e-nope"}, {"test_sub_a", "e2e-nope"}, {""}, {"test_sub_a", ""}} {
		err := ValidateSubsystems(bad)
		if err == nil {
			t.Errorf("ValidateSubsystems(%v) = nil, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "test_sub_a") {
			t.Errorf("the refusal for %v does not list the valid subsystems: %v", bad, err)
		}
	}
}
