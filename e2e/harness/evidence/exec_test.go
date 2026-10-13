package evidence

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

func TestRunRecorded_recordsRedactedOutputAndExit(t *testing.T) {
	dir := t.TempDir()
	rec, err := New(dir, "feat", secrets.NewRedactor("s3cret-token-value"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `echo out s3cret-token-value; echo err >&2; exit 3`)
	cmd.Dir = dir
	res, err := RunRecorded(t, rec, "vitest", cmd)
	if err != nil || res.Exit != 3 || !strings.Contains(res.Stdout, "out") || res.Stderr != "err\n" {
		t.Fatalf("res %+v err %v", res, err)
	}
	recs, err := Load(dir)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records %+v err %v", recs, err)
	}
	r := recs[0]
	if r.Kind != KindExec || r.Status != 3 || r.Test != t.Name() || !strings.HasPrefix(r.Summary, "vitest: sh -c") ||
		strings.Contains(r.Output, "s3cret-token-value") || !strings.Contains(r.Output, "[stderr]\nerr") || r.Input != "dir: "+dir {
		t.Fatalf("record %+v", r)
	}
}

func TestRunRecorded_notRunnableAndPresetOutput(t *testing.T) {
	dir := t.TempDir()
	rec, err := New(dir, "feat", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RunRecorded(t, rec, "missing", exec.Command("/nonexistent/binary"))
	if err == nil || res.Exit != -1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if recs, _ := Load(dir); len(recs) != 1 || recs[0].Error == "" {
		t.Fatalf("the failure was not recorded: %+v", recs)
	}
	cmd := exec.Command("true")
	cmd.Stdout = &strings.Builder{}
	if _, err := RunRecorded(t, rec, "preset", cmd); err == nil {
		t.Fatal("a command with its own stdout was accepted")
	}
	if _, err := RunRecorded(t, nil, "no recorder", exec.Command("true")); err != nil {
		t.Fatalf("nil recorder: %v", err)
	}
}
