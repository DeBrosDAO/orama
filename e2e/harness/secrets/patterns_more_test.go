package secrets

import (
	"strings"
	"testing"
)

// moreShapes are the shapes the first pattern set missed.
var moreShapes = []struct {
	name, in, secret, keep string
}{
	{"cluster secret mid-line", "node-1: cluster secret: cs0123456789abcdef in use", "cs0123456789abcdef", " in use"},
	{"cluster_secret mid-line", "loaded cluster_secret: cs9876543210fedcba", "cs9876543210fedcba", "cluster_secret: "},
	{"rqlite password mid-line", "auth rqlite password=rq-pw-12345678, ok", "rq-pw-12345678", ", ok"},
	{"x auth token header", "X-Auth-Token: xat-1234567890", "xat-1234567890", "X-Auth-Token: "},
	{"x orama key header", "x-orama-key: xok-1234567890", "xok-1234567890", "x-orama-key: "},
	{"x custom auth header", "X-Upstream-Auth: xua-1234567890", "xua-1234567890", "X-Upstream-Auth: "},
	{"password flag with space", "running rqlite --password hunter2hunter2 --port 5001", "hunter2hunter2", "--port 5001"},
	{"token flag with space", "orama login -token tk-abcdefgh", "tk-abcdefgh", "-token "},
	{"curl user", "curl -s -u admin:s3cr3t-pw http://10.0.0.1:5001", "s3cr3t-pw", "-u admin:"},
	{"curl long user", "curl --user admin:another-pw http://x", "another-pw", "--user admin:"},
	{"passphrase", `{"passphrase":"pp-12345678"}`, "pp-12345678", `"passphrase":"`},
	{"encryption_key", "ENCRYPTION_KEY=ek-12345678", "ek-12345678", "ENCRYPTION_KEY="},
	{"privkey", `{"privkey":"pk2-12345678"}`, "pk2-12345678", `"privkey":"`},
	{"signing_key", "signing_key: sgk-12345678", "sgk-12345678", "signing_key: "},
	{"hmac_key", `{"hmacKey":"hk-12345678"}`, "hk-12345678", `"hmacKey":"`},
	{"seed", "SEED=seed-12345678", "seed-12345678", "SEED="},
	{"private key as bytes", `{"private_key":[12,34,56,78,90],"n":1}`, "12,34,56,78,90", `"n":1`},
	{"numeric token", `{"token":1234567890123,"n":1}`, "1234567890123", `"n":1`},
	{"evm private key", "key 0x" + strings.Repeat("a1", 32) + " loaded", strings.Repeat("a1", 32), " loaded"},
	{"mnemonic line", "your mnemonic: abandon ability able about above absent absorb abstract absurd abuse access accident", "absorb abstract", "your mnemonic: "},
	{"seed phrase next line", "Seed phrase:\n  zoo zone youth young yellow year wrong write wrist world worth worry", "wrist world", "Seed phrase:"},
	{"double escaped json", `{\\\"access_token\\\":\\\"dbl-12345678\\\"}`, "dbl-12345678", `access_token`},
	{"key query param", "GET /v1/fn?key=qk-12345678&x=1", "qk-12345678", "&x=1"},
	{"sig query param", "GET /d?a=1&sig=sg-12345678", "sg-12345678", "?a=1&sig="},
}

func TestRedact_moreShapes(t *testing.T) {
	for _, c := range moreShapes {
		t.Run(c.name, func(t *testing.T) {
			var r *Redactor
			out := r.Redact(c.in)
			if strings.Contains(out, c.secret) {
				t.Errorf("secret %q survived:\n%s", c.secret, out)
			}
			if !strings.Contains(out, c.keep) {
				t.Errorf("context %q lost:\n%s", c.keep, out)
			}
			if again := r.Redact(out); again != out {
				t.Errorf("not idempotent:\n%s\n%s", out, again)
			}
		})
	}
}

// TestRedact_noOverRedaction: identifiers that look like secret members
// but are not stay as they are.
func TestRedact_noOverRedaction(t *testing.T) {
	var r *Redactor
	for _, in := range []string{
		`{"id":"ns-12345678","node_id":"node-abcdefgh","cid":"bafy12345678","created":"2026-09-29T10:00:00Z"}`,
		`{\"id\":\"ns-12345678\",\"node_id\":\"n-1\"}`,
		"tx 0x" + strings.Repeat("b", 63) + " short",
		"ok: the test passed in 12 steps with no error at all whatsoever today",
		"    token_test.go:12: want 401, got 200",
		"-u admin",
	} {
		if got := r.Redact(in); got != in {
			t.Errorf("over-redacted:\n%s\n%s", in, got)
		}
	}
}
