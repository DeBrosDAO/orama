package legacylayout

import (
	"os"
	"testing"
)

// readOnlySecrets makes secrets/ unwritable to this process, as it is to
// orama-node when root owns it or the unit mounts it read-only.
func readOnlySecrets(t *testing.T, f *fixture) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root may write any directory; the copy path needs an unprivileged user")
	}
	if err := os.Chmod(f.path("secrets"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(f.path("secrets"), 0o700) })
}

func TestRun_copiesKeysOutOfAnUnwritableSecretsDir(t *testing.T) {
	f := newFixture(t)
	f.write(t, "secrets/jwt-signing-key.pem", "rsa")
	f.write(t, "secrets/jwt-eddsa-key.pem", "ed")
	readOnlySecrets(t, f)

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := mustRead(t, f.path("data/namespaces/index/gateway/jwt-signing-key.pem")); got != "rsa" {
		t.Errorf("copied RSA key = %q", got)
	}
	if got := mustRead(t, f.path("secrets/jwt-signing-key.pem")); got != "rsa" {
		t.Errorf("the original must stay for root to delete: %q", got)
	}
	copied, err := ReadCopiedKeys(CopiedKeysMarker(f.oramaDir))
	if err != nil {
		t.Fatal(err)
	}
	if copied["jwt-signing-key.pem"] != KeyDigest([]byte("rsa")) || copied["jwt-eddsa-key.pem"] != KeyDigest([]byte("ed")) {
		t.Errorf("marker %v does not record both copies", copied)
	}

	// The gateway replaces the cluster-derived EdDSA key it was handed. The
	// original still matches what was copied, so the next boot waits for root
	// instead of refusing.
	writeFile(t, f.path("data/namespaces/index/gateway/jwt-eddsa-key.pem"), "gateway's own")
	if err := f.run(t); err != nil {
		t.Fatalf("a copy awaiting root's removal must not be a conflict: %v", err)
	}

	// Root removed the originals: the marker has nothing left to record.
	os.Chmod(f.path("secrets"), 0o700)
	for _, n := range SigningKeyNames {
		os.Remove(f.path("secrets/" + n))
	}
	if err := f.run(t); err != nil {
		t.Fatalf("after root's removal: %v", err)
	}
	assertGone(t, CopiedKeysMarker(f.oramaDir))
}

// An original that no longer matches what was copied is a second key, not the
// same one twice.
func TestRun_copiedKeyWhoseOriginalChangedIsAConflict(t *testing.T) {
	f := newFixture(t)
	f.write(t, "secrets/jwt-signing-key.pem", "rsa")
	readOnlySecrets(t, f)
	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	os.Chmod(f.path("secrets"), 0o700)
	f.write(t, "secrets/jwt-signing-key.pem", "a different key")
	os.Chmod(f.path("secrets"), 0o500)

	err := f.run(t)
	assertNamesBoth(t, err, f.path("secrets/jwt-signing-key.pem"), f.path("data/namespaces/index/gateway/jwt-signing-key.pem"))
}

// The marker is written before the copy, so a run stopped between the two is
// simply repeated.
func TestRun_interruptedCopyIsRepeated(t *testing.T) {
	f := newFixture(t)
	f.write(t, "secrets/jwt-signing-key.pem", "rsa")
	readOnlySecrets(t, f)
	writeFile(t, CopiedKeysMarker(f.oramaDir), `{"jwt-signing-key.pem":"`+KeyDigest([]byte("rsa"))+`"}`)

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := mustRead(t, f.path("data/namespaces/index/gateway/jwt-signing-key.pem")); got != "rsa" {
		t.Errorf("the interrupted copy was not made: %q", got)
	}
}

func TestRun_refusesASymlinkedSigningKey(t *testing.T) {
	f := newFixture(t)
	outside := f.path("configs/elsewhere.pem")
	f.write(t, "configs/elsewhere.pem", "not a key")
	if err := os.Symlink(outside, f.path("secrets/jwt-signing-key.pem")); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t); err == nil {
		t.Fatal("a symlink in place of a signing key must be refused")
	}
	assertGone(t, f.path("data/namespaces/index/gateway/jwt-signing-key.pem"))
}

// Clearing a spent marker is a change like any other: a refusal elsewhere
// leaves it in place.
func TestRun_refusalLeavesASpentMarkerInPlace(t *testing.T) {
	f := newFixture(t)
	writeFile(t, CopiedKeysMarker(f.oramaDir), `{"jwt-signing-key.pem":"`+KeyDigest([]byte("rsa"))+`"}`)
	f.write(t, "configs/turn.yaml", "old")
	f.write(t, "data/turn/turn.yaml", "new")

	if err := f.run(t); err == nil {
		t.Fatal("the TURN config on both layouts must be refused")
	}
	if _, err := os.Stat(CopiedKeysMarker(f.oramaDir)); err != nil {
		t.Errorf("a refused migration removed the marker: %v", err)
	}
}
