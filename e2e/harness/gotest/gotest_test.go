package gotest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

const stream = `{"Action":"start","Package":"e2e/features/smoke"}
{"Action":"run","Package":"e2e/features/smoke","Test":"TestHealth_ok"}
{"Action":"output","Package":"e2e/features/smoke","Test":"TestHealth_ok","Output":"=== RUN   TestHealth_ok\n"}
{"Action":"pass","Package":"e2e/features/smoke","Test":"TestHealth_ok","Elapsed":0.5}
{"Action":"run","Package":"e2e/features/smoke","Test":"TestStatus_shape"}
{"Action":"output","Package":"e2e/features/smoke","Test":"TestStatus_shape","Output":"    status_test.go:12: want 200, got 503\n"}
{"Action":"fail","Package":"e2e/features/smoke","Test":"TestStatus_shape","Elapsed":1.25}
{"Action":"run","Package":"e2e/features/smoke","Test":"TestTLS_chain"}
{"Action":"skip","Package":"e2e/features/smoke","Test":"TestTLS_chain","Elapsed":0}
{"Action":"run","Package":"e2e/features/smoke","Test":"TestHang"}
{"Action":"output","Package":"e2e/features/smoke","Output":"panic: test timed out after 1m0s\n"}
{"Action":"fail","Package":"e2e/features/smoke","Elapsed":60}
`

func TestParse_results(t *testing.T) {
	res, err := Parse(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range res {
		got[r.Test] = r.Action
	}
	want := map[string]string{"": ActionFail, "TestHealth_ok": ActionPass, "TestStatus_shape": ActionFail, "TestTLS_chain": ActionSkip, "TestHang": ActionFail}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: got %q want %q", k, got[k], v)
		}
	}
	if len(res) != len(want) {
		t.Errorf("got %d rows: %+v", len(res), res)
	}
	if res[0].Test != "" || res[1].Test != "TestHang" {
		t.Errorf("not sorted: %+v", res)
	}
}

func TestFailed_testsFirstPackageOnlyWhenNoTestFailed(t *testing.T) {
	res, _ := Parse(strings.NewReader(stream))
	failed := Failed(res)
	if len(failed) != 2 || failed[0].Test != "TestHang" || failed[1].Test != "TestStatus_shape" {
		t.Fatalf("failed %+v", failed)
	}
	build := `{"ImportPath":"e2e/features/broken","Action":"build-output","Output":"x.go:1: syntax error\n"}
{"Action":"fail","Package":"e2e/features/broken","Elapsed":0}
`
	res, _ = Parse(strings.NewReader(build))
	failed = Failed(res)
	if len(failed) != 1 || failed[0].Test != "" || !strings.Contains(failed[0].Output, "syntax error") {
		t.Fatalf("build failure: %+v", failed)
	}
}

func TestParse_strayLinesAndEmpty(t *testing.T) {
	res, err := Parse(strings.NewReader("go: cannot find module\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Action != ActionFail || !strings.Contains(res[0].Output, "cannot find module") {
		t.Fatalf("stray output: %+v", res)
	}
	res, err = Parse(strings.NewReader(""))
	if err != nil || len(res) != 0 {
		t.Fatalf("empty: %+v %v", res, err)
	}
}

func TestAppendBounded_caps(t *testing.T) {
	s := appendBounded("", strings.Repeat("x", MaxOutputBytes+10))
	if !strings.HasSuffix(s, "[truncated]") || len(s) > MaxOutputBytes+20 {
		t.Fatalf("len %d", len(s))
	}
	if appendBounded(s, "more") != s {
		t.Fatal("appended past the cap")
	}
}

func TestRedactFile_eventsAndStrayLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stage-01-x.json")
	in := `{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:9: got {\"access_token\":\"tok-12345678\"} and minted-99999999\n"}` + "\n" +
		`{"Action":"fail","Package":"p","Test":"TestA","Elapsed":1}` + "\n" +
		"stray line with minted-99999999\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	red := secrets.NewRedactor("minted-99999999")
	if err := RedactFile(path, red.Redact); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "tok-12345678") || strings.Contains(string(raw), "minted-99999999") {
		t.Fatalf("secret survived:\n%s", raw)
	}
	res, err := Parse(strings.NewReader(string(raw)))
	if err != nil || len(res) != 2 || res[1].Action != ActionFail || !strings.Contains(res[1].Output, "a_test.go:9") {
		t.Fatalf("results %+v err %v", res, err)
	}
	if err := RedactFile(filepath.Join(t.TempDir(), "none.json"), red.Redact); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}
