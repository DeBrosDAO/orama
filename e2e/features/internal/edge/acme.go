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

// ACME DNS-01 endpoints (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Internal"; core/pkg/gateway/acme_auth.go).
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
}

// acmeScript signs and sends: argv is url, path, key mode, skew, body,
// signed body. It prints "STATUS <code>" then the response body. The payload
// is auth.acmePayload: "orama-coordination-v2", method, path, query, the
// SHA-256 of the body, the timestamp, joined by newlines.
const acmeScript = `import hashlib,hmac,os,sys,time,urllib.request,urllib.error
url,path,mode,skew,body,signed=sys.argv[1:7]
h={"Content-Type":"application/json"}
if mode!="none":
    key=bytes.fromhex(open("` + ACMEKeyPath + `").read().strip()) if mode=="real" else os.urandom(32)
    ts=str(int(time.time())+int(skew))
    p="\n".join(["orama-coordination-v2","POST",path,"",hashlib.sha256(signed.encode()).hexdigest(),ts])
    h["` + MACHeader + `"]=ts+"."+hmac.new(key,p.encode(),hashlib.sha256).hexdigest()
req=urllib.request.Request(url+path,data=body.encode(),headers=h,method="POST")
try:
    r=urllib.request.urlopen(req,timeout=20);code=r.status;out=r.read()
except urllib.error.HTTPError as e:
    code=e.code;out=e.read()
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
	args := []string{url, c.Path, c.Key, strconv.Itoa(c.SkewSec), string(c.Body), string(signed)}
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
