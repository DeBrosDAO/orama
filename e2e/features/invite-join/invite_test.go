//go:build e2e_fleet

package invitejoin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// expiresLayout is how invite_tokens and the invite route write expires_at.
	expiresLayout = "2006-01-02 15:04:05"
	// maxInviteLife is the gateway's cap (handlers/operator invite: 60 minutes).
	maxInviteLife = time.Hour
	// clockSlack absorbs the runner's and the node's clocks and the round trip.
	clockSlack = 2 * time.Minute
	pathInvite = "/v1/operator/invite"
)

// minted is `orama invite --json`.
type minted struct {
	Invite        string `json:"invite"`
	JoinURL       string `json:"join_url"`
	SNI           string `json:"sni"`
	ExpiresAt     string `json:"expires_at"`
	CAFingerprint string `json:"ca_fingerprint"`
}

func mint(t testing.TB, args ...string) minted {
	t.Helper()
	f := harness.Fleet(t)
	all := append([]string{"invite", "--env", f.State.Env, "--node", f.State.Nodes[0].PublicIP, "--json"}, args...)
	var m minted
	if err := oramacli.DecodeJSON(harness.CLI(t).MustOK(t, all...), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func expiresIn(t testing.TB, expires string) time.Duration {
	t.Helper()
	at, err := time.ParseInLocation(expiresLayout, strings.TrimSpace(expires), time.UTC)
	if err != nil {
		at, err = time.Parse(time.RFC3339, strings.TrimSpace(expires))
	}
	if err != nil {
		t.Fatalf("expires_at %q is not a time: %v", expires, err)
	}
	return time.Until(at)
}

// TestInvite_namesNodeAndPinsItsCertificate: an invite minted through a node
// names that node by address, the name it serves, and the SHA-256 of the
// certificate it serves for that name, so the joiner pins the node that
// minted the token (docs/CLI_REFERENCE.md "orama invite", docs/SECURITY.md
// "TLS & Transport").
func TestInvite_namesNodeAndPinsItsCertificate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	m := mint(t)
	inv := infra.DecodeInvite(t, m.Invite)
	if inv.JoinURL != "https://"+n.PublicIP || m.JoinURL != inv.JoinURL {
		t.Errorf("the invite joins through %q (json %q), want https://%s", inv.JoinURL, m.JoinURL, n.PublicIP)
	}
	if inv.SNI == "" || inv.SNI != m.SNI {
		t.Errorf("the invite's server name %q differs from the JSON's %q", inv.SNI, m.SNI)
	}
	served := infra.ServedFingerprint(t, n.PublicIP, inv.SNI)
	if !strings.EqualFold(inv.CAFingerprint, served) || !strings.EqualFold(m.CAFingerprint, served) {
		t.Errorf("the invite pins %s, %s serves %s", inv.CAFingerprint, n.Name, served)
	}
	if d := expiresIn(t, m.ExpiresAt); d <= 0 || d > maxInviteLife+clockSlack {
		t.Errorf("a default invite expires in %s, want about an hour", d)
	}
}

// TestInvite_storedOnlyAsHash: the registry holds sha256 of the token, never
// the token: the raw value matches no row, the hash matches exactly one, not
// yet used (docs/SECURITY.md: invite tokens are stored hashed).
func TestInvite_storedOnlyAsHash(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	inv := infra.DecodeInvite(t, mint(t).Invite)
	for _, n := range f.State.Nodes {
		raw := infra.IndexQuery(t, f, n, "SELECT COUNT(*) FROM invite_tokens WHERE token = ? OR token LIKE ?", inv.Token, "%"+inv.Token+"%")
		if c, _ := raw.Values[0][0].(float64); c != 0 {
			t.Errorf("%s: the raw token is stored in invite_tokens", n.Name)
		}
		row := infra.ReadInvite(t, f, n, inv.Token)
		if !row.Found || row.Used {
			t.Errorf("%s: the hashed token row is %+v, want one unused row", n.Name, row)
		}
	}
}

// TestInvite_expiryCappedAtAnHour: however long an invite is asked for, the
// gateway caps it at an hour (`orama invite --expiry`: "the gateway caps it
// at 1h").
func TestInvite_expiryCappedAtAnHour(t *testing.T) {
	t.Parallel()
	for _, ask := range []string{"3h", "8760h"} {
		m := mint(t, "--expiry", ask)
		if d := expiresIn(t, m.ExpiresAt); d > maxInviteLife+clockSlack {
			t.Errorf("--expiry %s gave an invite that lives %s", ask, d)
		}
	}
}

// TestInvite_shortExpiryIsHonoured: an invite asked for 30 seconds must not
// outlive a minute ("How long the invite stays usable"). A sub-minute expiry
// that the CLI rounds to zero minutes becomes the gateway's default hour.
func TestInvite_shortExpiryIsHonoured(t *testing.T) {
	t.Parallel()
	m := mint(t, "--expiry", "30s")
	if d := expiresIn(t, m.ExpiresAt); d > time.Minute+clockSlack {
		t.Fatalf("--expiry 30s gave an invite that lives %s", d)
	}
}

// TestInvite_refusals: a non-positive expiry is a usage error; a node that
// is not in the cluster is unavailable; a caller with no credentials is not
// let mint (docs/CLI_REFERENCE.md "orama invite").
func TestInvite_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	for _, bad := range []string{"0s", "-5m"} {
		infra.ExpectExit(t, infra.Run(t, cli, "invite", "--env", f.State.Env, "--expiry", bad), infra.ExitUsage, "--expiry must be positive")
	}
	infra.ExpectRefused(t, infra.Run(t, cli, "invite", "--env", f.State.Env, "--node", "192.0.2.1"), "192.0.2.1")
	infra.ExpectExit(t, infra.Run(t, cli.Isolated(t), "invite", "--env", f.State.Env, "--node", f.State.Nodes[0].PublicIP), infra.ExitAuth)
}

// TestInviteRoute_operatorsOnly: the invite route refuses no credential
// (401), a signed-in wallet with no grant (403: the route needs the admin
// grant), and the admin of a namespace who is not on the operator list (403
// NOT_AN_OPERATOR), and mints nothing for them (docs/AUTH.md "Operating the
// cluster": the admin grant and the operator list).
func TestInviteRoute_operatorsOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	body := []byte(`{"expiry_minutes":5}`)
	h := http.Header{"Content-Type": {"application/json"}}
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathInvite, Header: h, Body: body}); r.Status != http.StatusUnauthorized {
		t.Errorf("no credential: HTTP %d, want 401: %.200s", r.Status, r.Body)
	}
	u := gw.NewUser(t, f, gw.LobbyNamespace)
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathInvite, Header: h, Body: body, Bearer: u.Token()}); r.Status != http.StatusForbidden {
		t.Errorf("a wallet with no grant: HTTP %d %s, want 403", r.Status, r.ErrorCode())
	}
	admin := ns.New(t, f, ns.Options{}).Owner
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathInvite, Header: h, Body: body, Bearer: admin.Token()})
	if r.Status != http.StatusForbidden || r.ErrorCode() != "NOT_AN_OPERATOR" {
		t.Errorf("a namespace admin who is not an operator: HTTP %d %s, want 403 NOT_AN_OPERATOR", r.Status, r.ErrorCode())
	}
	if r := c.MustSend(t, gw.Req{Method: http.MethodGet, Path: pathInvite, Bearer: u.Token()}); r.Status < 400 {
		t.Errorf("GET %s answered %d", pathInvite, r.Status)
	}
}
