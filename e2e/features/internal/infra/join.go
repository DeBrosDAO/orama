//go:build e2e_fleet

package infra

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/pkg/invite"
)

// Join route and its refusals (core/pkg/gateway/handlers/join/handler.go).
const (
	JoinPath        = "/v1/internal/join"
	JoinUsed        = "this invite has already been used"
	JoinExpired     = "this invite has expired"
	JoinUnknown     = "no invite matches this token"
	JoinIdentityDup = "identity already registered"
	// InviteHashPrefix prefixes what invite_tokens stores: sha256 of the token
	// (core/pkg/gateway/handlers/operator/authorize.go HashInviteToken).
	InviteHashPrefix = "sha256:"
	// wgKeyBytes is a WireGuard public key's length.
	wgKeyBytes = 32
)

// JoinBody is the join request a joining node sends.
type JoinBody struct {
	Token       string `json:"token"`
	WGPublicKey string `json:"wg_public_key"`
	PublicIP    string `json:"public_ip"`
}

// NewWGKey is a random, well-formed WireGuard public key no node has.
func NewWGKey(t testing.TB) string {
	t.Helper()
	b := make([]byte, wgKeyBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// PostJoin sends raw to the join route of node n, through the public name
// pinned to n's address (the minting node the invite names).
func PostJoin(t testing.TB, n fleet.Node, raw []byte) *gw.Response {
	t.Helper()
	c := harness.GW(t).PinTo(n.PublicIP)
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: JoinPath,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: raw})
}

// Join sends a well-formed join request.
func Join(t testing.TB, n fleet.Node, b JoinBody) *gw.Response {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return PostJoin(t, n, raw)
}

// DecodeInvite reads an encoded invite (orama1_...).
func DecodeInvite(t testing.TB, encoded string) invite.Invite {
	t.Helper()
	inv, err := invite.Decode(strings.TrimSpace(encoded))
	if err != nil {
		t.Fatalf("the minted invite does not decode: %v", err)
	}
	return inv
}

// HashToken is what the cluster stores for a raw token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return InviteHashPrefix + hex.EncodeToString(sum[:])
}

// ServedFingerprint is the SHA-256 of the certificate ip:443 serves for sni,
// read without verification: the invite's pin is what is being checked.
func ServedFingerprint(t testing.TB, ip, sni string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), dialBudget)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{ServerName: sni, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, httpsPort))
	if err != nil {
		t.Fatalf("TLS to %s for %s: %v", ip, sni, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		t.Fatalf("%s served no certificate for %s", ip, sni)
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:])
}

// InviteRow is one invite_tokens row, looked up by the stored hash.
type InviteRow struct {
	Found   bool
	Used    bool
	UsedBy  string
	Expires string
}

// ReadInvite reads the invite_tokens row of a raw token on n's index rqlite.
func ReadInvite(t testing.TB, f *fleet.Fleet, n fleet.Node, token string) InviteRow {
	t.Helper()
	q := IndexQuery(t, f, n, "SELECT used_at IS NOT NULL, COALESCE(used_by_ip, ''), expires_at FROM invite_tokens WHERE token = ?", HashToken(token))
	if len(q.Values) == 0 {
		return InviteRow{}
	}
	row := q.Values[0]
	used, _ := row[0].(float64)
	by, _ := row[1].(string)
	exp, _ := row[2].(string)
	return InviteRow{Found: true, Used: used == 1, UsedBy: by, Expires: exp}
}

// ForgetPhantomOnCleanup registers a cleanup for a test that sends joins it
// expects refused, naming ip, an address no machine has: should one go
// through after all, ip is a member of the cluster, and the cleanup retires
// it with `orama node remove --offline --force` (nothing to wipe) so the
// refusal's regression does not leave a phantom peer for the later stages.
func ForgetPhantomOnCleanup(t testing.TB, ip string) {
	t.Helper()
	f := harness.Fleet(t)
	t.Cleanup(func() {
		q, err := IndexQueryInCleanup(t, f, f.State.Nodes[0], "SELECT COUNT(*) FROM wireguard_peers WHERE public_ip = ?", ip)
		if err != nil {
			t.Errorf("cleanup: cannot tell whether %s joined as a phantom peer: %v", ip, err)
			return
		}
		if c, _ := q.Values[0][0].(float64); c == 0 {
			return
		}
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		res, err := harness.CLI(t).For(t).Run(ctx, "node", "remove", "--env", f.State.Env, "--offline", "--force", "--node", ip)
		if err != nil || res.Exit != ExitOK {
			t.Errorf("cleanup: a refused join made %s a member and removing it failed (exit %d): %v\n%s",
				ip, res.Exit, err, f.Redact(res.Stdout+res.Stderr))
		}
	})
}
