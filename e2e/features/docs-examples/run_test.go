//go:build e2e_fleet

package docsexamples

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// readOnly are the documented commands that only read, so an example of one
// is run as written against the fleet. Everything else (deploy, delete,
// login, key minting, node lifecycle) is validated but never executed here:
// its behaviour is the owning feature's test, and running a doc's example of
// it would change the fleet.
var readOnly = map[string]bool{
	"env list": true, "env current": true, "auth status": true, "auth whoami": true, "auth list": true,
	"auth sessions": true, "namespace list": true, "namespace keys list": true, "members list": true,
	"app list": true, "db list": true, "function list": true, "domain list": true, "audit": true,
	"status": true, "nodes": true, "node list": true, "monitor report": true, "monitor cluster": true,
	"monitor alerts": true, "cluster settings show": true, "operator list": true, "version": true,
	"namespace webrtc-status": true,
}

// streaming are flags that make a command run until it is stopped.
var streaming = map[string]bool{"--follow": true, "-f": true, "--watch": true}

// placeholderEnvs are environment names docs use for "your cluster".
var placeholderEnvs = map[string]bool{"devnet": true, "testnet": true, "mainnet": true, "production": true, "stagenet": true}

// runnable rewrites a documented read-only command for this run: the
// environment becomes the run's, the namespace the test's. It reports false
// for a command that is not read-only or keeps a placeholder.
func runnable(cl commandLine, env, namespace string) ([]string, bool) {
	key := commandKey(cl.Args)
	if !readOnly[key] {
		return nil, false
	}
	args := append([]string{}, cl.Args...)
	for i := range args {
		switch {
		case i > 0 && args[i-1] == "--env" && (placeholderEnvs[args[i]] || isPlaceholder(args[i])):
			args[i] = env
		case i > 0 && args[i-1] == "--namespace" && isPlaceholder(args[i]):
			args[i] = namespace
		case isPlaceholder(args[i]) || strings.ContainsAny(args[i], "$`*") || streaming[args[i]]:
			return nil, false
		}
	}
	return args, true
}

// commandKey is the command words of args ("namespace keys list").
func commandKey(args []string) string {
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || !commandWord(a) {
			break
		}
		words = append(words, a)
	}
	// Every leading word, exactly: `auth sessions revoke` must never match
	// the read-only `auth sessions`.
	return strings.Join(words, " ")
}

func commandWord(w string) bool {
	for _, r := range w {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	return w != ""
}

func isPlaceholder(a string) bool {
	return strings.HasPrefix(a, "<") || strings.Contains(a, "your-") || strings.Contains(a, "my-") || strings.Contains(a, "myapp")
}

// TestDocsExamples_readOnlyCommandsRun: every read-only `orama` example in
// the documents runs as written (environment and namespace placeholders
// rewritten to this run's) and succeeds, signed in to a fresh namespace as
// the operator. A failure is a doc bug at the file:line named, or the
// command's.
func TestDocsExamples_readOnlyCommandsRun(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	ran := 0
	for _, cl := range oramaLines(t) {
		args, ok := runnable(cl, f.State.Env, n.Name)
		if !ok {
			continue
		}
		ran++
		if res := infra.Run(t, n.CLI, args...); res.Exit != infra.ExitOK {
			t.Errorf("%s: `orama %s` exited %d:\n%s", cl.Where, strings.Join(args, " "), res.Exit, f.Redact(res.Stdout+res.Stderr))
		}
	}
	if ran == 0 {
		t.Fatal("no read-only orama example found to run: the extraction or the allowlist broke")
	}
}
