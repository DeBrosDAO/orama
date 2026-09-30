//go:build e2e_fleet

package tenancy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// probeMain is a Go backend that reports, from inside its own sandbox, what
// it can see and do. Every probe answers JSON {"ok": bool, "detail": "..."}
// and never returns a secret: /read reports whether a file could be read, not
// its bytes. VERSION is replaced per build so updates and rollbacks are
// observable.
const probeMain = `package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const version = "VERSION"

// caPEM is the run's CA bundle (Let's Encrypt staging roots): nodes do not
// trust them, only the harness and the CLI do, so the app's own gateway calls
// carry the bundle the way a real app deployed against a staging cluster would.
//
//go:embed ca.pem
var caPEM []byte

func gatewayClient() *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(caPEM)
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

type answer struct {
	OK     bool   ` + "`json:\"ok\"`" + `
	Detail string ` + "`json:\"detail\"`" + `
}

func reply(w http.ResponseWriter, ok bool, detail string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer{OK: ok, Detail: detail})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func uid() string {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return "unreadable: " + err.Error()
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if strings.HasPrefix(s.Text(), "Uid:") {
			return strings.Join(strings.Fields(strings.TrimPrefix(s.Text(), "Uid:")), " ")
		}
	}
	return "no Uid line"
}

func main() {
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { reply(w, true, "healthy") })
	http.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) { reply(w, true, version) })
	http.HandleFunc("/uid", func(w http.ResponseWriter, _ *http.Request) { reply(w, true, uid()) })
	http.HandleFunc("/getenv", func(w http.ResponseWriter, r *http.Request) {
		v, ok := os.LookupEnv(r.URL.Query().Get("k"))
		reply(w, ok, v)
	})
	http.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		_, err := os.ReadFile(r.URL.Query().Get("p"))
		reply(w, err == nil, errText(err))
	})
	http.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(r.URL.Query().Get("p"))
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		reply(w, err == nil, strings.Join(names, ",")+errText(err))
	})
	http.HandleFunc("/write", func(w http.ResponseWriter, r *http.Request) {
		err := os.WriteFile(r.URL.Query().Get("p"), []byte("probe"), 0o600)
		reply(w, err == nil, errText(err))
	})
	http.HandleFunc("/bind", func(w http.ResponseWriter, r *http.Request) {
		l, err := net.Listen("tcp", r.URL.Query().Get("addr"))
		if err == nil {
			l.Close()
		}
		reply(w, err == nil, errText(err))
	})
	http.HandleFunc("/dial", func(w http.ResponseWriter, r *http.Request) {
		c, err := net.DialTimeout("tcp", r.URL.Query().Get("addr"), 3*time.Second)
		if err == nil {
			c.Close()
		}
		reply(w, err == nil, errText(err))
	})
	http.HandleFunc("/renew", func(w http.ResponseWriter, _ *http.Request) {
		tok, err := os.ReadFile(os.Getenv("ORAMA_TOKEN_FILE"))
		if err != nil {
			reply(w, false, "token file: "+err.Error())
			return
		}
		req, _ := http.NewRequest(http.MethodPost, os.Getenv("ORAMA_GATEWAY_URL")+"/v1/auth/renew", nil)
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
		resp, err := gatewayClient().Do(req)
		if err != nil {
			reply(w, false, "renew: "+err.Error())
			return
		}
		resp.Body.Close()
		reply(w, resp.StatusCode == http.StatusOK, resp.Status)
	})
	http.HandleFunc("/crash", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, true, "exiting")
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(3) }()
	})
	http.HandleFunc("/oom", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, true, "allocating")
		go func() {
			var hold [][]byte
			for {
				b := make([]byte, 64<<20)
				for i := range b {
					b[i] = 1
				}
				hold = append(hold, b)
			}
		}()
	})
	_ = http.ListenAndServe(":"+os.Getenv("PORT"), nil)
}
`

// WriteProbeApp writes the probe backend, stamped with version, and the run's
// CA bundle (ca.pem, embedded) into a fresh directory and returns it. `orama
// deploy go` builds it on the runner. The deployments packages share it.
func WriteProbeApp(t testing.TB, version string) string {
	t.Helper()
	ca, err := os.ReadFile(harness.Fleet(t).State.CAFile)
	if err != nil {
		t.Fatalf("failed to read the run's CA bundle for the probe app: %v", err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module probe\n\ngo 1.22\n",
		"main.go": strings.Replace(probeMain, `"VERSION"`, `"`+version+`"`, 1),
		"ca.pem":  string(ca),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("failed to write the probe app's %s: %v", name, err)
		}
	}
	return dir
}
