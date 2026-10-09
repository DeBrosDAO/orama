//go:build e2e_fleet

package edge

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// TLSStorePath is the cluster's certificate store on the index gateway
// (core/pkg/gateway/tls_store_handler.go).
const TLSStorePath = "/v1/internal/tls-store"

// tlsStoreScript signs a store call the way Caddy's storage module does and
// sends it: argv is url, key mode (real|random|none), body, and "replay" to
// send the same signed request twice. The MAC key is HKDF-Expand of the
// node's store master key with "orama-tls-store-mac-v1"; the stamps are the
// coordination v2 payload and the v3 payload (the same with the port of the
// process the call is for after the audience) for the audience
// "caddy-tls-store" (core/pkg/tlsstore/keys.go,
// core/pkg/auth/coordination_v2.go, coordination_v3.go). argv[5] says which
// are sent: "both" (as Caddy's module does), "v3" (only the v3 stamp) or
// "v3-other-port" (only a v3 stamp scoped to the next port up, as one made
// for a sibling process on the node would be). It prints "STATUS <code>" then
// the body, once per send.
const tlsStoreScript = `import hashlib,hmac,os,sys,time,urllib.request,urllib.error
import urllib.parse
url,mode,body,replay,stamps=sys.argv[1:6]
h={"Content-Type":"application/json"}
if mode!="none":
    master=bytes.fromhex(open("` + TLSStoreKeyPath + `").read().strip()) if mode=="real" else os.urandom(32)
    key=hmac.new(master,b"orama-tls-store-mac-v1\x01",hashlib.sha256).digest()
    ts=str(int(time.time()));nonce=os.urandom(16).hex()
    p="\n".join(["orama-coordination-v2","POST","caddy-tls-store","` + TLSStorePath + `","",hashlib.sha256(body.encode()).hexdigest(),nonce,ts])
    h["X-Orama-Coordination-Nonce"]=nonce
    if stamps=="both":
        h["X-Orama-Coordination-MAC-V2"]=ts+"."+hmac.new(key,p.encode(),hashlib.sha256).hexdigest()
    port=urllib.parse.urlparse(url).port+(1 if stamps=="v3-other-port" else 0)
    p="\n".join(["orama-coordination-v3","POST","caddy-tls-store",str(port),"` + TLSStorePath + `","",hashlib.sha256(body.encode()).hexdigest(),nonce,ts])
    h["X-Orama-Coordination-MAC-V3"]=ts+"."+hmac.new(key,p.encode(),hashlib.sha256).hexdigest()
for _ in range(2 if replay=="replay" else 1):
    req=urllib.request.Request(url+"` + TLSStorePath + `",data=body.encode(),headers=h,method="POST")
    try:
        r=urllib.request.urlopen(req,timeout=20);code=r.status;out=r.read()
    except urllib.error.HTTPError as e:
        code=e.code;out=e.read()
    print("STATUS",code);print(out.decode(errors="replace").replace("\n"," "))
`

// TLSStoreCall is one store call made from a node shell, signed there so the
// key never leaves the node.
type TLSStoreCall struct {
	Body   map[string]any
	Key    string // KeyReal, KeyRandom or KeyNone
	Replay bool
	// Stamps is "both" (the default: v2 and v3, as Caddy's module sends them),
	// "v3" or "v3-other-port".
	Stamps string
}

// Run makes the call on n and returns one probe per send.
func (c TLSStoreCall) Run(t testing.TB, f *fleet.Fleet, n fleet.Node) []Probe {
	t.Helper()
	body, err := json.Marshal(c.Body)
	if err != nil {
		t.Fatal(err)
	}
	replay := "once"
	if c.Replay {
		replay = "replay"
	}
	stamps := c.Stamps
	if stamps == "" {
		stamps = "both"
	}
	cmd := "python3 -c " + fleet.ShellQuote(tlsStoreScript)
	for _, a := range []string{LocalGateway(""), c.Key, string(body), replay, stamps} {
		cmd += " " + fleet.ShellQuote(a)
	}
	out := f.Exec(t, n, cmd)
	if out.Exit != 0 {
		t.Fatalf("%s: the TLS store call could not be made (exit %d): %s %s", n.Name, out.Exit, out.Stdout, f.Redact(out.Stderr))
	}
	var probes []Probe
	lines := strings.Split(strings.TrimRight(out.Stdout, "\n"), "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[i], "STATUS")))
		if err != nil {
			t.Fatalf("%s: unreadable TLS store answer: %q", n.Name, out.Stdout)
		}
		probes = append(probes, Probe{Status: code, Body: lines[i+1]})
	}
	return probes
}
