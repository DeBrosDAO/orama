package tlsstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore opens an in-memory registry with the migration's tables.
func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	migration, err := os.ReadFile("../../migrations/076_tls_store.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}

func TestStore_putLoadReplace(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, "certificates/ca/a/a.crt", "v1.one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "certificates/ca/a/a.crt", "v1.two"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(ctx, "certificates/ca/a/a.crt")
	if err != nil || got != "v1.two" {
		t.Fatalf("Load = %q, %v; want the last stored value", got, err)
	}
}

func TestStore_loadMissing(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Load(context.Background(), "nope"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Load of a missing key: %v, want ErrNotExist", err)
	}
}

func TestStore_statFileDirectoryAndMissing(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.now = func() time.Time { return time.UnixMilli(1_790_000_000_000) }
	if err := s.Put(ctx, "certificates/ca/a/a.crt", "v1.abc"); err != nil {
		t.Fatal(err)
	}
	f, err := s.Stat(ctx, "certificates/ca/a/a.crt")
	if err != nil || !f.IsTerminal || f.Size != 6 || f.Modified.UnixMilli() != 1_790_000_000_000 {
		t.Fatalf("file Stat = %+v, %v", f, err)
	}
	d, err := s.Stat(ctx, "certificates/ca")
	if err != nil || d.IsTerminal {
		t.Fatalf("directory Stat = %+v, %v", d, err)
	}
	// "certificates/c" is a prefix of a key's text, not a component of it.
	if _, err := s.Stat(ctx, "certificates/c"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Stat of a partial component: %v, want ErrNotExist", err)
	}
}

func TestStore_deleteDirectory(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, k := range []string{"certificates/ca/a/a.crt", "certificates/ca/a/a.key", "certificates/cab/b.crt"} {
		if err := s.Put(ctx, k, "v1.x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(ctx, "certificates/ca"); err != nil {
		t.Fatal(err)
	}
	keys, err := s.List(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"certificates/cab/b.crt"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("after deleting certificates/ca: %v, want %v (a sibling sharing its prefix stays)", keys, want)
	}
	if err := s.Delete(ctx, "never/stored"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
}

func TestStore_list(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, k := range []string{"certificates/ca/a/a.crt", "certificates/ca/a/a.key", "certificates/ca/b/b.crt", "acme/ca/users/u.json", "last_clean.json"} {
		if err := s.Put(ctx, k, "v1.x"); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		prefix    string
		recursive bool
		want      []string
	}{
		{"certificates/ca", false, []string{"certificates/ca/a", "certificates/ca/b"}},
		{"certificates/ca", true, []string{"certificates/ca/a/a.crt", "certificates/ca/a/a.key", "certificates/ca/b/b.crt"}},
		{"", false, []string{"acme", "certificates", "last_clean.json"}},
		{"missing", false, []string{}},
	}
	for _, c := range cases {
		got, err := s.List(ctx, c.prefix, c.recursive)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("List(%q, %v) = %v, want %v", c.prefix, c.recursive, got, c.want)
		}
	}
}

// LIKE wildcards in a key are matched literally.
func TestStore_listEscapesWildcards(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, k := range []string{"certificates/x_y/a", "certificates/xzy/b"} {
		if err := s.Put(ctx, k, "v1.x"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ctx, "certificates/x_y", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"certificates/x_y/a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"certificates/acme-v02.api.letsencrypt.org-directory/wildcard_.a.b/wildcard_.a.b.crt", "last_clean.json"} {
		if err := ValidKey(k); err != nil {
			t.Errorf("ValidKey(%q) = %v", k, err)
		}
	}
	for _, k := range []string{"", "/abs", "trailing/", "a//b", "a/../b", "./a", "sp ace", "back\\slash", "nl\n", string(make([]byte, MaxKeyLen+1))} {
		if err := ValidKey(k); err == nil {
			t.Errorf("ValidKey(%q) accepted", k)
		}
	}
}

// Keys compare case-sensitively: a directory differing only in case is
// another directory (LIKE would fold ASCII case).
func TestStore_prefixesAreCaseSensitive(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, "certificates/foo/a.crt", "v1.x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "certificates/Foo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "certificates/foo/a.crt"); err != nil {
		t.Fatalf("deleting certificates/Foo removed certificates/foo/a.crt: %v", err)
	}
	if got, _ := s.List(ctx, "certificates/Foo", true); len(got) != 0 {
		t.Errorf("List(certificates/Foo) = %v, want nothing", got)
	}
	if _, err := s.Stat(ctx, "certificates/Foo"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Stat(certificates/Foo) = %v, want ErrNotExist", err)
	}
}

// RQLite's driver returns INTEGER columns as float64, and a millisecond
// timestamp as 1.79092464079e+12, which database/sql refuses to scan into an
// int64 (it broke every stat on stagenet; SQLite returns int64, so the
// sqlite-backed tests above never saw it).
func TestInteger_readsWhatRQLiteReturns(t *testing.T) {
	cases := map[string]struct {
		in   any
		want int64
	}{
		"rqlite timestamp": {float64(1790924640790), 1790924640790},
		"rqlite size":      {float64(5120), 5120},
		"sqlite":           {int64(42), 42},
	}
	for name, c := range cases {
		got, err := integer(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s: integer(%v) = %d, %v; want %d", name, c.in, got, err, c.want)
		}
	}
	for _, bad := range []any{1.5, "12", nil, float64(1 << 60)} {
		if _, err := integer(bad); err == nil {
			t.Errorf("integer(%v) accepted it", bad)
		}
	}
}
