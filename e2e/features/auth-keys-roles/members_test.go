//go:build e2e_fleet

package authkeysroles

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const pathTransfer = pathMembers + "/transfer"

// TestMembers_cliLifecycle drives `orama members` as the operator owning the
// namespace: list, add with each role, the refusals, remove (docs/AUTH.md#roles).
func TestMembers_cliLifecycle(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	if out := n.CLI.MustOK(t, "members", "list").Stdout; !containsFold(out, f.State.OperatorAddress) || !strings.Contains(out, roleOwner) {
		t.Fatalf("members list does not show the operator as owner:\n%s", out)
	}
	dev, rt := newWallet(t).Address(), newWallet(t).Address()
	out := n.CLI.MustOK(t, "members", "add", dev, "--role", roleDev, "--name", "e2e dev").Stdout
	if !containsFold(out, dev) || !strings.Contains(out, "is now developer") {
		t.Errorf("members add printed:\n%s", out)
	}
	n.CLI.MustOK(t, "members", "add", rt, "--role", roleRuntime, "--resource", "pubsub:topic=chat.*", "--expires-in-hours", "1")
	listed := n.CLI.MustOK(t, "members", "ls").Stdout
	for _, w := range []string{dev, rt} {
		if !containsFold(listed, w) {
			t.Errorf("members ls does not show %s:\n%s", w, listed)
		}
	}
	for want, args := range map[int][]string{
		exitUsage:    {"add", newWallet(t).Address()},
		exitConflict: {"remove", f.State.OperatorAddress},
	} {
		if res := runCLI(t, n.CLI, append([]string{"members"}, args...)...); res.Exit != want {
			t.Errorf("members %v: want exit %d, got %d %s", args, want, res.Exit, res.Stderr)
		}
	}
	for _, args := range [][]string{
		{"add", newWallet(t).Address(), "--role", roleOwner}, {"add", newWallet(t).Address(), "--role", "wizard"},
		{"add", newWallet(t).Address(), "--role", roleRuntime, "--expires-in-hours", "9000"},
		{"add", newWallet(t).Address(), "--role", roleRuntime, "--resource", "db:table=posts"},
	} {
		if res := runCLI(t, n.CLI, append([]string{"members"}, args...)...); res.Exit != exitUsage {
			t.Errorf("members %v: want exit %d, got %d", args, exitUsage, res.Exit)
		}
	}
	n.CLI.MustOK(t, "members", "rm", dev)
	if res := runCLI(t, n.CLI, "members", "remove", dev); res.Exit != exitNotFound {
		t.Errorf("removing a wallet with no grant: want exit %d, got %d", exitNotFound, res.Exit)
	}
	if out := n.CLI.MustOK(t, "members").Stdout; !strings.Contains(out, "transfer") {
		t.Errorf("`orama members` does not list its subcommands:\n%s", out)
	}
}

