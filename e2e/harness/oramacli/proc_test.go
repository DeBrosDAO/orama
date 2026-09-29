package oramacli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

func writeBin(t *testing.T, r *Runner, script string) {
	t.Helper()
	if err := os.WriteFile(r.Bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStart_streamsLinesThenWaits(t *testing.T) {
	r, evDir := newRunner(t)
	writeBin(t, r, "echo one\nprintf 'two\\r\\nthree'\necho err >&2\nexit 2\n")
	p, err := r.Start(context.Background(), "stream", "--token", "tok-secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for line := range p.StdoutLines() {
		got = append(got, line)
	}
	if strings.Join(got, "|") != "one|two|three" {
		t.Fatalf("lines %q", got)
	}
	res, err := p.Wait()
	if err != nil || res.Exit != 2 || res.Stderr != "err\n" || !strings.Contains(res.Stdout, "three") {
		t.Fatalf("res %+v err %v", res, err)
	}
	if again, err2 := p.Wait(); err2 != nil || again.Exit != res.Exit {
		t.Fatalf("second Wait %+v %v", again, err2)
	}
	recs, err := evidence.Load(evDir)
	if err != nil || len(recs) != 1 || recs[0].Status != 2 || strings.Contains(recs[0].Summary, "tok-secret-1234") {
		t.Fatalf("evidence %+v err %v", recs, err)
	}
}

func TestStart_unreadOutputNeverBlocksTheCLI(t *testing.T) {
	r, _ := newRunner(t)
	writeBin(t, r, "i=0\nwhile [ $i -lt 20000 ]; do echo line-$i; i=$((i+1)); done\n")
	p, err := r.Start(context.Background(), "flood")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Wait()
	if err != nil || res.Exit != 0 || !strings.Contains(res.Stdout, "line-19999") {
		t.Fatalf("exit %d err %v", res.Exit, err)
	}
	for range p.StdoutLines() {
	}
}

func TestProcKill_endsALongRunningCommand(t *testing.T) {
	r, _ := newRunner(t)
	writeBin(t, r, "echo ready\nexec tail -f /dev/null\n")
	p, err := r.Start(context.Background(), "wait")
	if err != nil {
		t.Fatal(err)
	}
	if line := <-p.StdoutLines(); line != "ready" {
		t.Fatalf("first line %q", line)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	res, err := p.Wait()
	if err != nil || res.Exit == 0 {
		t.Fatalf("killed process: res %+v err %v", res, err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("killing an exited process: %v", err)
	}
}

func TestStart_contextEndsProcess(t *testing.T) {
	r, _ := newRunner(t)
	writeBin(t, r, "exec tail -f /dev/null\n")
	ctx, cancel := context.WithCancel(context.Background())
	p, err := r.Start(ctx, "wait")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if res, err := p.Wait(); err != nil || res.Exit == 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestStart_checkRefusalsAndStartFailure(t *testing.T) {
	r, evDir := newRunner(t)
	r.AgentSock = ""
	if _, err := r.Start(context.Background(), "version"); err == nil {
		t.Fatal("empty agent socket accepted")
	}
	r, evDir = newRunner(t)
	if err := os.Chmod(r.Bin, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(context.Background(), "version"); err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("err %v", err)
	}
	if recs, err := evidence.Load(evDir); err != nil || len(recs) != 1 || recs[0].Status != -1 {
		t.Fatalf("start failure not recorded: %+v %v", recs, err)
	}
}

func TestSplitLines_forms(t *testing.T) {
	lines, rest := splitLines("a\r\nb\nc")
	if strings.Join(lines, "|") != "a|b" || rest != "c" {
		t.Fatalf("%q %q", lines, rest)
	}
	if lines, rest := splitLines(""); len(lines) != 0 || rest != "" {
		t.Fatalf("%q %q", lines, rest)
	}
}
