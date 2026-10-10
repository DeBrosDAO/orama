//go:build e2e_fleet

package authkeysroles

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// followBudget bounds `orama audit --follow`: printing the history,
	// minting a key, and the next of its five-second polls
	// (cmd/orama/internal/audit_commands.go auditFollowInterval).
	followBudget = time.Minute
	shownOnce    = "API KEY (shown once"
)

var (
	idLine      = regexp.MustCompile(`(?m)^\s+id:\s+(\d+)\s*$`)
	cliKeyShape = regexp.MustCompile(`(?m)^\s*(orama_(?:sk|rk)_[0-9A-Za-z]+_[0-9A-Za-z]+)\s*$`)
)

// cliKeyCreate mints a key with `orama namespace keys create` and revokes it
// at cleanup; it returns the id and the key.
func cliKeyCreate(t testing.TB, cli *oramacli.Runner, args ...string) (string, string) {
	t.Helper()
	out := cli.MustOK(t, append([]string{"namespace", "keys", "create"}, args...)...).Stdout
	id, key := idLine.FindStringSubmatch(out), cliKeyShape.FindStringSubmatch(out)
	if id == nil || key == nil || !strings.Contains(out, shownOnce) {
		t.Fatalf("keys create printed no id, key or shown-once notice")
	}
	protect(t, harness.GW(t), key[1])
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		res, err := cli.Run(ctx, "namespace", "keys", "revoke", "--id", id[1])
		if err != nil || (res.Exit != 0 && res.Exit != exitNotFound) {
			t.Errorf("cleanup: failed to revoke key %s: %v %s", id[1], err, res.Stderr)
		}
	})
	return id[1], key[1]
}

// TestNamespaceKeys_cliLifecycle: create shows the key once, list never
// again, rotate mints a successor, revoke ends it; malformed flags are
// refused (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama namespace keys").
func TestNamespaceKeys_cliLifecycle(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	id, key := cliKeyCreate(t, n.CLI, "--scope", "app-runtime", "--label", "cli-web", "--expires-in-days", "30")
	list := n.CLI.MustOK(t, "namespace", "keys", "ls").Stdout
	if strings.Contains(list, key) || !strings.Contains(list, "#"+id) || !strings.Contains(list, "cli-web") {
		t.Fatalf("keys list shows the key, or not the key's row:\n%s", strings.ReplaceAll(list, key, "<key>"))
	}
	rot := n.CLI.MustOK(t, "namespace", "keys", "rotate", "--id", id, "--overlap-days", "1").Stdout
	newKey := cliKeyShape.FindStringSubmatch(rot)
	if !strings.Contains(rot, "Key "+id+" rotated.") || newKey == nil || newKey[1] == key {
		t.Fatalf("keys rotate did not print a new key")
	}
	protect(t, harness.GW(t), newKey[1])
	if m := idLine.FindStringSubmatch(strings.Replace(rot, "new id:", "id:", 1)); m != nil {
		t.Cleanup(func() {
			if res, err := n.CLI.Run(context.Background(), "namespace", "keys", "revoke", "--id", m[1]); err != nil || (res.Exit != 0 && res.Exit != exitNotFound) {
				t.Errorf("cleanup: failed to revoke the successor %s: %v", m[1], err)
			}
		})
	}
	if out := n.CLI.MustOK(t, "namespace", "keys", "revoke", "--id", id).Stdout; !strings.Contains(out, "Key "+id+" revoked.") {
		t.Errorf("keys revoke printed:\n%s", out)
	}
	for want, args := range map[string][]string{
		"no scope":        {"create", "--label", "x"},
		"rotate id 0":     {"rotate", "--id", "0"},
		"revoke no id":    {"revoke"},
		"too long a life": {"create", "--scope", "cache", "--expires-in-days", "400"},
		"unknown grant":   {"create", "--scope", "root"},
		"revoke again":    {"revoke", "--id", id},
	} {
		if res := runCLI(t, n.CLI, append([]string{"namespace", "keys"}, args...)...); res.Exit == 0 {
			t.Errorf("keys %s succeeded", want)
		}
	}
	if out := n.CLI.MustOK(t, "namespace", "keys", "revoke-legacy", "--force").Stdout; !strings.Contains(out, "Revoked 0 legacy key(s).") {
		t.Errorf("revoke-legacy on a namespace with none:\n%s", out)
	}
	if out := n.CLI.MustOK(t, "namespace", "keys").Stdout; !strings.Contains(out, "revoke-legacy") {
		t.Errorf("`orama namespace keys` does not list its subcommands:\n%s", out)
	}
}

// followPrintsNewEvents: `orama audit --follow` prints the history, keeps
// running, and prints a key.issue recorded after the history was out.
func followPrintsNewEvents(t *testing.T, n *ns.Namespace) {
	t.Helper()
	before := strings.Count(n.CLI.MustOK(t, "audit", "--action", "key.issue").Stdout, "key.issue")
	ctx, cancel := context.WithTimeout(t.Context(), followBudget)
	defer cancel()
	p, err := n.CLI.For(t).Start(ctx, "audit", "--follow", "--action", "key.issue")
	if err != nil {
		t.Fatalf("orama audit --follow: %v", err)
	}
	defer func() {
		if err := p.Kill(); err != nil {
			t.Error(err)
		}
		if _, err := p.Wait(); err != nil {
			t.Errorf("orama audit --follow: %v", err)
		}
	}()
	if err := awaitKeyIssues(ctx, p, before); err != nil {
		t.Fatalf("--follow did not print the %d key.issue events of the history: %v", before, err)
	}
	cliKeyCreate(t, n.CLI, "--scope", "cache", "--label", "followed")
	if err := awaitKeyIssues(ctx, p, 1); err != nil {
		t.Fatalf("--follow printed no key.issue recorded while it ran: %v", err)
	}
}

// awaitKeyIssues reads p's stdout until want more key.issue lines arrived.
func awaitKeyIssues(ctx context.Context, p *oramacli.Proc, want int) error {
	for seen := 0; seen < want; {
		select {
		case line, ok := <-p.StdoutLines():
			if !ok {
				return fmt.Errorf("the command ended after %d of %d", seen, want)
			}
			if strings.Contains(line, "key.issue") {
				seen++
			}
		case <-ctx.Done():
			return fmt.Errorf("saw %d of %d: %w", seen, want, ctx.Err())
		}
	}
	return nil
}

func decodeJSON(s string, v any) error {
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	return nil
}
