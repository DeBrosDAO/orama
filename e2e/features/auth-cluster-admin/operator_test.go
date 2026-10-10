//go:build e2e_fleet

package authclusteradmin

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// deleteBudget bounds deleting a namespace the quota failed to stop.
	deleteBudget = 5 * time.Minute
	// leakPollEvery paces signing in to a namespace that is still provisioning.
	leakPollEvery = 5 * time.Second
	// lastOperatorText is the refusal to empty the operator list
	// (core/pkg/gateway/handlers/operator/operators.go).
	lastOperatorText = "last operator"
)

// rewroteLine is `orama maint operator rotate-secrets`' index summary.
var rewroteLine = regexp.MustCompile(`Index:\s+scanned (\d+), rewrote (\d+), skipped (\d+)`)

// TestWalletCap_namespaceQuota: with the per-wallet cap at 1, a wallet that
// owns a namespace is refused a second with 403 NAMESPACE_QUOTA
// (docs/API_SURFACE.md "/v1/namespaces": per-wallet cap).
func TestWalletCap_namespaceQuota(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	cli := harness.CLI(t)
	c := harness.GW(t)
	setSetting(t, cli, settingCap, "1")
	if got := showSettings(t, cli).Cap; got != 1 {
		t.Fatalf("settings show says cap %d after setting 1", got)
	}
	owner := lobbyToken(t, c, n.Owner.Wallet)
	second := ns.UniqueName(t.Name() + "/second")
	resp := createAs(t, c, owner, second)
	if resp.Status == http.StatusAccepted || resp.Status == http.StatusCreated {
		t.Cleanup(func() { deleteLeaked(t, c, n.Owner.Wallet, second) })
	}
	expectCode(t, resp, http.StatusForbidden, codeQuota)
	// Another wallet owns nothing and is still admitted by the cap.
	expectAllowed(t, c, lobbyToken(t, c, newWallet(t)), n.Name, "a wallet that owns nothing")
}

// deleteLeaked removes a namespace a failed assertion let through: its owner
// signs in to it once it serves and deletes it.
func deleteLeaked(t testing.TB, c *gw.Client, w *wallet.EVM, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deleteBudget)
	defer cancel()
	err := eventually.Poll(ctx, leakPollEvery, deleteBudget, "leaked namespace "+name+" to be deleted", func() (bool, error) {
		s, err := c.SignIn(ctx, w, name, nil)
		if err != nil {
			return false, err
		}
		_, err = c.JSON(ctx, http.MethodDelete, ns.PathDelete, s.AccessToken, nil, nil)
		return err == nil, err
	})
	if err != nil {
		t.Errorf("cleanup: namespace %s leaked: %v", name, err)
	}
}

// TestOperator_addListRemove: operators are listed, added idempotently and
// removed (docs/AUTH.md#operating-the-cluster).
func TestOperator_addListRemove(t *testing.T) {
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	if out := cli.MustOK(t, "maint", "operator", "list").Stdout; !containsFold(out, f.State.OperatorAddress) {
		t.Fatalf("operator list does not show the run's operator %s:\n%s", f.State.OperatorAddress, out)
	}
	w := newWallet(t).Address()
	cli.MustOK(t, "maint", "operator", "add", w)
	t.Cleanup(func() { removeOperator(t, cli, w) })
	cli.MustOK(t, "maint", "operator", "add", w) // idempotent
	if out := cli.MustOK(t, "maint", "operator", "list").Stdout; strings.Count(strings.ToLower(out), strings.ToLower(w)) != 1 {
		t.Errorf("after adding %s twice the list shows it %d times:\n%s", w, strings.Count(strings.ToLower(out), strings.ToLower(w)), out)
	}
	cli.MustOK(t, "maint", "operator", "remove", w)
	for _, args := range [][]string{{"remove", w}, {"add", "not-a-wallet"}, {"add"}, {"remove"}} {
		if res := runCLI(t, cli, append([]string{"operator"}, args...)...); res.Exit == 0 {
			t.Errorf("orama maint operator %v succeeded", args)
		}
	}
}

