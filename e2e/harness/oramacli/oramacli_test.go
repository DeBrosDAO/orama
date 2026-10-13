package oramacli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// fakeOrama prints its environment and arguments, then exits with $FAKE_EXIT.
const fakeOrama = `#!/bin/sh
echo "HOME=$HOME"
echo "RW_AGENT_SOCK=$RW_AGENT_SOCK"
echo "ORAMA_E2E=$ORAMA_E2E"
echo "HCLOUD=$HCLOUD_TOKEN"
echo "XDG=$XDG_CONFIG_HOME"
echo "ARGS=$*"
echo "problem" >&2
exit ${FAKE_EXIT:-0}
`

func newRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "orama")
	if err := os.WriteFile(bin, []byte(fakeOrama), 0o755); err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(dir, "evidence")
	rec, err := evidence.New(evDir, "cli", nil)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return &Runner{
		Bin: bin, Home: home, AgentSock: filepath.Join(dir, "agent.sock"), Recorder: rec,
		RealHome: func() (string, error) { return "/nonexistent/real-home", nil },
	}, evDir
}

func TestRun_isolatesEnvironment(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "hcloud-secret-value-1")
	t.Setenv("XDG_CONFIG_HOME", "/real/config")
	r, _ := newRunner(t)
	res, err := r.Run(context.Background(), "version")
	if err != nil || res.Exit != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	for _, want := range []string{"HOME=" + r.Home, "RW_AGENT_SOCK=" + r.AgentSock, "ORAMA_E2E=1", "HCLOUD=\n", "XDG=\n", "ARGS=version"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, res.Stdout)
		}
	}
}

func TestRun_nonZeroExitIsNotAnError(t *testing.T) {
	r, evDir := newRunner(t)
	r.Env = []string{"FAKE_EXIT=4"}
	res, err := r.Run(context.Background(), "node", "invite", "--token", "invite-secret-xyz")
	if err != nil || res.Exit != 4 || !strings.Contains(res.Stderr, "problem") {
		t.Fatalf("res %+v err %v", res, err)
	}
	recs, err := evidence.Load(evDir)
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs %v err %v", recs, err)
	}
	if recs[0].Status != 4 || strings.Contains(recs[0].Summary, "invite-secret-xyz") || !strings.Contains(recs[0].Output, "[stderr]") {
		t.Fatalf("record %+v", recs[0])
	}
}

func TestCheck_refusals(t *testing.T) {
	cases := map[string]func(r *Runner){
		"empty sock":        func(r *Runner) { r.AgentSock = "" },
		"sock in real home": func(r *Runner) { r.AgentSock = "/nonexistent/real-home/.rootwallet/agent.sock" },
		"relative home":     func(r *Runner) { r.Home = "home" },
		"home is real home": func(r *Runner) { r.Home = "/nonexistent/real-home" },
		"missing binary":    func(r *Runner) { r.Bin = "/nonexistent/orama" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := newRunner(t)
			mutate(r)
			if _, err := r.Run(context.Background(), "version"); err == nil {
				t.Fatal("runner ran")
			}
		})
	}
}

func TestMustOK_andDecodeJSON(t *testing.T) {
	r, _ := newRunner(t)
	res := r.MustOK(t, "version")
	var v map[string]any
	if err := DecodeJSON(res, &v); err == nil {
		t.Fatal("non-JSON stdout decoded")
	}
	if err := DecodeJSON(Result{Stdout: `{"a":1}`}, &v); err != nil || v["a"] != float64(1) {
		t.Fatalf("v %v err %v", v, err)
	}
	if err := DecodeJSON(Result{Stdout: `{"a":1}{"b":2}`}, &v); err == nil {
		t.Fatal("two JSON values accepted")
	}
}

func TestRedactArgs_forms(t *testing.T) {
	got := strings.Join(RedactArgs([]string{"--token", "t1", "--api-key=k1", "--env", "e2e", "--password"}), " ")
	if got != "--token [REDACTED] --api-key=[REDACTED] --env e2e --password" {
		t.Fatalf("got %s", got)
	}
	if len(RedactArgs(nil)) != 0 {
		t.Fatal("nil args")
	}
}

func TestEnviron_allowlistOnly(t *testing.T) {
	r := &Runner{Home: "/tmp/h", AgentSock: "/tmp/a.sock", Env: []string{"EXTRA=1"}}
	host := map[string]string{
		"PATH": "/bin", "LANG": "C.UTF-8", "TZ": "UTC", "SSH_AUTH_SOCK": "/owner/agent",
		"AWS_SECRET_ACCESS_KEY": "aws-secret", "XDG_CONFIG_HOME": "/owner/.config", "HOME": "/owner",
		"RW_AGENT_SOCK": "/owner/.rootwallet/agent.sock", "HCLOUD_TOKEN": "h", "GOFLAGS": "-x",
	}
	env := strings.Join(r.environ(func(k string) (string, bool) { v, ok := host[k]; return v, ok }), "\n")
	for _, want := range []string{"PATH=/bin", "LANG=C.UTF-8", "TZ=UTC", "HOME=/tmp/h", "RW_AGENT_SOCK=/tmp/a.sock", "ORAMA_E2E=1", "EXTRA=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s:\n%s", want, env)
		}
	}
	for _, leak := range []string{"SSH_AUTH_SOCK", "aws-secret", "/owner", "HCLOUD_TOKEN", "GOFLAGS"} {
		if strings.Contains(env, leak) {
			t.Errorf("%s reached the CLI:\n%s", leak, env)
		}
	}
}

func TestRun_sshAgentNotInherited(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/owner/ssh-agent.sock")
	r, _ := newRunner(t)
	if err := os.WriteFile(r.Bin, []byte("#!/bin/sh\necho \"SSH=$SSH_AUTH_SOCK\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), "version")
	if err != nil || !strings.Contains(res.Stdout, "SSH=\n") {
		t.Fatalf("res %+v err %v", res, err)
	}
}

// TestRun_recordErrorKeepsRunError: a CLI that could not start and evidence
// that could not be written are both reported.
func TestRun_recordErrorKeepsRunError(t *testing.T) {
	r, evDir := newRunner(t)
	if err := os.Chmod(r.Bin, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(evDir); err != nil {
		t.Fatal(err)
	}
	_, err := r.Run(context.Background(), "version")
	if err == nil || !strings.Contains(err.Error(), "failed to run orama") || !strings.Contains(err.Error(), "record") {
		t.Fatalf("err %v", err)
	}
}

type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper()                  {}
func (f *fatalTB) Name() string             { return "TestFake" }
func (f *fatalTB) Context() context.Context { return context.Background() }
func (f *fatalTB) Fatal(args ...any)        { f.msg = fmt.Sprint(args...); runtime.Goexit() }

func TestMustOK_failureMessageRedacted(t *testing.T) {
	r, _ := newRunner(t)
	r.Env = []string{"FAKE_EXIT=3"}
	if err := r.Recorder.Redactor().Add("minted-secret-12345"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.Bin, []byte("#!/bin/sh\necho 'token minted-secret-12345 {\"api_key\":\"ak-9999999\"}'\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); r.MustOK(tb, "version") }()
	<-done
	if tb.msg == "" || strings.Contains(tb.msg, "minted-secret-12345") || strings.Contains(tb.msg, "ak-9999999") {
		t.Fatalf("message %q", tb.msg)
	}
}
