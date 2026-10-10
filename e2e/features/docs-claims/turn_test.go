//go:build e2e_fleet

package docsclaims

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Where the two TURN credential lifetimes are defined.
const (
	turnServerSrc  = "core/pkg/turn/server.go"
	namespaceTypes = "core/pkg/namespace/types.go"
	// restTTLSeconds is DefaultCredentialTTL (24 * time.Hour) in seconds.
	restTTLSeconds = 24 * 60 * 60
	// sfuTTLSeconds is DefaultTURNCredentialTTL, the SFU-signalled default.
	sfuTTLSeconds = 600
	turnCredPath  = "/v1/webrtc/turn/credentials"
	// turnBudget covers `orama namespace enable webrtc` provisioning TURN.
	turnBudget   = 5 * time.Minute
	turnPoll     = 5 * time.Second
	expirySlack  = 5 * time.Minute
	cleanupLimit = 2 * time.Minute
)

var (
	// turnTTLLine is a line that states how long a TURN credential lives.
	turnTTLLine = regexp.MustCompile(`(?i)(turn|hmac credential).*(ttl|expire|lifetime)|(ttl|expire).*turn`)
	shortTTL    = regexp.MustCompile(`(?i)10[- ]minute|\b600\s*s\b|\b600 seconds`)
	sfuContext  = regexp.MustCompile(`(?i)sfu|signal`)
	turnTLS443  = regexp.MustCompile(`(?i)turn.*(tls|turns).*443/udp`)
	// fixedBug marks a line that tells the history of a fixed bug, which may
	// name the old wrong value.
	fixedBug = regexp.MustCompile(`(?i)bugboard #\d+`)
)

// TestTURNCredentialTTL_docsAgreeWithCode: REST and host-function TURN
// credentials last 24h (core/pkg/turn DefaultCredentialTTL) and only the SFU
// signalling path uses the per-namespace TTL, 600s by default
// (core/pkg/namespace DefaultTURNCredentialTTL). A doc line that gives TURN
// credentials a 10-minute TTL without saying it is the SFU path is wrong
// (bugboard 2855: ARCHITECTURE's "10-minute TTL" vs WEBRTC's 24h and 600s).
func TestTURNCredentialTTL_docsAgreeWithCode(t *testing.T) {
	t.Parallel()
	if v := codeConst(t, turnServerSrc, "const DefaultCredentialTTL"); v != "24 * time.Hour" {
		t.Fatalf("%s DefaultCredentialTTL is %s; the docs and this check say 24h", turnServerSrc, v)
	}
	if v := codeConst(t, namespaceTypes, "DefaultTURNCredentialTTL"); v != strconv.Itoa(sfuTTLSeconds) {
		t.Fatalf("%s DefaultTURNCredentialTTL is %s; the docs and this check say %d", namespaceTypes, v, sfuTTLSeconds)
	}
	for _, doc := range docs(t) {
		for _, l := range grep(t, doc, turnTTLLine) {
			if shortTTL.MatchString(l.Text) && !sfuContext.MatchString(l.Text) && !fixedBug.MatchString(l.Text) {
				t.Errorf("%s\n  gives TURN credentials a 10-minute TTL; REST/host-fn credentials last 24h, only SFU-signalled ones %ds", l, sfuTTLSeconds)
			}
		}
	}
	webrtc := strings.Join(texts(lines(t, webrtcDoc)), "\n")
	for _, want := range []string{"24h", "600s"} {
		if !strings.Contains(webrtc, want) {
			t.Errorf("%s no longer states the %s TURN credential lifetime", webrtcDoc, want)
		}
	}
}

// TestTURNTLSPort_docsAgreeWithCode: TURN over TLS is served on 5349/tcp and,
// with stealth TURN, on 443/tcp through the SNI router
// (core/pkg/gateway/handlers/webrtc/credentials.go: turns:<host>:5349 and
// turns:<cdn>:443). No doc may say TURN TLS is 443/udp, and the WebRTC page's
// firewall table must list 443 for stealth TURN (bugboard 2855).
func TestTURNTLSPort_docsAgreeWithCode(t *testing.T) {
	t.Parallel()
	for _, doc := range docs(t) {
		for _, l := range grep(t, doc, turnTLS443) {
			if denial.MatchString(l.Text) {
				continue // a rule being removed, not a claim of today's port
			}
			t.Errorf("%s\n  TURN over TLS is 5349/tcp and 443/tcp (stealth SNI), never 443/udp", l)
		}
	}
	firewall := section(lines(t, webrtcDoc), "## Firewall")
	if !regexp.MustCompile(`\|\s*443\s*\|`).MatchString(firewall) {
		t.Errorf("%s \"## Firewall\" omits 443/tcp, which stealth TURN serves TURNS on (website/src/docs/operator/stealth-turn.mdx)", webrtcDoc)
	}
}

// texts is the text of each line.
func texts(ls []line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Text
	}
	return out
}

// section returns the text from heading to the next heading of that level.
func section(ls []line, heading string) string {
	level := strings.Fields(heading)[0] + " "
	var b strings.Builder
	in := false
	for _, l := range ls {
		switch {
		case strings.TrimSpace(l.Text) == heading:
			in = true
		case in && strings.HasPrefix(l.Text, level):
			return b.String()
		case in:
			b.WriteString(l.Text + "\n")
		}
	}
	return b.String()
}

// TestTURNCredentialTTL_liveIs24h: the credentials a namespace member gets
// from the gateway last 24h, the username carries that expiry, and TURNS is
// offered on 5349 (never a 443/udp TURN TLS URI).
func TestTURNCredentialTTL_liveIs24h(t *testing.T) {
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupLimit)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: disabling WebRTC on %s: %v %s", n.Name, err, res.Stderr)
		}
	})
	member := tenancy.OperatorMember(t, f, n, "admin")
	c := member.Client.WithBase(gw.NamespaceURL(f.State, n.Name))
	var cred struct {
		Username string   `json:"username"`
		TTL      int      `json:"ttl"`
		URIs     []string `json:"uris"`
	}
	eventually.Require(t, turnPoll, turnBudget, "TURN credentials", func() (bool, error) {
		resp := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: turnCredPath, Bearer: member.Token()})
		if resp.Status != http.StatusOK {
			return false, errStatus(resp)
		}
		return true, resp.Decode(&cred)
	})
	if cred.TTL != restTTLSeconds {
		t.Errorf("TURN credential ttl %d, want %d (24h)", cred.TTL, restTTLSeconds)
	}
	expiry, err := strconv.ParseInt(strings.SplitN(cred.Username, ":", 2)[0], 10, 64)
	if err != nil {
		t.Fatalf("TURN username %q does not start with its expiry", cred.Username)
	}
	if d := time.Until(time.Unix(expiry, 0)); d < restTTLSeconds*time.Second-expirySlack || d > restTTLSeconds*time.Second+expirySlack {
		t.Errorf("TURN credential expires in %s, want about 24h", d)
	}
	for _, u := range cred.URIs {
		if strings.Contains(u, ":443?transport=udp") {
			t.Errorf("TURN URI %s offers 443/udp", u)
		}
	}
}
