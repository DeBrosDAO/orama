//go:build e2e_fleet

package authclusteradmin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Creation refusals (core/pkg/gateway/handlers/namespace/create_handler.go).
const (
	codeDenied = "NAMESPACE_CREATION_DENIED"
	codeTaken  = "NAMESPACE_TAKEN"
	codeQuota  = "NAMESPACE_QUOTA"
	// codeTransferRefused is a transfer the recipient cannot take; it does not
	// say the recipient is at its cap.
	codeTransferRefused = "TRANSFER_REFUSED"
	operatorsMsg        = "only an operator"
	allowlistMsg        = "namespace-creator list"
	takenMsg            = "already exists"
)

// The creation policy is checked before the name's existence, so asking for
// a name that is already taken tells an allowed wallet (409) from a refused
// one (403) without provisioning anything.

// expectAllowed: the wallet passed the policy and hit the existing name.
func expectAllowed(t testing.TB, c *gw.Client, bearer, taken, who string) {
	t.Helper()
	resp := createAs(t, c, bearer, taken)
	if resp.Status != http.StatusConflict || resp.ErrorCode() != codeTaken {
		t.Fatalf("%s should pass the creation policy: want 409 %s, got %d %s", who, codeTaken, resp.Status, resp.Body)
	}
}

// expectDenied: the policy refused the wallet, saying why.
func expectDenied(t testing.TB, c *gw.Client, bearer, taken, who, why string) {
	t.Helper()
	resp := createAs(t, c, bearer, taken)
	expectCode(t, resp, http.StatusForbidden, codeDenied)
	if !strings.Contains(string(resp.Body), why) {
		t.Errorf("%s: the refusal does not say %q: %s", who, why, resp.Body)
	}
}

