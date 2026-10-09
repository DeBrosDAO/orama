//go:build e2e_fleet

package edge

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// ACME DNS-01 endpoints (docs/API_SURFACE.md "Internal"; core/pkg/gateway/acme_auth.go).
const (
	ACMEPresent = "/v1/internal/acme/present"
	ACMECleanup = "/v1/internal/acme/cleanup"
	// MACHeader carries "<unix ts>.<hex hmac>" (core/pkg/auth/coordination.go).
	MACHeader = "X-Orama-Coordination-MAC"
)

// Key choices for a signed ACME call.
const (
	KeyReal   = "real"   // the node's own /etc/caddy/orama-acme.key
	KeyRandom = "random" // a fresh 32-byte key no node holds
	KeyNone   = "none"   // no MAC header at all
)

// ACMECall is one present or cleanup request made from a node shell, signed
// there with the key Caddy uses, so the key never leaves the node.
type ACMECall struct {
	Path string
	// Body is what is sent; SignedBody (when set) is what the MAC covers, to
	// replay a stamp onto another record.
	Body, SignedBody []byte
	Key              string
	// SkewSec moves the stamp's timestamp, to step outside the ±60s window.
	SkewSec int
	// URL overrides the target (default: this node's loopback gateway).
	URL string
	// Unnonced signs only the MAC without the nonce, as a Caddy built before
	// the nonce does (auth.AcceptLegacyACMEMAC); by default the call carries
	// both stamps, as auth.SignACME and the current Caddy module write them.
	Unnonced bool
	// Replay sends the same signed request a second time and returns that
	// answer; the first must be 200 or the call cannot be made.
	Replay bool
}

// acmeScript signs and sends: argv is url, path, key mode, skew, body,
// signed body, stamps (both|unnonced) and replay (once|replay). It prints
// "STATUS <code>" then the response body, of the last send. The payloads are
// auth.acmePayloadNonced ("orama-acme-v2", method, path, query, the SHA-256 of
// the body, the nonce, the timestamp, joined by newlines) in
// X-Orama-ACME-MAC-V2 with the nonce in X-Orama-ACME-Nonce, and
// auth.acmePayload (the same without the nonce, labelled
// "orama-coordination-v2") in X-Orama-Coordination-MAC.
const acmeScript = `import hashlib,hmac,os,sys,time,urllib.request,urllib.error
url,path,mode,skew,body,signed,stamps,replay=sys.argv[1:9]
h={"Content-Type":"application/json"}
if mode!="none":
    key=bytes.fromhex(open("` + ACMEKeyPath + `").read().strip()) if mode=="real" else os.urandom(32)
    ts=str(int(time.time())+int(skew))
    digest=hashlib.sha256(signed.encode()).hexdigest()
    p="\n".join(["orama-coordination-v2","POST",path,"",digest,ts])
    h["` + MACHeader + `"]=ts+"."+hmac.new(key,p.encode(),hashlib.sha256).hexdigest()
    if stamps=="both":
        nonce=os.urandom(16).hex()
        p="\n".join(["orama-acme-v2","POST",path,"",digest,nonce,ts])
        h["X-Orama-ACME-Nonce"]=nonce
        h["X-Orama-ACME-MAC-V2"]=ts+"."+hmac.new(key,p.encode(),hashlib.sha256).hexdigest()
for i in range(2 if replay=="replay" else 1):
    req=urllib.request.Request(url+path,data=body.encode(),headers=h,method="POST")
    try:
        r=urllib.request.urlopen(req,timeout=20);code=r.status;out=r.read()
    except urllib.error.HTTPError as e:
        code=e.code;out=e.read()
    if replay=="replay" and i==0 and code!=200:
        print("first send answered",code,file=sys.stderr);sys.exit(3)
print("STATUS",code);sys.stdout.write(out.decode(errors="replace"))
`

// Command renders the call as a shell command for n's shell.
func (c ACMECall) Command() string {
	url := c.URL
	if url == "" {
		url = LocalGateway("")
	}
	signed := c.SignedBody
	if signed == nil {
		signed = c.Body
	}
	stamps, replay := "both", "once"
	if c.Unnonced {
		stamps = "unnonced"
	}
	if c.Replay {
		replay = "replay"
	}
	args := []string{url, c.Path, c.Key, strconv.Itoa(c.SkewSec), string(c.Body), string(signed), stamps, replay}
	cmd := "python3 -c " + fleet.ShellQuote(acmeScript)
	for _, a := range args {
		cmd += " " + fleet.ShellQuote(a)
	}
	return cmd
}

// Run makes the call on n and returns the gateway's status and body.
func (c ACMECall) Run(t testing.TB, f *fleet.Fleet, n fleet.Node) Probe {
	t.Helper()
	out := f.Exec(t, n, c.Command())
	head, body, _ := strings.Cut(out.Stdout, "\n")
	code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(head, "STATUS")))
	if out.Exit != 0 || err != nil {
		t.Fatalf("%s: the ACME call could not be made (exit %d): %s %s", n.Name, out.Exit, out.Stdout, f.Redact(out.Stderr))
	}
	return Probe{Status: code, Body: body, Exit: out.Exit, Stderr: out.Stderr}
}

// ACMEBody is a present/cleanup body.
func ACMEBody(t testing.TB, fqdn, value string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"fqdn": fqdn, "value": value})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ChallengeValue is a well-formed DNS-01 answer: 43 base64url characters
// (RFC 8555 8.4), random so two tests never share one.
func ChallengeValue(t testing.TB) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	if len(v) != 43 {
		t.Fatalf("challenge value %q is %d characters, want 43", v, len(v))
	}
	return v
}

// RandomLabel is a fresh DNS label no record uses.
func RandomLabel(t testing.TB, prefix string) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s%x", prefix, b)
}
