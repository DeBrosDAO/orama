package build

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// providerSignSource is the generated provider's sign function, with what it
// needs to compile on its own.
func providerSignSource(t *testing.T) string {
	t.Helper()
	code := generateCaddyProviderCode()
	start := strings.Index(code, "func sign(")
	if start < 0 {
		t.Fatal("the generated provider has no sign function")
	}
	end := strings.Index(code[start:], "\n}\n")
	if end < 0 {
		t.Fatal("the sign function does not end")
	}
	return code[start : start+end+len("\n}\n")]
}

// The gateway refuses a DNS-01 call whose MAC it cannot verify, and the
// provider cannot import the package that verifies it. So the provider's own
// sign function is compiled and run here, and its stamp is checked with the
// gateway's verifier: a drift in either shows up as a failed renewal in this
// test rather than on a node.
func TestCaddyProvider_signsWhatTheGatewayVerifies(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	prog := `package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

` + providerSignSource(t) + `
func main() {
	body := []byte("{\"fqdn\":\"_acme-challenge.example.com.\",\"value\":\"x\"}")
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:10104/v1/internal/acme/present", nil)
	sign([]byte("provider-test-key"), req, body, time.Now())
	fmt.Println(req.Header.Get("` + caddyProviderMACHeader + `"))
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(prog), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gobin, "run", "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run the provider's sign function: %v\n%s", err, out)
	}
	stamp := strings.TrimSpace(string(out))

	body := []byte("{\"fqdn\":\"_acme-challenge.example.com.\",\"value\":\"x\"}")
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:10104/v1/internal/acme/present", nil)
	req.Header.Set(auth.CoordinationMACHeader, stamp)
	if !auth.VerifyACME([]byte("provider-test-key"), req, body, time.Now()) {
		t.Fatalf("the gateway does not verify the provider's stamp %q", stamp)
	}
	if auth.VerifyACME([]byte("provider-test-key"), req, []byte("other"), time.Now()) {
		t.Error("a stamp verified over a different body")
	}
	req.URL.Path = "/v1/internal/acme/cleanup"
	if auth.VerifyACME([]byte("provider-test-key"), req, body, time.Now()) {
		t.Error("a stamp for present verified on cleanup")
	}
}

// The provider refuses to start without a key rather than making calls the
// gateway will refuse.
func TestCaddyProvider_requiresAKeyFile(t *testing.T) {
	code := generateCaddyProviderCode()
	for _, want := range []string{`case "key_file":`, `key_file is required`} {
		if !strings.Contains(code, want) {
			t.Errorf("generated provider lacks %q", want)
		}
	}
}