// TestOperator_neverRemovesTheLast: removing the only operator is refused and
// leaves it listed (docs/AUTH.md#operating-the-cluster). It applies only to a
// cluster with exactly one operator, and the test never removes an operator
// to make one: removing a real operator is not something a run may undo
// safely. The package's feature.yaml does not claim this promise for that
// reason; a run whose stage 1 leaves one operator covers it.
func TestOperator_neverRemovesTheLast(t *testing.T) {
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	out := cli.MustOK(t, "maint", "operator", "list").Stdout
	if lines := nonEmptyLines(out); len(lines) != 1 {
		harness.SkipNotApplicable(t, fmt.Sprintf("the cluster lists %d operators, so removing the run's operator %s "+
			"would not remove the last one; the refusal is only observable on a cluster with exactly one operator, "+
			"and this test does not remove other operators to get there:\n%s", len(lines), f.State.OperatorAddress, out))
	}
	res := runCLI(t, cli, "maint", "operator", "remove", f.State.OperatorAddress)
	if res.Exit == 0 {
		t.Fatalf("THE LAST OPERATOR WAS REMOVED: the cluster has no operator left and later stages cannot operate it")
	}
	if !strings.Contains(res.Stdout+res.Stderr, lastOperatorText) {
		t.Errorf("the refusal does not say why:\n%s%s", res.Stdout, res.Stderr)
	}
	if out := cli.MustOK(t, "maint", "operator", "list").Stdout; !containsFold(out, f.State.OperatorAddress) {
		t.Fatal("the run's operator is no longer listed")
	}
}

// TestOperatorRoutes_ownerIsNotAnOperator: holding everything in one's own
// namespace is not operating the cluster (NOT_AN_OPERATOR).
func TestOperatorRoutes_ownerIsNotAnOperator(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	for _, r := range []gw.Req{
		{Method: http.MethodPost, Path: "/v1/operator/operators", Body: []byte(`{"wallet":"` + n.Owner.Wallet.Address() + `"}`)},
		{Method: http.MethodDelete, Path: "/v1/operator/operators/" + f.State.OperatorAddress},
		{Method: http.MethodPost, Path: "/v1/operator/rotate-secrets", Body: []byte(`{}`)},
		{Method: http.MethodPost, Path: "/v1/operator/rotate-signing-key"},
	} {
		r.Bearer = n.Owner.Token()
		resp := c.MustSend(t, r)
		if resp.Status != http.StatusForbidden || resp.ErrorCode() != "NOT_AN_OPERATOR" {
			t.Errorf("%s %s as an owner: want 403 NOT_AN_OPERATOR, got %d %s", r.Method, r.Path, resp.Status, resp.Body)
		}
	}
	if out := harness.CLI(t).MustOK(t, "maint", "operator", "list").Stdout; containsFold(out, n.Owner.Wallet.Address()) {
		t.Fatal("a namespace owner added itself to the operator list")
	}
}

// TestRotateSecrets_idempotent: rewriting stored secrets twice leaves nothing
// for the second run to rewrite (docs/CLI_REFERENCE.md "The walker is
// idempotent"). --rotate is not exercised: a new encryption root cannot be
// undone, and the upgrade stage still has to read this cluster's rows.
func TestRotateSecrets_idempotent(t *testing.T) {
	cli := harness.CLI(t)
	first := rewroteCount(t, cli.MustOK(t, "maint", "operator", "rotate-secrets").Stdout)
	second := rewroteCount(t, cli.MustOK(t, "maint", "operator", "rotate-secrets").Stdout)
	if second != 0 {
		t.Fatalf("the second rewrite rewrote %d rows (first rewrote %d): the walk is not idempotent", second, first)
	}
}

func rewroteCount(t testing.TB, out string) int {
	t.Helper()
	if !strings.Contains(out, "Stored secrets rewritten.") || !strings.Contains(out, "IKM:        unchanged") {
		t.Fatalf("rotate-secrets output:\n%s", out)
	}
	m := rewroteLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("rotate-secrets printed no index summary:\n%s", out)
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func removeOperator(t testing.TB, cli *oramacli.Runner, w string) {
	t.Helper()
	ctx, cancel := fleet.CleanupContext(t)
	defer cancel()
	res, err := cli.Run(ctx, "maint", "operator", "list")
	if err != nil || !containsFold(res.Stdout, w) {
		return
	}
	if res, err := cli.Run(ctx, "maint", "operator", "remove", w); err != nil || res.Exit != 0 {
		t.Errorf("cleanup: failed to remove operator %s: %v %s", w, err, res.Stderr)
	}
}

func runCLI(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
