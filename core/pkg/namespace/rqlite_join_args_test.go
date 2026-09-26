package namespace

import (
	"os"
	"path/filepath"
	"testing"
)

func authFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rqlite-auth.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// rqlited runs with -auth, which refuses an anonymous join: the superman join
// failed "unauthorized (did you forget to set -join-as?)" on every attempt.
func TestRQLiteJoinArgs_joinsAsTheAuthUser(t *testing.T) {
	auth := authFile(t, `[{"username":"orama","password":"x","perms":["all"]}]`)
	got, err := rqliteJoinArgs([]string{"10.0.0.1:10101", "10.0.0.3:10101"}, auth)
	if err != nil {
		t.Fatalf("rqliteJoinArgs: %v", err)
	}
	if want := "-join 10.0.0.1:10101,10.0.0.3:10101 -join-as orama"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The genesis node joins nothing and needs no auth file to say so.
func TestRQLiteJoinArgs_nothingToJoin(t *testing.T) {
	got, err := rqliteJoinArgs(nil, filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || got != "" {
		t.Fatalf("rqliteJoinArgs(nil) = %q, %v; want empty", got, err)
	}
}

func TestRQLiteJoinArgs_noUserMayJoin(t *testing.T) {
	auth := authFile(t, `[{"username":"reader","password":"x","perms":["query"]}]`)
	if got, err := rqliteJoinArgs([]string{"10.0.0.1:10101"}, auth); err == nil {
		t.Fatalf("rqliteJoinArgs = %q, want an error", got)
	}
}
