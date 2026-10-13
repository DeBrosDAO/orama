package deployments

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

const boundTestIKM = "a-cluster-secret"

func codecWithRoot(t *testing.T, root secrets.Root) *EnvCodec {
	t.Helper()
	c, err := NewEnvCodec(root.CurrentIKM)
	if err != nil {
		t.Fatalf("NewEnvCodec: %v", err)
	}
	c.SetHolder(secrets.NewHolder(root))
	return c
}

func boundCodec(t *testing.T) *EnvCodec {
	return codecWithRoot(t, secrets.Root{CurrentID: "1", CurrentIKM: boundTestIKM, WriteVersioned: true, WriteBound: true})
}

func unboundCodec(t *testing.T) *EnvCodec {
	return codecWithRoot(t, secrets.Root{CurrentID: "1", CurrentIKM: boundTestIKM})
}

func envelopeVersion(t *testing.T, stored string) int {
	t.Helper()
	env, err := secrets.ParseEnvelope(stored)
	if err != nil {
		t.Fatalf("ParseEnvelope(%.14s...): %v", stored, err)
	}
	return env.Version
}

func TestEnvCodec_boundRoundTrip(t *testing.T) {
	c := boundCodec(t)
	stored, err := c.Encode("acme", "dep-1", map[string]string{"DATABASE_URL": "postgres://u:p@h/db"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if v := envelopeVersion(t, stored); v != 2 {
		t.Fatalf("stored envelope version %d, want the bound envelope (2)", v)
	}
	got, err := c.Decode("acme", "dep-1", stored)
	if err != nil || got["DATABASE_URL"] != "postgres://u:p@h/db" {
		t.Fatalf("Decode = %v, %v", got, err)
	}
}

// The finding: one cluster-wide key and no additional data meant a ciphertext
// copied onto another deployment's row opened there.
func TestEnvCodec_aCiphertextMovedToAnotherRowDoesNotOpen(t *testing.T) {
	c := boundCodec(t)
	stored, err := c.Encode("acme", "dep-1", map[string]string{"STRIPE_KEY": "sk_live_supersecret"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if _, err := c.Decode("acme", "dep-2", stored); err == nil {
		t.Error("the environment opened for another deployment of the same namespace")
	}
	if _, err := c.Decode("mallory", "dep-1", stored); err == nil {
		t.Error("the environment opened for another namespace")
	}
	if _, err := c.Decode("mallory", "dep-2", stored); err == nil {
		t.Error("the environment opened for another namespace and deployment")
	}
}

// Rows sealed before the binding existed hold the unbound envelopes. They
// must keep opening, for the row they are in, under a codec that now writes
// bound ones.
func TestEnvCodec_rowsSealedWithoutABindingStillOpen(t *testing.T) {
	old := unboundCodec(t)
	legacy, err := old.Encode("acme", "dep-1", map[string]string{"OLD": "value"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if v := envelopeVersion(t, legacy); v != 0 {
		t.Fatalf("an unbound codec wrote envelope version %d, want the legacy one (0)", v)
	}

	versioned := codecWithRoot(t, secrets.Root{CurrentID: "1", CurrentIKM: boundTestIKM, WriteVersioned: true})
	mid, err := versioned.Encode("acme", "dep-1", map[string]string{"MID": "value"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if v := envelopeVersion(t, mid); v != 1 {
		t.Fatalf("a versioned codec wrote envelope version %d, want 1", v)
	}

	c := boundCodec(t)
	if got, err := c.Decode("acme", "dep-1", legacy); err != nil || got["OLD"] != "value" {
		t.Errorf("legacy row: %v, %v", got, err)
	}
	if got, err := c.Decode("acme", "dep-1", mid); err != nil || got["MID"] != "value" {
		t.Errorf("versioned row: %v, %v", got, err)
	}
}

// Rolling upgrade, the other direction: until the operator enables bound
// writes a node writes what a node without this change reads.
func TestEnvCodec_writesAnUnboundEnvelopeUntilBoundWritesAreEnabled(t *testing.T) {
	stored, err := unboundCodec(t).Encode("acme", "dep-1", map[string]string{"K": "v"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	plain, err := secrets.Decrypt(stored, mustKey(t))
	if err != nil || !strings.Contains(plain, `"K":"v"`) {
		t.Fatalf("a reader without the binding got %q, %v", plain, err)
	}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := secrets.DeriveKey(boundTestIKM, EnvEncryptionPurpose)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEnvCodec_refusesToBindToNothing(t *testing.T) {
	c := boundCodec(t)
	for _, ids := range [][2]string{{"", "dep-1"}, {"acme", ""}, {"", ""}} {
		if _, err := c.Encode(ids[0], ids[1], map[string]string{"K": "v"}); err == nil {
			t.Errorf("Encode(%q, %q) stored an environment bound to nothing", ids[0], ids[1])
		}
	}
	stored, err := c.Encode("acme", "dep-1", map[string]string{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Decode("", "", stored); err == nil {
		t.Error("a sealed environment was decoded with no row to check it against")
	}
}

// Plaintext rows (from before the column was encrypted) carry no seal to check
// and need no row, as before.
func TestEnvCodec_plaintextAndEmptyRowsNeedNoBinding(t *testing.T) {
	c := boundCodec(t)
	got, err := c.Decode("", "", `{"OLD":"value"}`)
	if err != nil || got["OLD"] != "value" {
		t.Fatalf("plaintext row: %v, %v", got, err)
	}
	if got, err := c.Decode("", "", ""); err != nil || len(got) != 0 {
		t.Fatalf("empty row: %v, %v", got, err)
	}
}

// A rotation re-seals under a new root; the binding survives it.
func TestEnvCodec_theBindingSurvivesARootRotation(t *testing.T) {
	prev := secrets.Root{CurrentID: "1", CurrentIKM: boundTestIKM, WriteVersioned: true, WriteBound: true}
	c := codecWithRoot(t, prev)
	stored, err := c.Encode("acme", "dep-1", map[string]string{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}

	c.SetHolder(secrets.NewHolder(secrets.Root{
		CurrentID: "2", CurrentIKM: "a-rotated-root", PreviousID: "1", PreviousIKM: boundTestIKM,
		WriteVersioned: true, WriteBound: true,
	}))
	if got, err := c.Decode("acme", "dep-1", stored); err != nil || got["K"] != "v" {
		t.Fatalf("a row under the previous root: %v, %v", got, err)
	}
	if _, err := c.Decode("acme", "dep-2", stored); err == nil {
		t.Error("a row under the previous root opened for another deployment")
	}
}
