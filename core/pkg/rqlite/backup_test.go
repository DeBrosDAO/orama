package rqlite

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func backupName(t time.Time) string {
	return backupPrefix + t.Format(backupTimestampFormat) + backupSuffix
}

func TestBackupsToKeep_keepsRecentHoursAndOneADay(t *testing.T) {
	// A flat "keep the newest 3" covers three hours, and only the hours this
	// node happened to be leader for. What an operator usually needs is the
	// state before a change that turned out to be wrong, which may be days back.
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	var names []string
	// 30 hourly backups over the last 30 hours.
	for i := 0; i < 30; i++ {
		names = append(names, backupName(base.Add(-time.Duration(i)*time.Hour)))
	}
	// One a day for the previous 10 days.
	for d := 2; d <= 11; d++ {
		names = append(names, backupName(base.AddDate(0, 0, -d)))
	}

	keep := backupsToKeep(names)

	// The newest 24 hourly ones survive.
	for i := 0; i < hourlyRetention; i++ {
		name := backupName(base.Add(-time.Duration(i) * time.Hour))
		if !keep[name] {
			t.Errorf("hourly backup %s (%d hours old) was pruned", name, i)
		}
	}

	// Something older than the hourly window survives, or the history is only
	// as deep as the hourly window.
	deep := 0
	for name := range keep {
		ts, err := backupTimestamp(name)
		if err != nil {
			continue
		}
		if base.Sub(ts) > time.Duration(hourlyRetention)*time.Hour {
			deep++
		}
	}
	if deep == 0 {
		t.Fatal("nothing older than the hourly window survived; the history is only as deep as the hourly window")
	}
}

func TestBackupsToKeep_keepsEverythingWhenThereIsLittle(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	names := []string{
		backupName(base),
		backupName(base.Add(-time.Hour)),
		backupName(base.Add(-2 * time.Hour)),
	}

	keep := backupsToKeep(names)
	for _, name := range names {
		if !keep[name] {
			t.Errorf("%s was pruned from a set of three", name)
		}
	}
}

func TestBackupsToKeep_keepsUnparseableNames(t *testing.T) {
	// Deleting a file this cannot identify is the one outcome worth avoiding.
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	names := []string{backupName(base), backupPrefix + "not-a-timestamp" + backupSuffix}

	keep := backupsToKeep(names)
	if !keep[backupPrefix+"not-a-timestamp"+backupSuffix] {
		t.Fatal("a backup whose name could not be parsed was pruned")
	}
}

func TestBackupsToKeep_emptyInput(t *testing.T) {
	if got := backupsToKeep(nil); len(got) != 0 {
		t.Fatalf("got %v, want an empty set", got)
	}
}

func TestSealAndOpenBackup_roundTrips(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("key: %v", err)
	}
	plaintext := []byte("SQLite format 3\x00 ... a registry snapshot ...")

	sealed, err := sealBackup(plaintext, key)
	if err != nil {
		t.Fatalf("sealBackup: %v", err)
	}
	// A registry snapshot holds every API key and DNS record in the fleet;
	// pinning it in the clear would publish that to anyone who can reach the
	// cluster's IPFS.
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("the sealed snapshot contains its plaintext")
	}

	opened, err := OpenBackup(sealed, key)
	if err != nil {
		t.Fatalf("OpenBackup: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("the round trip did not return the original snapshot")
	}
}

func TestOpenBackup_rejectsTheWrongKeyAndShortInput(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sealed, err := sealBackup([]byte("data"), key)
	if err != nil {
		t.Fatalf("sealBackup: %v", err)
	}

	wrong := make([]byte, 32)
	wrong[0] = key[0] + 1
	if _, err := OpenBackup(sealed, wrong); err == nil {
		t.Fatal("the wrong key opened the snapshot")
	}

	if _, err := OpenBackup([]byte("short"), key); err == nil {
		t.Fatal("a truncated file was accepted as an encrypted backup")
	}
}

func TestVerifyBackup(t *testing.T) {
	plaintext := []byte("a registry snapshot")
	// Digest of the plaintext, as recordBackup stores it.
	sealed, _ := sealBackup(plaintext, make([]byte, 32))
	_ = sealed

	if err := VerifyBackup(plaintext, "0000"); err == nil {
		t.Fatal("a mismatched digest was accepted")
	}

	// The real digest round-trips.
	sum := sha256.Sum256(plaintext)
	good := hex.EncodeToString(sum[:])
	if err := VerifyBackup(plaintext, good); err != nil {
		t.Fatalf("the correct digest was rejected: %v", err)
	}
}