// operatorCreate is the operator asking for taken through the CLI.
func operatorCreate(t testing.TB, cli *oramacli.Runner, taken string) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), "namespace", "create", taken)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestCreationMode_operatorsAdmitsOnlyOperators: in `operators` mode a user
// is refused and the operator is admitted (docs/AUTH.md#the-lobby,
// docs/CLI_REFERENCE.md "orama cluster settings set").
func TestCreationMode_operatorsAdmitsOnlyOperators(t *testing.T) {
	f := harness.Fleet(t)
	taken := ns.New(t, f, ns.Options{}).Name
	cli := harness.CLI(t)
	c := harness.GW(t)
	user := lobbyToken(t, c, newWallet(t))
	setSetting(t, cli, settingMode, modeOperators)
	if got := showSettings(t, cli).Mode; got != modeOperators {
		t.Fatalf("settings show says %q after setting %q", got, modeOperators)
	}
	expectDenied(t, c, user, taken, "a user in operators mode", operatorsMsg)
	if res := operatorCreate(t, cli, taken); res.Exit == 0 || !strings.Contains(res.Stdout+res.Stderr, takenMsg) {
		t.Fatalf("the operator should pass the policy and hit the taken name: exit %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// TestCreationMode_allowlistAdmitsOnlyListed: only listed wallets pass; an
// operator is not on the list unless added; removing takes it away again;
// an empty list admits nobody.
func TestCreationMode_allowlistAdmitsOnlyListed(t *testing.T) {
	f := harness.Fleet(t)
	taken := ns.New(t, f, ns.Options{}).Name
	cli := harness.CLI(t)
	c := harness.GW(t)
	w := newWallet(t)
	user := lobbyToken(t, c, w)
	setSetting(t, cli, settingMode, modeAllowlist)
	expectDenied(t, c, user, taken, "an unlisted user", allowlistMsg)
	if res := operatorCreate(t, cli, taken); res.Exit == 0 || strings.Contains(res.Stdout+res.Stderr, takenMsg) {
		t.Errorf("an unlisted operator passed the allowlist: exit %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	cli.MustOK(t, "cluster", "creators", "add", w.Address())
	t.Cleanup(func() { removeCreator(t, cli, w.Address()) })
	if out := cli.MustOK(t, "cluster", "creators", "list").Stdout; !containsFold(out, w.Address()) {
		t.Errorf("creators list does not show %s:\n%s", w.Address(), out)
	}
	expectAllowed(t, c, user, taken, "a listed user")
	cli.MustOK(t, "cluster", "creators", "remove", w.Address())
	expectDenied(t, c, user, taken, "a user taken off the list", allowlistMsg)
}

// TestCreationMode_openAdmitsAnySignedInWallet: `open` lets any EVM wallet in.
func TestCreationMode_openAdmitsAnySignedInWallet(t *testing.T) {
	f := harness.Fleet(t)
	taken := ns.New(t, f, ns.Options{}).Name
	cli := harness.CLI(t)
	c := harness.GW(t)
	setSetting(t, cli, settingMode, modeOpen)
	expectAllowed(t, c, lobbyToken(t, c, newWallet(t)), taken, "a stranger in open mode")
}

// TestClusterSettings_invalidValuesRefused: the CLI refuses bad values locally
// (exit 2) and the gateway refuses them too (400); nothing changes.
func TestClusterSettings_invalidValuesRefused(t *testing.T) {
	cli := harness.CLI(t)
	before := showSettings(t, cli)
	for _, args := range [][]string{
		{settingMode, "everyone"}, {settingMode, ""}, {settingCap, "0"}, {settingCap, "10001"},
		{settingCap, "-1"}, {settingCap, "ten"}, {"no-such-setting", "1"},
	} {
		res, err := cli.For(t).Run(t.Context(), append([]string{"cluster", "settings", "set"}, args...)...)
		if err != nil || res.Exit == 0 {
			t.Errorf("settings set %q accepted (exit %d): %v", args, res.Exit, err)
		}
	}
	if after := showSettings(t, cli); after != before {
		t.Fatalf("refused values changed the settings: %+v -> %+v", before, after)
	}
}

// TestClusterSettings_refusedToNonOperators: a namespace owner's credential is
// not an operator's (docs/API_SURFACE.md "Node and operator").
func TestClusterSettings_refusedToNonOperators(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	for _, r := range []gw.Req{
		{Path: "/v1/operator/settings"},
		{Method: http.MethodPut, Path: "/v1/operator/settings/namespace-creation", Body: []byte(`{"value":"open"}`)},
		{Path: "/v1/operator/creators"},
		{Method: http.MethodPost, Path: "/v1/operator/creators", Body: []byte(`{"wallet":"` + n.Owner.Wallet.Address() + `"}`)},
		{Path: "/v1/operator/operators"},
	} {
		r.Bearer = n.Owner.Token()
		resp := c.MustSend(t, r)
		if resp.Status != http.StatusForbidden {
			t.Errorf("%s %s as a namespace owner: want 403, got %d %s", r.Method, r.Path, resp.Status, resp.Body)
			continue
		}
		if code := resp.ErrorCode(); code != "NOT_AN_OPERATOR" && code != "INSUFFICIENT_SCOPE" {
			t.Errorf("%s %s: refusal code %q", r.Method, r.Path, code)
		}
		if anon := c.MustSend(t, gw.Req{Method: r.Method, Path: r.Path, Body: r.Body}); anon.Status != http.StatusUnauthorized {
			t.Errorf("%s %s anonymously: want 401, got %d", r.Method, r.Path, anon.Status)
		}
	}
}

func removeCreator(t testing.TB, cli *oramacli.Runner, w string) {
	t.Helper()
	ctx, cancel := fleet.CleanupContext(t)
	defer cancel()
	res, err := cli.Run(ctx, "cluster", "creators", "list")
	if err != nil || !containsFold(res.Stdout, w) {
		return
	}
	if res, err := cli.Run(ctx, "cluster", "creators", "remove", w); err != nil || res.Exit != 0 {
		t.Errorf("cleanup: failed to remove creator %s: %v %s", w, err, res.Stderr)
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// TestClusterCreators_invalidInputsRefused: a malformed wallet, a wallet not
// on the list and a missing argument all fail and change nothing.
func TestClusterCreators_invalidInputsRefused(t *testing.T) {
	cli := harness.CLI(t)
	before := cli.MustOK(t, "cluster", "creators", "list").Stdout
	for _, args := range [][]string{
		{"add", "not-a-wallet"}, {"add", "0x" + strings.Repeat("z", 40)}, {"add"},
		{"remove", newWallet(t).Address()}, {"remove"},
	} {
		res, err := cli.For(t).Run(t.Context(), append([]string{"cluster", "creators"}, args...)...)
		if err != nil || res.Exit == 0 {
			t.Errorf("cluster creators %q succeeded (exit %d): %v", args, res.Exit, err)
		}
	}
	if after := cli.MustOK(t, "cluster", "creators", "list").Stdout; after != before {
		t.Fatalf("refused inputs changed the creator list:\n%s\n->\n%s", before, after)
	}
}

// TestClusterGroups_listSubcommands: the group commands print what they hold.
func TestClusterGroups_listSubcommands(t *testing.T) {
	cli := harness.CLI(t)
	for group, subs := range map[string][]string{
		"cluster":          {"creators", "settings", "register-onchain", "retire-onchain"},
		"cluster creators": {"add", "list", "remove"},
		"cluster settings": {"set", "show"},
		"operator":         {"add", "list", "remove", "rotate-secrets", "rotate-signing-key"},
	} {
		out := cli.MustOK(t, strings.Fields(group)...).Stdout
		for _, sub := range subs {
			if !strings.Contains(out, sub) {
				t.Errorf("`orama %s` does not list %q:\n%s", group, sub, out)
			}
		}
	}
}
