package inspector

import (
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	r := &Results{
		Checks: []CheckResult{
			{ID: "a", Status: StatusPass},
			{ID: "b", Status: StatusPass},
			{ID: "c", Status: StatusFail},
			{ID: "d", Status: StatusWarn},
			{ID: "e", Status: StatusSkip},
			{ID: "f", Status: StatusPass},
		},
	}
	passed, failed, warned, skipped := r.Summary()
	if passed != 3 {
		t.Errorf("passed: want 3, got %d", passed)
	}
	if failed != 1 {
		t.Errorf("failed: want 1, got %d", failed)
	}
	if warned != 1 {
		t.Errorf("warned: want 1, got %d", warned)
	}
	if skipped != 1 {
		t.Errorf("skipped: want 1, got %d", skipped)
	}
}

func TestFailures(t *testing.T) {
	r := &Results{
		Checks: []CheckResult{
			{ID: "a", Status: StatusPass},
			{ID: "b", Status: StatusFail},
			{ID: "c", Status: StatusWarn},
			{ID: "d", Status: StatusFail},
		},
	}
	failures := r.Failures()
	if len(failures) != 2 {
		t.Fatalf("want 2 failures, got %d", len(failures))
	}
	for _, f := range failures {
		if f.Status != StatusFail {
			t.Errorf("expected StatusFail, got %s for check %s", f.Status, f.ID)
		}
	}
}

func TestFailuresAndWarnings(t *testing.T) {
	r := &Results{
		Checks: []CheckResult{
			{ID: "a", Status: StatusPass},
			{ID: "b", Status: StatusFail},
			{ID: "c", Status: StatusWarn},
			{ID: "d", Status: StatusSkip},
		},
	}
	fw := r.FailuresAndWarnings()
	if len(fw) != 2 {
		t.Fatalf("want 2 failures+warnings, got %d", len(fw))
	}
}

func TestPass(t *testing.T) {
	c := Pass("test.id", "Test Name", "sub", "node1", "msg", Critical)
	if c.Status != StatusPass {
		t.Errorf("want pass, got %s", c.Status)
	}
	if c.Severity != Critical {
		t.Errorf("want Critical, got %s", c.Severity)
	}
	if c.Node != "node1" {
		t.Errorf("want node1, got %s", c.Node)
	}
}

func TestFail(t *testing.T) {
	c := Fail("test.id", "Test Name", "sub", "", "msg", High)
	if c.Status != StatusFail {
		t.Errorf("want fail, got %s", c.Status)
	}
	if c.Node != "" {
		t.Errorf("want empty node, got %q", c.Node)
	}
}

func TestWarn(t *testing.T) {
	c := Warn("test.id", "Test Name", "sub", "n", "msg", Medium)
	if c.Status != StatusWarn {
		t.Errorf("want warn, got %s", c.Status)
	}
}

func TestSkip(t *testing.T) {
	c := Skip("test.id", "Test Name", "sub", "n", "msg", Low)
	if c.Status != StatusSkip {
		t.Errorf("want skip, got %s", c.Status)
	}
}

func TestSeverityString(t *testing.T) {
	tests := []struct {
		sev  Severity
		want string
	}{
		{Low, "LOW"},
		{Medium, "MEDIUM"},
		{High, "HIGH"},
		{Critical, "CRITICAL"},
		{Severity(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.sev.String(); got != tt.want {
				t.Errorf("Severity(%d).String() = %q, want %q", tt.sev, got, tt.want)
			}
		})
	}
}

func TestRunChecks_EmptyData(t *testing.T) {
	data := &ClusterData{
		Nodes:    map[string]*NodeData{},
		Duration: time.Second,
	}
	results := RunChecks(data, nil)
	if results == nil {
		t.Fatal("RunChecks returned nil")
	}
	// Should not panic and should return a valid Results
}

func TestRunChecks_FilterBySubsystem(t *testing.T) {
	// Register a test checker
	called := map[string]bool{}
	SubsystemCheckers["test_sub_a"] = func(data *ClusterData) []CheckResult {
		called["a"] = true
		return []CheckResult{Pass("a.1", "A1", "test_sub_a", "", "ok", Low)}
	}
	SubsystemCheckers["test_sub_b"] = func(data *ClusterData) []CheckResult {
		called["b"] = true
		return []CheckResult{Pass("b.1", "B1", "test_sub_b", "", "ok", Low)}
	}
	defer delete(SubsystemCheckers, "test_sub_a")
	defer delete(SubsystemCheckers, "test_sub_b")

	data := &ClusterData{Nodes: map[string]*NodeData{}}

	// Filter to only "test_sub_a"
	results := RunChecks(data, []string{"test_sub_a"})
	if !called["a"] {
		t.Error("test_sub_a checker was not called")
	}
	if called["b"] {
		t.Error("test_sub_b checker should not have been called")
	}

	found := false
	for _, c := range results.Checks {
		if c.ID == "a.1" {
			found = true
		}
		if c.Subsystem == "test_sub_b" {
			t.Error("should not have checks from test_sub_b")
		}
	}
	if !found {
		t.Error("expected check a.1 in results")
	}
}

func TestRunChecks_AliasWG(t *testing.T) {
	called := false
	SubsystemCheckers["wireguard"] = func(data *ClusterData) []CheckResult {
		called = true
		return nil
	}
	defer delete(SubsystemCheckers, "wireguard")

	data := &ClusterData{Nodes: map[string]*NodeData{}}
	RunChecks(data, []string{"wg"})
	if !called {
		t.Error("wireguard checker not called via 'wg' alias")
	}
}
