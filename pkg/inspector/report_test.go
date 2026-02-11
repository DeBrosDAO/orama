package inspector

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrintTable_EmptyResults(t *testing.T) {
	r := &Results{}
	var buf bytes.Buffer
	PrintTable(r, &buf)
	if !strings.Contains(buf.String(), "No checks executed") {
		t.Errorf("expected 'No checks executed', got %q", buf.String())
	}
}

func TestPrintTable_SortsFailuresFirst(t *testing.T) {
	r := &Results{
		Duration: time.Second,
		Checks: []CheckResult{
			{ID: "a", Name: "Pass check", Subsystem: "test", Status: StatusPass, Severity: Low},
			{ID: "b", Name: "Fail check", Subsystem: "test", Status: StatusFail, Severity: Critical},
			{ID: "c", Name: "Warn check", Subsystem: "test", Status: StatusWarn, Severity: High},
		},
	}
	var buf bytes.Buffer
	PrintTable(r, &buf)
	output := buf.String()

	// FAIL should appear before WARN, which should appear before OK
	failIdx := strings.Index(output, "FAIL")
	warnIdx := strings.Index(output, "WARN")
	okIdx := strings.Index(output, "OK")

	if failIdx < 0 || warnIdx < 0 || okIdx < 0 {
		t.Fatalf("expected FAIL, WARN, and OK in output:\n%s", output)
	}
	if failIdx > warnIdx {
		t.Errorf("FAIL (pos %d) should appear before WARN (pos %d)", failIdx, warnIdx)
	}
	if warnIdx > okIdx {
		t.Errorf("WARN (pos %d) should appear before OK (pos %d)", warnIdx, okIdx)
	}
}

func TestPrintTable_IncludesNode(t *testing.T) {
	r := &Results{
		Duration: time.Second,
		Checks: []CheckResult{
			{ID: "a", Name: "Check A", Subsystem: "test", Status: StatusPass, Node: "ubuntu@1.2.3.4"},
		},
	}
	var buf bytes.Buffer
	PrintTable(r, &buf)
	if !strings.Contains(buf.String(), "ubuntu@1.2.3.4") {
		t.Error("expected node name in table output")
	}
}

func TestPrintTable_IncludesSummary(t *testing.T) {
	r := &Results{
		Duration: 2 * time.Second,
		Checks: []CheckResult{
			{ID: "a", Subsystem: "test", Status: StatusPass},
			{ID: "b", Subsystem: "test", Status: StatusFail},
		},
	}
	var buf bytes.Buffer
	PrintTable(r, &buf)
	output := buf.String()
	if !strings.Contains(output, "1 passed") {
		t.Error("summary should mention passed count")
	}
	if !strings.Contains(output, "1 failed") {
		t.Error("summary should mention failed count")
	}
}

func TestPrintJSON_ValidJSON(t *testing.T) {
	r := &Results{
		Duration: time.Second,
		Checks: []CheckResult{
			{ID: "a", Name: "A", Subsystem: "test", Status: StatusPass, Severity: Low, Message: "ok"},
			{ID: "b", Name: "B", Subsystem: "test", Status: StatusFail, Severity: High, Message: "bad"},
		},
	}
	var buf bytes.Buffer
	PrintJSON(r, &buf)

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	summary, ok := parsed["summary"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'summary' object in JSON")
	}
	if v := summary["passed"]; v != float64(1) {
		t.Errorf("summary.passed = %v, want 1", v)
	}
	if v := summary["failed"]; v != float64(1) {
		t.Errorf("summary.failed = %v, want 1", v)
	}
	if v := summary["total"]; v != float64(2) {
		t.Errorf("summary.total = %v, want 2", v)
	}

	checks, ok := parsed["checks"].([]interface{})
	if !ok {
		t.Fatal("missing 'checks' array in JSON")
	}
	if len(checks) != 2 {
		t.Errorf("want 2 checks, got %d", len(checks))
	}
}

func TestSummaryLine(t *testing.T) {
	r := &Results{
		Checks: []CheckResult{
			{Status: StatusPass},
			{Status: StatusPass},
			{Status: StatusFail},
			{Status: StatusWarn},
		},
	}
	got := SummaryLine(r)
	want := "2 passed, 1 failed, 1 warnings, 0 skipped"
	if got != want {
		t.Errorf("SummaryLine = %q, want %q", got, want)
	}
}
