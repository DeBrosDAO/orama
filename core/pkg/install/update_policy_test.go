package install

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
	_ "github.com/mattn/go-sqlite3"
)

const testRepo = "https://test-releases.example.org"

// settingsDB is a cluster_settings table the way migration 063 creates it.
func settingsDB(t *testing.T) (*sql.DB, settingExec) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE cluster_settings (
		key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_by TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	exec := func(ctx context.Context, query string, args ...any) error {
		_, err := db.ExecContext(ctx, query, args...)
		return err
	}
	return db, exec
}

func storedSettings(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT key, value FROM cluster_settings`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

func TestSeedUpdatePolicy_writesRepoAndChannelOnly(t *testing.T) {
	db, exec := settingsDB(t)
	if err := seedUpdatePolicy(t.Context(), exec, testRepo, "nightly"); err != nil {
		t.Fatal(err)
	}
	got := storedSettings(t, db)
	if len(got) != 2 || got[updatepolicy.KeyRepo] != testRepo || got[updatepolicy.KeyChannel] != "nightly" {
		t.Errorf("settings = %v", got)
	}
	if _, set := got[updatepolicy.KeyMode]; set {
		t.Errorf("auto_update was written; the cluster keeps the default")
	}
}

func TestSeedUpdatePolicy_keepsWhatAnOperatorSet(t *testing.T) {
	db, exec := settingsDB(t)
	if _, err := db.Exec(`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, 'main', 'operator:0xabc')`, updatepolicy.KeyChannel); err != nil {
		t.Fatal(err)
	}
	if err := seedUpdatePolicy(t.Context(), exec, testRepo, "nightly"); err != nil {
		t.Fatal(err)
	}
	got := storedSettings(t, db)
	if got[updatepolicy.KeyChannel] != "main" {
		t.Errorf("channel = %q, want the operator's main", got[updatepolicy.KeyChannel])
	}
	if got[updatepolicy.KeyRepo] != testRepo {
		t.Errorf("repo = %q, want it stored where the operator set none", got[updatepolicy.KeyRepo])
	}
}

func TestSeedUpdatePolicy_emptyValuesWriteNothing(t *testing.T) {
	db, exec := settingsDB(t)
	if err := seedUpdatePolicy(t.Context(), exec, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := storedSettings(t, db); len(got) != 0 {
		t.Errorf("settings = %v", got)
	}
	if err := seedUpdatePolicy(t.Context(), exec, "", "main"); err != nil {
		t.Fatal(err)
	}
	if got := storedSettings(t, db); len(got) != 1 || got[updatepolicy.KeyChannel] != "main" {
		t.Errorf("settings = %v", got)
	}
}

func TestSeedUpdatePolicy_refusesInvalidValuesBeforeWriting(t *testing.T) {
	db, exec := settingsDB(t)
	for name, c := range map[string]struct{ repo, channel string }{
		"plain http repo": {"http://releases.example.org", "nightly"},
		"bad channel":     {testRepo, "Not A Channel"},
	} {
		err := seedUpdatePolicy(t.Context(), exec, c.repo, c.channel)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if got := storedSettings(t, db); len(got) != 0 {
			t.Errorf("%s: wrote %v", name, got)
		}
	}
}

func TestSeedUpdatePolicy_namesTheSettingThatFailedToStore(t *testing.T) {
	boom := errors.New("no such table: cluster_settings")
	exec := func(context.Context, string, ...any) error { return boom }
	err := seedUpdatePolicy(t.Context(), exec, testRepo, "nightly")
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), updatepolicy.KeyRepo) {
		t.Errorf("error = %v", err)
	}
}
