package ns

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper()                  {}
func (f *fatalTB) Name() string             { return "TestFake" }
func (f *fatalTB) Context() context.Context { return context.Background() }
func (f *fatalTB) TempDir() string          { return f.TB.TempDir() }
func (f *fatalTB) Cleanup(func())           {}
func (f *fatalTB) Fatal(args ...any)        { f.msg = fmt.Sprint(args...); runtime.Goexit() }

// TestCreateViaOperator_nothingCreatedWhenSetupFails: a failure preparing the
// namespace's CLI must stop the test before `namespace create`, or the
// namespace would exist with no cleanup registered to delete it.
func TestCreateViaOperator_nothingCreatedWhenSetupFails(t *testing.T) {
	cli, log := fakeCLI(t, "open") // its HOME has no environment list
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); createViaOperator(tb, cli, nil, "e2e-x", "https://ns-e2e-x.example") }()
	<-done
	if tb.msg == "" {
		t.Fatal("a missing environment list was accepted")
	}
	if strings.Contains(readFile(t, log), "namespace create") {
		t.Fatalf("the namespace was created before its setup could fail: %s", readFile(t, log))
	}
}

// flakyOperatorCLI stands in for orama: the namespace stays listed until
// `namespace delete`, and the first two sign-ins are rate limited (exit 5).
const flakyOperatorCLI = `#!/bin/sh
d="$(dirname "$0")"
case "$1 $2" in
"auth login")
  n=$(cat "$d/logins" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$d/logins"
  if [ $n -lt 3 ]; then echo "Error: sign-in not accepted right now; retry in 60s: too many authentication attempts" >&2; exit 5; fi ;;
"namespace delete") touch "$d/deleted" ;;
"namespace list") if [ -f "$d/deleted" ]; then echo '[]'; else echo '[{"name":"e2e-x","cluster":"ready"}]'; fi ;;
esac
`

// TestDeleteViaOperator_retriesARateLimitedSignIn: the cleanup of
// TestFunctionCLI_groupListsSubcommands met "too many authentication attempts"
// once, gave up, and left its namespace on stagenet.
func TestDeleteViaOperator_retriesARateLimitedSignIn(t *testing.T) {
	fastTeardown(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "orama")
	if err := os.WriteFile(bin, []byte(flakyOperatorCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &oramacli.Runner{Bin: bin, Home: dir, AgentSock: filepath.Join(dir, "a.sock"),
		RealHome: func() (string, error) { return "/nonexistent/home", nil }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	client, err := gw.NewWithTLS(srv.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := &Namespace{Name: "e2e-x", Client: client, CLI: cli}
	n.deleteViaOperator(t, cli)
	if _, err := os.Stat(filepath.Join(dir, "deleted")); err != nil {
		t.Fatalf("the namespace was never deleted: %v", err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "logins"))); got != "3" {
		t.Fatalf("%s sign-in attempts, want 3 (two refused, one accepted)", got)
	}
}
