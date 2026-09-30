package oramacli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// promptOrama answers a typed confirmation like `namespace rqlite import`.
const promptOrama = `#!/bin/sh
echo "cwd=$(pwd -P)"
echo "GOFLAGS=$GOFLAGS"
printf 'Type the namespace name to confirm: '
read answer
if [ "$answer" = "$1" ]; then echo "confirmed $answer"; exit 0; fi
echo "aborted" >&2
exit 7
`

func promptRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	r, evDir := newRunner(t)
	if err := os.WriteFile(r.Bin, []byte(promptOrama), 0o755); err != nil {
		t.Fatal(err)
	}
	r.WorkDir = t.TempDir()
	return r, evDir
}

func TestRunWith_stdinDirAndEnv(t *testing.T) {
	r, evDir := promptRunner(t)
	dir := filepath.Join(r.WorkDir, "build")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := r.RunWith(context.Background(), RunOpts{Stdin: []byte("myns\n"), Dir: dir, Env: []string{"GOFLAGS=-mod=mod"}}, "myns")
	if err != nil || res.Exit != 0 || !strings.Contains(res.Stdout, "confirmed myns") || !strings.Contains(res.Stdout, "GOFLAGS=-mod=mod") {
		t.Fatalf("res %+v err %v", res, err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(res.Stdout, "cwd="+real) {
		t.Fatalf("not run in %s: %s", real, res.Stdout)
	}
	recs, _ := evidence.Load(evDir)
	if len(recs) != 1 || recs[0].Input != "myns\n" {
		t.Fatalf("stdin not recorded: %+v", recs)
	}
	res, _ = r.RunWith(context.Background(), RunOpts{Stdin: []byte("wrong\n")}, "myns")
	if res.Exit != 7 {
		t.Fatalf("a wrong confirmation exited %d", res.Exit)
	}
}

func TestRunWith_refusesEscapesAndReservedEnv(t *testing.T) {
	r, _ := promptRunner(t)
	outside := t.TempDir()
	link := filepath.Join(r.Home, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]RunOpts{
		"outside":        {Dir: outside},
		"symlink out":    {Dir: link},
		"relative":       {Dir: "home"},
		"dotdot":         {Dir: filepath.Join(r.Home, "..", "..")},
		"missing":        {Dir: filepath.Join(r.Home, "nope")},
		"HOME":           {Env: []string{"HOME=/Users/owner"}},
		"agent":          {Env: []string{"RW_AGENT_SOCK=/x"}},
		"cloud token":    {Env: []string{"HCLOUD_TOKEN=x"}},
		"infisical":      {Env: []string{"INFISICAL_TOKEN=x"}},
		"xdg":            {Env: []string{"XDG_CONFIG_HOME=/x"}},
		"malformed":      {Env: []string{"NOEQUALS"}},
		"both stdins":    {Stdin: []byte("a"), StdinReader: strings.NewReader("b")},
		"reader for Run": {StdinReader: strings.NewReader("b")},
	} {
		if res, err := r.RunWith(context.Background(), o, "x"); err == nil || res.Exit != -1 {
			t.Errorf("%s: ran (exit %d, err %v)", name, res.Exit, err)
		}
	}
}

func TestStartWith_streamsStdin(t *testing.T) {
	r, _ := promptRunner(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	p, err := r.StartWith(context.Background(), RunOpts{StdinReader: pr, Dir: r.Home}, "ns2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.WriteString("ns2\n"); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	res, err := p.Wait()
	if err != nil || res.Exit != 0 || !strings.Contains(res.Stdout, "confirmed ns2") {
		t.Fatalf("res %+v err %v", res, err)
	}
	if _, err := r.StartWith(context.Background(), RunOpts{Dir: "/"}, "x"); err == nil {
		t.Fatal("StartWith ran in /")
	}
}

func TestGoEnv_onlyGoVariables(t *testing.T) {
	env := map[string]string{"GOFLAGS": "-mod=mod", "GOCACHE": "/c", "HOME": "/h", "HCLOUD_TOKEN": "x"}
	got := strings.Join(GoEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok }), " ")
	if got != "GOFLAGS=-mod=mod GOCACHE=/c" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactArgs_secretFlagFamilies(t *testing.T) {
	in := []string{"--key", "k1", "--key-file", "/k", "--password-file=/p", "--token-file", "/t",
		"--secret-key", "s", "--password", "--env", "e2e", "--mnemonic=a b c"}
	got := strings.Join(RedactArgs(in), " ")
	want := "--key [REDACTED] --key-file /k --password-file=[REDACTED] --token-file [REDACTED] " +
		"--secret-key [REDACTED] --password --env e2e --mnemonic=[REDACTED]"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
