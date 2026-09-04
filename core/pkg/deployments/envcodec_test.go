package deployments

import (
	"encoding/json"
	"strings"
	"testing"
)

func newTestCodec(t *testing.T) *EnvCodec {
	t.Helper()
	c, err := NewEnvCodec("a-cluster-secret")
	if err != nil {
		t.Fatalf("NewEnvCodec: %v", err)
	}
	return c
}

func TestEnvCodec_roundTrip(t *testing.T) {
	c := newTestCodec(t)
	env := map[string]string{
		"DATABASE_URL": "postgres://user:p@ss@host/db",
		"MULTILINE":    "-----BEGIN KEY-----\nabc\n-----END KEY-----",
		"EMPTY":        "",
	}

	stored, err := c.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(stored)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(env) {
		t.Fatalf("decoded %d variables, encoded %d", len(got), len(env))
	}
	for key, want := range env {
		if got[key] != want {
			t.Errorf("%s: decoded %q, encoded %q", key, got[key], want)
		}
	}
}

// The whole point: what lands in the database must not be the tenant's secrets.
func TestEnvCodec_theStoredFormDoesNotContainTheValues(t *testing.T) {
	c := newTestCodec(t)
	stored, err := c.Encode(map[string]string{"STRIPE_KEY": "sk_live_supersecret"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(stored, "sk_live_supersecret") {
		t.Fatalf("the stored environment contains the secret in the clear: %s", stored)
	}
	if strings.Contains(stored, "STRIPE_KEY") {
		t.Fatalf("the stored environment leaks the variable name: %s", stored)
	}
	if !c.IsEncrypted(stored) {
		t.Fatalf("the stored environment is not marked as encrypted: %s", stored)
	}
}

func TestEnvCodec_encryptsDifferentlyEachTime(t *testing.T) {
	c := newTestCodec(t)
	env := map[string]string{"K": "v"}
	first, err := c.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := c.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if first == second {
		t.Fatal("two encodings of the same environment are byte-identical, so the nonce is not fresh")
	}
}

// Deployments created before the column was encrypted hold plaintext JSON.
// Refusing to read them would strand every existing deployment.
func TestEnvCodec_readsALegacyPlaintextRow(t *testing.T) {
	c := newTestCodec(t)
	legacy, err := json.Marshal(map[string]string{"OLD": "value"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := c.Decode(string(legacy))
	if err != nil {
		t.Fatalf("Decode of a legacy row: %v", err)
	}
	if got["OLD"] != "value" {
		t.Fatalf("legacy row decoded to %#v", got)
	}
	if c.IsEncrypted(string(legacy)) {
		t.Fatal("a legacy plaintext row is reported as encrypted")
	}
}

func TestEnvCodec_emptyStoredValues(t *testing.T) {
	c := newTestCodec(t)
	for _, stored := range []string{"", "   ", "null", "{}"} {
		got, err := c.Decode(stored)
		if err != nil {
			t.Fatalf("Decode(%q): %v", stored, err)
		}
		if len(got) != 0 {
			t.Fatalf("Decode(%q) = %#v, want an empty environment", stored, got)
		}
	}
}

// An environment that cannot be read is not an empty environment. Returning one
// starts the app without its database URL and its API keys, which looks like
// the tenant's own bug.
func TestEnvCodec_refusesWhatItCannotRead(t *testing.T) {
	c := newTestCodec(t)
	if _, err := c.Decode("not json at all"); err == nil {
		t.Error("a corrupt row decoded to an environment instead of an error")
	}
	if _, err := c.Decode("enc:not-base64!!"); err == nil {
		t.Error("an undecodable sealed row decoded to an environment instead of an error")
	}

	other, err := NewEnvCodec("a-different-cluster-secret")
	if err != nil {
		t.Fatalf("NewEnvCodec: %v", err)
	}
	stored, err := c.Encode(map[string]string{"K": "v"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := other.Decode(stored); err == nil {
		t.Error("a row sealed with another cluster's key decoded without error")
	}
}

func TestNewEnvCodec_refusesAnEmptyClusterSecret(t *testing.T) {
	for _, secret := range []string{"", "   ", "\n"} {
		if _, err := NewEnvCodec(secret); err == nil {
			t.Errorf("NewEnvCodec(%q) returned a codec; with no secret there is no key and the environment would be stored in the clear", secret)
		}
	}
}

func TestEnvCodec_nilCodecRefusesInsteadOfStoringPlaintext(t *testing.T) {
	var c *EnvCodec
	if _, err := c.Encode(map[string]string{"K": "v"}); err == nil {
		t.Error("a nil codec encoded an environment")
	}
	if _, err := c.Decode("{}"); err == nil {
		t.Error("a nil codec decoded an environment")
	}
}

func TestEnvCodec_refusesAValueItCouldNotDeliver(t *testing.T) {
	c := newTestCodec(t)
	if _, err := c.Encode(map[string]string{"K": "\xff"}); err == nil {
		t.Error("the codec stored a value systemd would discard at runtime")
	}
	if _, err := c.Encode(map[string]string{"1BAD": "x"}); err == nil {
		t.Error("the codec stored an unusable variable name")
	}
}

func TestEnvCodec_encodesNilAsAnEmptyEnvironment(t *testing.T) {
	c := newTestCodec(t)
	stored, err := c.Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil): %v", err)
	}
	got, err := c.Decode(stored)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got == nil {
		t.Fatal("Decode returned a nil map, which a caller will write into")
	}
	if len(got) != 0 {
		t.Fatalf("Decode returned %#v", got)
	}
}