// TestMembers_removalLandsOnTheNextRequest: a wallet's grant is resolved per
// request, so taking it away refuses its very next request
// (docs/AUTH.md#how-long-a-change-takes-to-land).
func TestMembers_removalLandsOnTheNextRequest(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	w, tok := memberToken(t, n, roleDev, "")
	// The query goes to the namespace's own database, at its host.
	send(t, n.Client, http.MethodPost, pathQuery, tok, map[string]string{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
	send(t, c, http.MethodDelete, pathMembers+"/"+w.Address(), n.Owner.Token(), nil).Expect(t, http.StatusOK)
	if r := send(t, n.Client, http.MethodPost, pathQuery, tok, map[string]string{"sql": "SELECT 1"}); r.Status != http.StatusForbidden {
		t.Errorf("the removed developer's next request: want 403, got %d %s", r.Status, r.Body)
	}
	if r := send(t, c, http.MethodDelete, pathMembers+"/"+n.Owner.Wallet.Address(), n.Owner.Token(), nil); r.Status != http.StatusConflict {
		t.Errorf("removing the owner: want 409, got %d", r.Status)
	}
}

// TestMembers_transferIsOneStep: only the owner transfers; the new owner is
// owner and the old one keeps admin, with no moment without an owner.
func TestMembers_transferIsOneStep(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	c := harness.GW(t)
	heir := newWallet(t)
	n.CLI.MustOK(t, "members", "add", heir.Address(), "--role", roleAdmin)
	heirTok := signIn(t, n, heir)
	r := send(t, c, http.MethodPost, pathTransfer, heirTok, map[string]string{"wallet": heir.Address()})
	if r.Status != http.StatusForbidden || r.ErrorCode() != "OWNERSHIP_REQUIRED" {
		t.Fatalf("an admin transferring the namespace: want 403 OWNERSHIP_REQUIRED, got %d %s", r.Status, r.Body)
	}
	n.CLI.MustOK(t, "members", "transfer", heir.Address(), "--force")
	t.Cleanup(func() { transferBack(t, n, c, heir, f.State.OperatorAddress) })
	out := n.CLI.MustOK(t, "members", "list").Stdout
	if !lineHas(out, heir.Address(), roleOwner) || !lineHas(out, f.State.OperatorAddress, roleAdmin) {
		t.Fatalf("after the transfer the heir is not owner and the operator not admin:\n%s", out)
	}
	if res := runCLI(t, n.CLI, "members", "transfer", heir.Address(), "--force"); res.Exit == 0 {
		t.Error("the former owner transferred the namespace again")
	}
}

// signIn signs w in to n through the main gateway. It does not go through
// n.Owner: a namespace created by the operator (ns.ViaOperator) has none.
func signIn(t testing.TB, n *ns.Namespace, w *wallet.EVM) string {
	t.Helper()
	s, err := harness.GW(t).For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.AccessToken
}

// transferBack returns the namespace to the operator, so its deletion (as the
// operator) works and the run leaks nothing.
func transferBack(t testing.TB, n *ns.Namespace, c *gw.Client, heir *wallet.EVM, operator string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	err := eventually.Poll(ctx, pollEvery, cleanupBudget, "the namespace to return to the operator", func() (bool, error) {
		s, err := c.SignIn(ctx, heir, n.Name, nil)
		if err != nil {
			return false, err
		}
		_, err = c.JSON(ctx, http.MethodPost, pathTransfer, s.AccessToken, map[string]string{"wallet": operator}, nil)
		return err == nil, err
	})
	if err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

func lineHas(out string, parts ...string) bool {
	for _, line := range strings.Split(out, "\n") {
		ok := true
		for _, p := range parts {
			ok = ok && containsFold(line, p)
		}
		if ok {
			return true
		}
	}
	return false
}

// TestMembers_httpRefusals: bad roles, owner as a role, out-of-range expiry
// and a missing wallet are 400; a member without members-write cannot list.
func TestMembers_httpRefusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	for name, body := range map[string]map[string]any{
		"no wallet":    {"role": roleRuntime},
		"unknown role": {"wallet": newWallet(t).Address(), "role": "wizard"},
		"owner role":   {"wallet": newWallet(t).Address(), "role": roleOwner},
		"expiry -1":    {"wallet": newWallet(t).Address(), "role": roleRuntime, "expires_in_hours": -1},
		"expiry 8761":  {"wallet": newWallet(t).Address(), "role": roleRuntime, "expires_in_hours": 8761},
	} {
		if r := send(t, c, http.MethodPost, pathMembers, n.Owner.Token(), body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, r.Status, r.Body)
		}
	}
	_, rtTok := memberToken(t, n, roleRuntime, "")
	refusal(t, send(t, c, http.MethodGet, pathMembers, rtTok, nil), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	if r := send(t, c, http.MethodDelete, pathMembers+"/"+newWallet(t).Address(), n.Owner.Token(), nil); r.Status != http.StatusNotFound {
		t.Errorf("removing a stranger: want 404, got %d", r.Status)
	}
	if r := send(t, c, http.MethodPost, pathTransfer, n.Owner.Token(), map[string]string{"wallet": n.Owner.Wallet.Address()}); r.Status != http.StatusBadRequest {
		t.Errorf("transferring to oneself: want 400, got %d", r.Status)
	}
}
