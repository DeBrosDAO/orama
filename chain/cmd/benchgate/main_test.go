package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBench writes a benchmark file carrying the stamp and returns its path.
func writeBench(t *testing.T, name string, stamp Stamp) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(stamp.Line()+"\nBenchmarkX-8 10 100 ns/op\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gate(t *testing.T, csv string, extra ...string) (code int, out, errOut string) {
	t.Helper()
	release, _ := NewStamp(stableBox, true, false)
	fresh, _ := NewStamp(stableBox, false, false)
	args := append([]string{"compare", "-base", writeBench(t, "base.txt", release), "-new", writeBench(t, "new.txt", fresh)}, extra...)
	var stdout, stderr bytes.Buffer
	code = run(args, strings.NewReader(csv), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRun_compareExitCodes(t *testing.T) {
	if code, out, _ := gate(t, ",a,,b,,,\n,sec/op,CI,sec/op,CI,vs base,P\nX-8,1e-6,1%,1.1e-6,1%,+10.00%,p=0.002 n=6\n"); code != exitOK || !strings.Contains(out, "PASS") {
		t.Errorf("within the threshold: code %d\n%s", code, out)
	}
	code, out, _ := gate(t, comparisonCSV)
	if code != exitFailed || !strings.Contains(out, "REGRESSION Fast-8") || !strings.Contains(out, "MISSING Gone-8") || !strings.Contains(out, "FAIL") {
		t.Errorf("regression and missing benchmark: code %d\n%s", code, out)
	}
	if code, _, errOut := gate(t, ""); code != exitUsage || !strings.Contains(errOut, "no sec/op") {
		t.Errorf("empty input: code %d %q", code, errOut)
	}
	if code, out, _ := gate(t, comparisonCSV, "-threshold", "100"); code != exitFailed || strings.Contains(out, "REGRESSION") {
		t.Errorf("a 100%% threshold must leave only the missing benchmark: code %d\n%s", code, out)
	}
}

func TestRun_compareRefusesAnUnstableBaselineUnlessOverridden(t *testing.T) {
	notRelease, _ := NewStamp(laptop, true, true)
	laptopRun, _ := NewStamp(laptop, false, false)
	base, fresh := writeBench(t, "base.txt", notRelease), writeBench(t, "new.txt", laptopRun)
	csv := ",a,,b,,,\n,sec/op,CI,sec/op,CI,vs base,P\nX-8,1e-6,1%,1e-6,1%,~,p=0.9 n=6\n"
	var stdout, stderr bytes.Buffer
	if code := run([]string{"compare", "-base", base, "-new", fresh}, strings.NewReader(csv), &stdout, &stderr); code != exitUsage {
		t.Errorf("not-release baseline: code %d, want %d (%s)", code, exitUsage, stderr.String())
	}
	if code := run([]string{"compare", "-base", base, "-new", fresh, "-allow-unstable"}, strings.NewReader(csv), &stdout, &stderr); code != exitOK {
		t.Errorf("override: code %d (%s)", code, stderr.String())
	}
}

func TestRun_usageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for name, args := range map[string][]string{
		"no command":         nil,
		"unknown command":    {"gate"},
		"compare no files":   {"compare"},
		"compare zero limit": {"compare", "-base", "a", "-new", "b", "-threshold", "0"},
		"missing file":       {"compare", "-base", "/nonexistent/a", "-new", "/nonexistent/b"},
	} {
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != exitUsage {
			t.Errorf("%s: code %d, want %d", name, code, exitUsage)
		}
	}
}

func TestRun_stampPrintsTheHostLine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"stamp"}, strings.NewReader(""), &stdout, &stderr); code != exitOK {
		t.Fatalf("code %d (%s)", code, stderr.String())
	}
	if line := stdout.String(); !strings.HasPrefix(line, stampPrefix) || !strings.Contains(line, "release=false") {
		t.Fatalf("stamp output %q", line)
	}
}
