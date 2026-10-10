package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

const legacyTestSecret = "cluster-secret-for-legacy-key-tests"

func legacyKID(t *testing.T) string {
	t.Helper()
	legacy, err := LegacyClusterSigningKey(legacyTestSecret)
	if err != nil {
		t.Fatalf("derive legacy key: %v", err)
	}
	return auth.KeyIDFor(legacy)
}

// The first boot of this release arms the key for one token lifetime.
func TestArmLegacyClusterKey_firstBootArmsForOneLifetime(t *testing.T) {
	dir := t.TempDir()
	// The wall clock: Lookup judges liveness by it.
	now := time.Now()
	keys := auth.NewSigningKeys(nil, nil)

	if err := armLegacyClusterKey(keys, dir, legacyTestSecret, now, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if _, ok := keys.Lookup(legacyKID(t)); !ok {
		t.Fatal("the legacy key does not verify on the first boot of this release")
	}
	info, err := os.Stat(filepath.Join(dir, legacyKeyRetirementFileName))
	if err != nil {
		t.Fatalf("no retirement record written: %v", err)
	}
	if info.Mode().Perm() != legacyKeyRetirementFileMode {
		t.Errorf("record mode %v, want %v", info.Mode().Perm(), os.FileMode(legacyKeyRetirementFileMode))
	}
}

// The bug: every restart set RetiredAt to now + lifetime, so a gateway that
// restarted within each lifetime kept a key every node can derive forever.
func TestArmLegacyClusterKey_restartDoesNotRearm(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if err := armLegacyClusterKey(auth.NewSigningKeys(nil, nil), dir, legacyTestSecret, first, true); err != nil {
		t.Fatalf("first boot: %v", err)
	}

	later := first.Add(auth.AccessTokenLifetime + time.Minute)
	keys := auth.NewSigningKeys(nil, nil)
	if err := armLegacyClusterKey(keys, dir, legacyTestSecret, later, false); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if _, ok := keys.Lookup(legacyKID(t)); ok {
		t.Fatal("a restart after the recorded retirement re-armed the cluster-derived key")
	}
}

// A restart inside the window keeps the original deadline, not a new one.
func TestLegacyKeyRetirement_keepsTheFirstDeadline(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	want, err := legacyKeyRetirement(dir, first, auth.AccessTokenLifetime)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	got, err := legacyKeyRetirement(dir, first.Add(5*time.Minute), auth.AccessTokenLifetime)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("deadline moved from %v to %v on restart", want, got)
	}
}

// A record that cannot be parsed is refused, never replaced by a fresh window.
func TestLegacyKeyRetirement_unreadableRecordIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, legacyKeyRetirementFileName)
	if err := os.WriteFile(path, []byte("not a time\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyKeyRetirement(dir, time.Now(), auth.AccessTokenLifetime); err == nil {
		t.Fatal("an unparseable retirement record was accepted")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "not a time\n" {
		t.Errorf("the unparseable record was overwritten with %q", raw)
	}
}

// A gateway that did not replace a cluster-derived key does not arm one.
// The retirement is recorded so a later boot cannot open a fresh window.
func TestArmLegacyClusterKey_freshGatewayDoesNotArm(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	keys := auth.NewSigningKeys(nil, nil)
	if err := armLegacyClusterKey(keys, dir, legacyTestSecret, now, false); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if _, ok := keys.Lookup(legacyKID(t)); ok {
		t.Fatal("a gateway that never held the cluster-derived key is verifying with it")
	}
	raw, err := os.ReadFile(filepath.Join(dir, legacyKeyRetirementFileName))
	if err != nil {
		t.Fatalf("no retirement record: %v", err)
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if now.Before(at) {
		t.Fatalf("retirement %v is still in the future of %v", at, now)
	}
}

// With no cluster secret there is no legacy key and nothing is recorded.
func TestArmLegacyClusterKey_noClusterSecret(t *testing.T) {
	dir := t.TempDir()
	if err := armLegacyClusterKey(auth.NewSigningKeys(nil, nil), dir, "", time.Now(), false); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyKeyRetirementFileName)); !os.IsNotExist(err) {
		t.Errorf("a record was written for a gateway with no cluster secret: %v", err)
	}
}
