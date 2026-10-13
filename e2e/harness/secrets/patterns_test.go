package secrets

import (
	"strings"
	"testing"
)

// shapeCases name one secret per shape the patterns must recognise without
// knowing its value, and the context that must survive.
var shapeCases = []struct {
	name, in, secret, keep string
}{
	{"bearer header", "Authorization: Bearer abcdefghijkl", "abcdefghijkl", "Authorization: "},
	{"basic header", "Authorization: Basic dXNlcjpwYXNzd29yZA==", "dXNlcjpwYXNzd29yZA", "Authorization: "},
	{"token scheme", "authorization: Token tok-12345678", "tok-12345678", "authorization: "},
	{"basic scheme word", "Authorization: Basic dXNlcjpwYXNz", "Basic", "Authorization: "},
	{"printed header map", "map[Authorization:[Bearer sekret-value-1]]", "sekret-value-1", "Authorization:["},
	{"proxy auth", "Proxy-Authorization: Basic cHJveHk6cHc=", "cHJveHk6cHc", "Proxy-Authorization: "},
	{"api key header", "X-Api-Key: apikey-123456789", "apikey-123456789", "X-Api-Key: "},
	{"cookie", "Cookie: session=abcdef123456; other=1", "abcdef123456", "Cookie: "},
	{"set-cookie", "Set-Cookie: sid=zzzzzzzzzz; HttpOnly", "zzzzzzzzzz", "Set-Cookie: "},
	{"json snake", `{"refresh_token":"rt-99999999","n":1}`, "rt-99999999", `"n":1`},
	{"json camel", `{"accessToken":"at-88888888"}`, "at-88888888", `"accessToken":"`},
	{"json upper", `{"CLIENT_SECRET":"cs-77777777"}`, "cs-77777777", `"CLIENT_SECRET":"`},
	{"json passwd", `{"passwd":"pw-66666666"}`, "pw-66666666", `"passwd":"`},
	{"json private key", `{"privateKey":"pk-55555555"}`, "pk-55555555", `"privateKey":"`},
	{"json mnemonic", `{"Mnemonic":"word word word word"}`, "word word", `"Mnemonic":"`},
	{"json psk", `{"psk":"psk-44444444"}`, "psk-44444444", `"psk":"`},
	{"json swarm key", `{"swarmKey":"sk-33333333"}`, "sk-33333333", `"swarmKey":"`},
	{"json api key", `{"apiKey":"ak-22222222"}`, "ak-22222222", `"apiKey":"`},
	{"escaped json", `{"Output":"body {\"access_token\":\"esc-11111111\"}\n"}`, "esc-11111111", `\"access_token\":\"`},
	{"jwk d", `{"kty":"OKP","d":"private-d-value"}`, "private-d-value", `"kty":"OKP"`},
	{"env", "HCLOUD_TOKEN=env-00000000 next", "env-00000000", " next"},
	{"env quoted", `DB_PASSWORD="quoted secret"`, "quoted secret", "DB_PASSWORD="},
	{"query", "GET /x?api_key=q-12121212&y=1", "q-12121212", "&y=1"},
	{"flag", "orama login --token=flag-13131313", "flag-13131313", "--token="},
	{"yaml", "rqlite:\n  password: yaml-14141414\n  port: 5001", "yaml-14141414", "port: 5001"},
	{"yaml list", "- api_key: yl-15151515", "yl-15151515", "- api_key: "},
	{"url credentials", "dial https://admin:hunter22@10.0.0.1:5001/db", "hunter22", "https://"},
	{"url user", "redis://user1:pa55word99@host", "user1", "@host"},
	{"wireguard", "[Interface]\nPrivateKey = wgPrivKeyBase64Value=\nListenPort = 51820", "wgPrivKeyBase64Value", "ListenPort = 51820"},
	{"wireguard psk", "PresharedKey = wgPsk00000000000=", "wgPsk00000000000", "PresharedKey = "},
	{"swarm key", "/key/swarm/psk/1.0.0/\n/base16/\n" + strings.Repeat("ab", 32) + "\n", strings.Repeat("ab", 32), "/base16/"},
	{"escaped swarm key", `/key/swarm/psk/1.0.0/\n/base16/\n` + strings.Repeat("cd", 32), strings.Repeat("cd", 32), "/base16/"},
}

func TestRedact_shapes(t *testing.T) {
	for _, c := range shapeCases {
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

func TestRedact_testOutputFileNamesKept(t *testing.T) {
	var r *Redactor
	in := "    token_test.go:12: want 401, got 200\n    apikey_test.go:40: refused"
	if got := r.Redact(in); got != in {
		t.Fatalf("test output mangled:\n%s", got)
	}
}

func TestBoundTo_cutsOnRuneBoundary(t *testing.T) {
	in := strings.Repeat("é", 10) // two bytes per rune
	got := boundTo(in, 5)
	if got != "éé"+TruncatedMarker {
		t.Fatalf("got %q", got)
	}
	if boundTo("short", 5) != "short" {
		t.Fatal("input at the limit was cut")
	}
}

func TestAdd_capsValues(t *testing.T) {
	r := NewRedactor()
	vals := make([]string, MaxValues+2)
	for i := range vals {
		vals[i] = strings.Repeat("v", minValueLength) + "-" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + itoa(i)
	}
	if err := r.Add(vals...); err == nil {
		t.Fatal("more than MaxValues accepted without an error")
	}
	if len(r.values) != MaxValues {
		t.Fatalf("holds %d values, want the cap %d", len(r.values), MaxValues)
	}
}

func itoa(i int) string {
	var b []byte
	for {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
		if i == 0 {
			return string(b)
		}
	}
}
