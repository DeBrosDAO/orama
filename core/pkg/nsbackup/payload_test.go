package nsbackup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/secrets"
	"golang.org/x/crypto/nacl/box"
)

const (
	testCIDv0 = "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"
	testCIDv1 = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
)

func testDB() []byte {
	return append([]byte(SQLiteMagic), []byte("page data")...)
}

func testPayload() Payload {
	return Payload{
		Namespace: "myapp",
		Pins:      []string{testCIDv0, testCIDv1},
		Secrets: []Secret{
			{Table: "function_secrets", Column: "encrypted_value", IDs: []string{"7"}, Value: "sk_live_1"},
			{Table: "push_topics", Column: "token_encrypted", IDs: []string{"myapp", "t1"}, Value: "tok"},
		},
		RQLite: testDB(),
	}
}

func TestUnmarshalPayload_round_trip_through_seal(t *testing.T) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want := testPayload()
	plain, err := want.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Seal(pub, plain)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(priv, blob)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalPayload(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestUnmarshalPayload_empty_pins_and_secrets(t *testing.T) {
	p := Payload{Namespace: "myapp", RQLite: testDB()}
	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalPayload(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pins) != 0 || len(got.Secrets) != 0 || !bytes.Equal(got.RQLite, p.RQLite) {
		t.Fatalf("got %+v", got)
	}
}

func TestUnmarshalPayload_truncated(t *testing.T) {
	b, err := testPayload().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 3, frameFixed, frameFixed + 10, len(b) - 1} {
		if _, err := UnmarshalPayload(b[:n]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("truncated to %d bytes: %v", n, err)
		}
	}
}

func TestUnmarshalPayload_corrupt(t *testing.T) {
	b, err := testPayload().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]byte){
		"magic":      func(c []byte) { c[0] = 'X' },
		"version":    func(c []byte) { c[4] = 9 },
		"length":     func(c []byte) { c[5] = 0xff },
		"sqlite bit": func(c []byte) { c[len(c)-1] ^= 1 },
	}
	for name, mutate := range cases {
		c := append([]byte(nil), b...)
		mutate(c)
		if _, err := UnmarshalPayload(c); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := UnmarshalPayload(append(b, 'x')); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("appended byte: %v", err)
	}
}

func TestUnmarshalPayload_refuses_a_request_frame(t *testing.T) {
	req, err := testPayload().Rewrap(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalPayload(b); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("request frame read as a payload: %v", err)
	}
}

func TestPayloadMarshal_refuses_invalid_contents(t *testing.T) {
	cases := map[string]func(*Payload){
		"namespace":     func(p *Payload) { p.Namespace = "../x" },
		"not sqlite":    func(p *Payload) { p.RQLite = []byte("hello") },
		"bad cid":       func(p *Payload) { p.Pins = []string{"not-a-cid"} },
		"duplicate cid": func(p *Payload) { p.Pins = []string{testCIDv0, testCIDv0} },
		"unknown column": func(p *Payload) {
			p.Secrets = []Secret{{Table: "api_keys", Column: "key", IDs: []string{"1"}, Value: "x"}}
		},
		"wrong id count": func(p *Payload) {
			p.Secrets = []Secret{{Table: "push_topics", Column: "token_encrypted", IDs: []string{"1"}, Value: "x"}}
		},
		"empty id": func(p *Payload) {
			p.Secrets = []Secret{{Table: "function_secrets", Column: "encrypted_value", IDs: []string{""}, Value: "x"}}
		},
	}
	for name, mutate := range cases {
		p := testPayload()
		mutate(&p)
		if _, err := p.Marshal(); err == nil {
			t.Fatalf("%s: marshalled", name)
		}
	}
}

func TestOpenSecrets_round_trip(t *testing.T) {
	root := secrets.Root{CurrentID: "1", CurrentIKM: strings.Repeat("a", 64)}
	pub, priv, err := RestoreKey(root, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	p := testPayload()
	req, err := p.Rewrap(pub)
	if err != nil {
		t.Fatal(err)
	}
	b, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("sk_live_1")) {
		t.Fatal("restore request carries a secret in the clear")
	}
	got, err := UnmarshalRestoreRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := got.OpenSecrets(priv)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, p.Secrets) {
		t.Fatalf("secrets: got %+v want %+v", opened, p.Secrets)
	}
	if got.Namespace != p.Namespace || !reflect.DeepEqual(got.Pins, p.Pins) || !bytes.Equal(got.RQLite, p.RQLite) {
		t.Fatalf("request: %+v", got)
	}
}

func TestOpenSecrets_wrong_key_opens_nothing(t *testing.T) {
	req, err := testPayload().Rewrap(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	other := secrets.Root{CurrentID: "1", CurrentIKM: strings.Repeat("b", 64)}
	_, priv, err := RestoreKey(other, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	got, err := req.OpenSecrets(priv)
	if !errors.Is(err, ErrNotForKey) || got != nil {
		t.Fatalf("wrong key: %v, %v", got, err)
	}
}

func TestRestoreKey_follows_the_encryption_root(t *testing.T) {
	a1, _, err := RestoreKey(secrets.Root{CurrentIKM: "root-a"}, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := RestoreKey(secrets.Root{CurrentIKM: "root-a"}, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := RestoreKey(secrets.Root{CurrentIKM: "root-b"}, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if *a1 != *a2 || *a1 == *b {
		t.Fatal("restore key is not a function of the encryption root")
	}
	if _, _, err := RestoreKey(secrets.Root{}, "myapp"); err == nil {
		t.Fatal("empty root derived a key")
	}
}

func TestRewrap_requires_a_key(t *testing.T) {
	if _, err := testPayload().Rewrap(nil); err == nil {
		t.Fatal("rewrapped to a nil key")
	}
}

func mustKey(t *testing.T) *[32]byte {
	t.Helper()
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
