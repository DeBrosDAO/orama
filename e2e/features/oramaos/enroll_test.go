//go:build e2e_fleet

package oramaos

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/enroll"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

const (
	pathComplete = "/v1/agent/enroll/complete"
	// codeHex is the code's length: 10 bytes, 80 bits (enroll/server.go codeBytes).
	codeHex     = 20
	tokenHex    = 64
	httpBudget  = 15 * time.Second
	sshWait     = 10 * time.Second
	closeBudget = 2 * time.Minute
)

// loginPrompt is a getty or shell login prompt on the console.
var loginPrompt = regexp.MustCompile(`(?m)login:\s*$`)

// TestOramaOS_firstBootEnrollmentContract boots the image once and walks
// its first boot: the code on the console, no SSH and no login prompt, the
// code never served, a payload that does not decrypt refused, completion
// accepted exactly once, and the enrollment port closed after it
// (docs/ORAMAOS_DEPLOYMENT.md "Enrollment Flow"; docs/SECURITY.md).
func TestOramaOS_firstBootEnrollmentContract(t *testing.T) {
	t.Parallel()
	v := bootVM(t)
	code := v.waitConsole(t, codeLine, "the registration code on the console")[1]
	if len(code) != codeHex {
		t.Errorf("the registration code is %d hex characters, want %d (80 bits)", len(code), codeHex)
	}
	t.Run("no ssh and no login shell", func(t *testing.T) {
		if loginPrompt.MatchString(v.Console()) {
			t.Error("the console offers a login prompt")
		}
		if banner := sshBanner(v.sshd); strings.HasPrefix(banner, "SSH-") {
			t.Errorf("the guest answers SSH: %q", banner)
		}
	})
	t.Run("code is never served", func(t *testing.T) {
		for _, p := range []string{"/", pathComplete, "/v1/agent/enroll/code"} {
			status, body, err := call(t, v, http.MethodGet, p, nil)
			if err != nil || strings.Contains(body, code) || status == http.StatusOK {
				t.Errorf("GET %s: HTTP %d %v, carries the code: %t", p, status, err, strings.Contains(body, code))
			}
		}
	})
	t.Run("payload must decrypt", func(t *testing.T) {
		wrong := strings.Repeat("0", codeHex)
		for name, body := range map[string][]byte{"garbage": []byte("not sealed"), "wrong code": seal(t, wrong, payload(t))} {
			if status, _, err := call(t, v, http.MethodPost, pathComplete, body); err != nil || status != http.StatusBadRequest {
				t.Errorf("%s: HTTP %d %v, want 400", name, status, err)
			}
		}
	})
	t.Run("completes exactly once", func(t *testing.T) { completeOnce(t, v, code) })
	eventually.Require(t, 2*time.Second, closeBudget, "the enrollment port to close", func() (bool, error) {
		_, _, err := call(t, v, http.MethodGet, "/", nil)
		return err != nil, nil
	})
	// The command receiver binds the overlay address only
	// (command/receiver.go Listen): nothing answers on the public interface.
	if status, _, err := sendTo(t, v.command, http.MethodGet, "/v1/agent/status", nil); err == nil {
		t.Errorf("the command receiver answers on the public interface: HTTP %d", status)
	}
}

// completeOnce proves completion is accepted once. The first request sends
// its headers and half its body and stalls, so it is in flight on the guest;
// a second, whole request is then accepted with a response sealed under the
// code carrying the node's own agent token; when the first finishes its
// body, it decrypts too and is refused 409 (docs/SECURITY.md "A second POST
// to /complete is 409").
func completeOnce(t *testing.T, v *vm, code string) {
	t.Helper()
	body := seal(t, code, payload(t))
	pr, pw := io.Pipe()
	first := make(chan int, 1)
	go func() {
		status, _, _ := callBody(t, v, pathComplete, pr)
		first <- status
	}()
	half := len(body) / 2
	if _, err := pw.Write(body[:half]); err != nil {
		t.Fatalf("starting the stalled completion: %v", err)
	}
	status, won, err := call(t, v, http.MethodPost, pathComplete, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("the completion answered HTTP %d %v, want 200", status, err)
	}
	if _, err := pw.Write(body[half:]); err != nil {
		t.Fatalf("finishing the stalled completion: %v", err)
	}
	_ = pw.Close()
	if second := <-first; second != http.StatusConflict {
		t.Errorf("a second valid completion answered HTTP %d, want 409", second)
	}
	plain, err := enroll.Open(code, strings.TrimSpace(won))
	if err != nil {
		t.Fatalf("the completion response is not sealed under the code: %v", err)
	}
	var resp struct {
		Status     string `json:"status"`
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(plain, &resp); err != nil || resp.Status != "ok" || len(resp.AgentToken) != tokenHex {
		t.Errorf("the completion response is %+v (%v), want ok with a %d-hex agent token", resp.Status, err, tokenHex)
	}
}

// payload is a cluster configuration as the gateway sends it
// (enroll/server.go Result), with throwaway values.
func payload(t *testing.T) []byte {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"node_id": "node-10.0.0.250", "wireguard_config": "[Interface]\nAddress = 10.0.0.250/24\n",
		"cluster_secret": hex.EncodeToString(secret), "peers": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func seal(t *testing.T, code string, plain []byte) []byte {
	t.Helper()
	s, err := enroll.Seal(code, plain)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(s)
}

// call sends a request with body to the guest's enrollment port.
func call(t *testing.T, v *vm, method, path string, body []byte) (int, string, error) {
	t.Helper()
	return send(t, v, method, path, bytes.NewReader(body))
}

// callBody POSTs whatever r yields, for a request that stalls mid-body.
func callBody(t *testing.T, v *vm, path string, r io.Reader) (int, string, error) {
	return send(t, v, http.MethodPost, path, r)
}

// send makes one request to the guest's enrollment port and records it
// (never the body: a sealed payload is the cluster secret under the code).
func send(t *testing.T, v *vm, method, path string, body io.Reader) (int, string, error) {
	return sendTo(t, v.enroll, method, path, body)
}

// sendTo makes one request to a forwarded guest port and records it.
func sendTo(t *testing.T, port int, method, path string, body io.Reader) (int, string, error) {
	req, err := http.NewRequestWithContext(t.Context(), method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), body)
	if err != nil {
		return 0, "", err
	}
	start := time.Now()
	resp, err := (&http.Client{Timeout: httpBudget}).Do(req)
	status, text := 0, ""
	if err == nil {
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		status, text, err = resp.StatusCode, string(raw), rerr
	}
	rec := evidence.Record{Kind: evidence.KindHTTP, Test: t.Name(), Summary: method + " guest:" + path, Status: status, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		rec.Error = err.Error()
	}
	_ = harness.Fleet(t).Recorder().Add(rec)
	return status, text, err
}

// sshBanner is what the guest's forwarded port 22 says first, if anything.
func sshBanner(port int) string {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), sshWait)
	if err != nil {
		return ""
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(sshWait))
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	return string(buf[:n])
}
