//go:build e2e_fleet

// Package services holds what the G5 service feature packages (storage,
// serverless, webrtc, push, anon-tor, vault and their chaos packages) share:
// node-side probes of loopback services that need a credential derived from
// the cluster secret, derived ON the node so the secret never reaches the
// runner or the evidence, and small request helpers.
package services

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Loopback services of the index plane (core/pkg/constants/ports.go,
// docs/SECURITY.md#network-isolation, docs/ARCHITECTURE.md#node-process-model).
const (
	KuboAPIPort        = 10107
	ClusterRESTPort    = 10108
	ClusterKuboProxy   = 10110
	ClusterSecretPath  = "/opt/orama/.orama/secrets/cluster-secret"
	IPFSRepoPath       = "/opt/orama/.orama/data/ipfs/repo"
	KuboTokenPurpose   = "ipfs-kubo-api"         // core/pkg/ipfs/kubo_auth.go
	ClusterRESTPurpose = "ipfs-cluster-rest-api" // core/pkg/ipfs/cluster_auth.go
	ClusterRESTUser    = "orama"
	// statusMarker separates a probe's body from the HTTP status curl prints.
	statusMarker = "\n__E2E_HTTP_STATUS__="
)

// deriveScript prints hex(HKDF-SHA256(cluster secret, no salt, info=purpose))
// (core/pkg/secrets DeriveKey), computed on the node.
func deriveScript(purpose string) string {
	return `python3 -c 'import hmac,hashlib;` +
		`k=open("` + ClusterSecretPath + `").read().strip().encode();` +
		`p=hmac.new(b"\0"*32,k,hashlib.sha256).digest();` +
		`print(hmac.new(p,b"` + purpose + `\x01",hashlib.sha256).hexdigest())'`
}

// Probe is one loopback HTTP answer seen from a node shell.
type Probe struct {
	Status int
	Body   string
	Exit   int
}

// parseProbe splits curl's output into body and status.
func parseProbe(out fleet.Output) Probe {
	p := Probe{Exit: out.Exit, Body: out.Stdout}
	i := strings.LastIndex(out.Stdout, statusMarker)
	if i < 0 {
		return p
	}
	p.Body = out.Stdout[:i]
	p.Status, _ = strconv.Atoi(strings.TrimSpace(out.Stdout[i+len(statusMarker):]))
	return p
}

// curlCmd is a curl of url with method, printing the body then the status.
// stdinOpt, when set, is a curl option that reads from stdin, and feed the
// shell (a builtin printf) that writes the credential into it: a credential
// never appears on a command line, where any local user reads it from
// /proc/<pid>/cmdline.
func curlCmd(method, url, feed, stdinOpt, prefix string) string {
	pipe, opt := "", ""
	if stdinOpt != "" {
		pipe, opt = feed+" | ", " "+stdinOpt
	}
	return prefix + pipe + `curl -sS --max-time 20 -X ` + method + opt + ` -w '` +
		strings.ReplaceAll(statusMarker, "\n", `\n`) + `%{http_code}' '` + url + `'`
}

// Kubo calls the Kubo RPC on node n; with auth the bearer derived on the node,
// given to curl as a header read from stdin.
func Kubo(t testing.TB, f *fleet.Fleet, n fleet.Node, pathQuery string, auth bool) Probe {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", KuboAPIPort, pathQuery)
	if !auth {
		return parseProbe(f.Exec(t, n, curlCmd("POST", url, "", "", "")))
	}
	prefix := "TOK=$(" + deriveScript(KuboTokenPurpose) + ") && "
	return parseProbe(f.Exec(t, n, curlCmd("POST", url, `printf 'Authorization: Bearer %s\n' "$TOK"`, "-H @-", prefix)))
}

// ClusterREST calls IPFS Cluster's REST API on node n; with auth the basic
// credentials derived on the node, given to curl as a config read from stdin.
func ClusterREST(t testing.TB, f *fleet.Fleet, n fleet.Node, path string, auth bool) Probe {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", ClusterRESTPort, path)
	if !auth {
		return parseProbe(f.Exec(t, n, curlCmd("GET", url, "", "", "")))
	}
	prefix := "PW=$(" + deriveScript(ClusterRESTPurpose) + ") && "
	feed := `printf 'user = "` + ClusterRESTUser + `:%s"\n' "$PW"`
	return parseProbe(f.Exec(t, n, curlCmd("GET", url, feed, "-K -", prefix)))
}

// ClusterKuboProxyAs calls the serve-ipfs-cluster proxy as a local user:
// it admits only sockets the orama user owns (docs/ARCHITECTURE.md).
func ClusterKuboProxyAs(t testing.TB, f *fleet.Fleet, n fleet.Node, user string) Probe {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v0/version", ClusterKuboProxy)
	prefix := ""
	if user != "" {
		prefix = "sudo -n -u " + fleet.ShellQuote(user) + " "
	}
	return parseProbe(f.Exec(t, n, curlCmd("POST", url, "", "", prefix)))
}
