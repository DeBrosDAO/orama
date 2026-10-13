package main

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

func TestExecute_journalReadsOnlyTheNamedUnit(t *testing.T) {
	orig := runTool
	defer func() { runTool = orig }()
	var gotTool string
	var gotArgs []string
	runTool = func(tool string, args []string) privhelper.Response {
		gotTool, gotArgs = tool, args
		return privhelper.Response{Output: "line\n"}
	}
	inv, err := privhelper.Validate([]string{"journal", "orama-deploy-go@acme-web.service", "20"})
	if err != nil {
		t.Fatal(err)
	}
	resp := execute(inv, nil)
	want := []string{"-u", "orama-deploy-go@acme-web.service", "-n", "20", "--no-pager", "-q"}
	if gotTool != "journalctl" || !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("ran %s %q, want journalctl %q", gotTool, gotArgs, want)
	}
	if resp.ExitCode != 0 || resp.Output != "line\n" {
		t.Errorf("response %+v", resp)
	}
}

func TestToolPaths_journalctlIsFixed(t *testing.T) {
	if len(toolPaths["journalctl"]) == 0 {
		t.Fatal("journalctl has no fixed path; the helper would report it not found")
	}
}

func TestTailBuffer_keepsNewestLinesWithoutANoticeInTheText(t *testing.T) {
	b := &tailBuffer{limit: 20}
	for _, l := range []string{"old-1\n", "old-2\n", "new-3\n", "new-4\n"} {
		b.Write([]byte(l))
	}
	got := b.String()
	if !b.truncated {
		t.Fatal("not marked truncated")
	}
	if got != "new-3\nnew-4\n" && got != "old-2\nnew-3\nnew-4\n" {
		t.Errorf("kept %q, want the newest whole lines", got)
	}
	if strings.Contains(got, "truncated") || !strings.HasSuffix(got, "new-4\n") {
		t.Errorf("tail %q lost the newest line or carries a notice", got)
	}
}

func TestTailBuffer_underLimitIsUntouched(t *testing.T) {
	b := &tailBuffer{limit: 100}
	b.Write([]byte("a\nb\n"))
	if b.truncated || b.String() != "a\nb\n" {
		t.Errorf("got %q truncated=%v", b.String(), b.truncated)
	}
}

func TestRunJournalctl_tailStderrSeparate(t *testing.T) {
	script := "i=1; while [ $i -le 5000 ]; do echo line-$i-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; i=$((i+1)); done; echo warning >&2"
	resp := runJournalctl(exec.Command("/bin/sh", "-c", script))
	if resp.ExitCode != 0 || !resp.Truncated {
		t.Fatalf("exit=%d truncated=%v, want 0/true", resp.ExitCode, resp.Truncated)
	}
	if !strings.HasSuffix(resp.Output, "line-5000-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n") {
		t.Error("the newest line was dropped")
	}
	if strings.Contains(resp.Output, "warning") || strings.Contains(resp.Output, "truncated") || strings.HasPrefix(resp.Output, "line-1-") {
		t.Error("stderr or a notice is inside the log, or the oldest lines were kept")
	}
	if len(resp.Output) > maxToolOutput {
		t.Errorf("output %d bytes over the cap", len(resp.Output))
	}
}

func TestRunJournalctl_failureReportsStderr(t *testing.T) {
	resp := runJournalctl(exec.Command("/bin/sh", "-c", "echo out; echo boom >&2; exit 3"))
	if resp.ExitCode != 3 || resp.Output != "boom\n" {
		t.Errorf("got %+v", resp)
	}
}
