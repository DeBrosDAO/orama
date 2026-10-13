package clientkey

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeProof(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proof")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProver_Verify(t *testing.T) {
	p := NewProver(writeProof(t, testProof+"\n"), nil)
	if !p.Ready() {
		t.Fatal("a prover with a good file is not ready")
	}
	cases := map[string]struct {
		got  string
		want bool
	}{
		"the secret":            {testProof, true},
		"empty":                 {"", false},
		"a wrong value":         {strings.Repeat("a", len(testProof)), false},
		"a prefix of the value": {testProof[:len(testProof)-1], false},
		"the value and more":    {testProof + "0", false},
		"the value, spaced":     {" " + testProof, false},
	}
	for name, tc := range cases {
		if got := p.Verify(tc.got); got != tc.want {
			t.Errorf("%s: Verify = %v, want %v", name, got, tc.want)
		}
	}
}

func TestProver_noFileProvesNothing(t *testing.T) {
	var reported []error
	p := NewProver(filepath.Join(t.TempDir(), "absent"), func(err error) { reported = append(reported, err) })
	if p.Ready() || p.Verify(testProof) || p.Verify("") {
		t.Error("a prover with no file proved a request")
	}
	if len(reported) != 1 {
		t.Fatalf("reported %d errors, want 1", len(reported))
	}
	if !errors.Is(reported[0], os.ErrNotExist) {
		t.Errorf("the error does not wrap the cause: %v", reported[0])
	}
}

func TestProver_shortFileIsNotASecret(t *testing.T) {
	var reported []error
	p := NewProver(writeProof(t, "abc\n"), func(err error) { reported = append(reported, err) })
	if p.Ready() || p.Verify("abc") {
		t.Error("a file shorter than the minimum became a secret")
	}
	if len(reported) != 1 {
		t.Fatalf("reported %d errors, want 1", len(reported))
	}
	if strings.Contains(reported[0].Error(), "abc") {
		t.Errorf("the error carries the file's content: %v", reported[0])
	}
}

func TestProver_nilProvesNothing(t *testing.T) {
	var p *Prover
	if p.Ready() || p.Verify(testProof) {
		t.Error("a nil prover proved a request")
	}
}

// An upgrade makes a new secret while the gateway runs. Caddy sends the new one at once; the
// gateway reads the file again when a header does not match, but not more than once per interval.
func TestProver_followsARotation(t *testing.T) {
	path := writeProof(t, testProof)
	now := time.Unix(1000, 0)
	p := NewProver(path, nil)
	p.now = func() time.Time { return now }
	p.lastReload = now

	rotated := strings.Repeat("f", len(testProof))
	if err := os.WriteFile(path, []byte(rotated), 0o600); err != nil {
		t.Fatal(err)
	}
	if p.Verify(rotated) {
		t.Error("the file was read again inside the reload interval")
	}
	now = now.Add(proofReloadInterval)
	if !p.Verify(rotated) {
		t.Error("the rotated secret is not accepted after the reload interval")
	}
	if p.Verify(testProof) {
		t.Error("the old secret is still accepted after the rotation")
	}
}

func TestProver_wrongValuesDoNotReadTheDiskPerRequest(t *testing.T) {
	path := writeProof(t, testProof)
	reads := 0
	p := NewProver(path, func(error) { reads++ })
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	p.lastReload = now
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		p.Verify("wrong")
	}
	if reads != 0 {
		t.Errorf("the file was read %d times inside one interval", reads)
	}
	now = now.Add(proofReloadInterval)
	p.Verify("wrong")
	p.Verify("wrong")
	if reads != 1 {
		t.Errorf("the file was read %d times in the next interval, want 1", reads)
	}
}

func TestMiddleware(t *testing.T) {
	p := NewProver(writeProof(t, testProof), nil)
	run := func(header string, set bool, peer, xff string) (headerSeen string, client string) {
		r := req(peer, xff, "")
		if set {
			r.Header.Set(ProofHeader, header)
		}
		p.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
			headerSeen = got.Header.Get(ProofHeader)
			client = Public(got)
		})).ServeHTTP(httptest.NewRecorder(), r)
		return headerSeen, client
	}

	t.Run("the secret proves the request and is deleted", func(t *testing.T) {
		seen, client := run(testProof, true, "127.0.0.1:1", "6.6.6.6, 203.0.113.9")
		if seen != "" {
			t.Errorf("the handler saw the header %q", seen)
		}
		if client != "203.0.113.9" {
			t.Errorf("client = %q, want the last entry", client)
		}
	})
	t.Run("a forged value is deleted and proves nothing", func(t *testing.T) {
		seen, client := run("forged", true, "127.0.0.1:1", "203.0.113.9")
		if seen != "" || client != "127.0.0.1" {
			t.Errorf("seen=%q client=%q, want nothing and the peer", seen, client)
		}
	})
	t.Run("no header, no proof", func(t *testing.T) {
		_, client := run("", false, "127.0.0.1:1", "203.0.113.9")
		if client != "127.0.0.1" {
			t.Errorf("client = %q, want the peer", client)
		}
	})
	t.Run("an empty header proves nothing", func(t *testing.T) {
		_, client := run("", true, "127.0.0.1:1", "203.0.113.9")
		if client != "127.0.0.1" {
			t.Errorf("client = %q, want the peer", client)
		}
	})
}

// A gateway that could not load a secret still deletes the header: it must not reach a proxied
// service, and a nil prover is what the gateway's unit tests build.
func TestMiddleware_nilProverStillDeletesTheHeader(t *testing.T) {
	var p *Prover
	r := req("127.0.0.1:1", "203.0.113.9", "")
	r.Header.Set(ProofHeader, testProof)
	var seen string
	p.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		seen = got.Header.Get(ProofHeader)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if seen != "" {
		t.Errorf("the handler saw the header %q", seen)
	}
}

// A reload that cannot read the file for a passing reason keeps the secret it had; a file that is
// gone or emptied proves nothing any more.
func TestProver_aFailedReadKeepsTheLastSecretAndARemovedFileDropsIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	path := writeProof(t, testProof)
	now := time.Unix(1000, 0)
	p := NewProver(path, nil)
	p.now = func() time.Time { return now }
	p.lastReload = now

	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(proofReloadInterval)
	p.Verify("a wrong value, which makes the prover read the file again")
	if !p.Verify(testProof) {
		t.Fatal("an unreadable file dropped the secret the prover held")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(proofReloadInterval)
	p.Verify("a wrong value, which makes the prover read the file again")
	if p.Verify(testProof) {
		t.Fatal("a removed file still proves")
	}
}
