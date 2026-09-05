package rqlite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAdminClient_sends_basic_auth_when_configured is the property the whole
// change exists for: every admin call carries credentials.
func TestAdminClient_sends_basic_auth_when_configured(t *testing.T) {
	type seen struct {
		user, pass string
		ok         bool
		method     string
		path       string
	}
	var got seen

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.user, got.pass, got.ok = r.BasicAuth()
		got.method, got.path = r.Method, r.URL.Path
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, "orama", "s3cret")

	for _, tc := range []struct {
		name   string
		call   func() error
		method string
		path   string
	}{
		{"Status", func() error { _, err := c.Status(context.Background()); return err }, http.MethodGet, "/status"},
		{"Join", func() error { return c.Join(context.Background(), "id", "addr", true) }, http.MethodPost, "/join"},
		{"Remove", func() error { return c.Remove(context.Background(), "id") }, http.MethodDelete, "/remove"},
		{"Backup", func() error { _, err := c.Backup(context.Background()); return err }, http.MethodGet, "/db/backup"},
		{"Ready", func() error { return c.Ready(context.Background()) }, http.MethodGet, "/readyz"},
		{"TransferLeadership", func() error { return c.TransferLeadership(context.Background(), "id") }, http.MethodPost, "/leader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = seen{}
			if err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !got.ok {
				t.Fatalf("%s sent no Authorization header", tc.name)
			}
			if got.user != "orama" || got.pass != "s3cret" {
				t.Fatalf("%s sent %q/%q", tc.name, got.user, got.pass)
			}
			if got.method != tc.method || got.path != tc.path {
				t.Fatalf("%s hit %s %s, want %s %s", tc.name, got.method, got.path, tc.method, tc.path)
			}
		})
	}
}

// Empty credentials must mean no header at all, not an empty one: that is the
// state every node is in today, with rqlited running without -auth.
func TestAdminClient_omits_auth_when_unconfigured(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Authorization") != ""
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := NewAdminClient(srv.URL, "", "").Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sawHeader {
		t.Fatal("sent an Authorization header with no credentials configured")
	}
}

// A 401 must say so. The whole failure mode this change guards against is an
// auth rejection that reads as a broken cluster.
func TestAdminClient_401_names_credentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewAdminClient(srv.URL, "", "").Nodes(context.Background())
	if err == nil {
		t.Fatal("want an error on 401")
	}
	if !strings.Contains(err.Error(), "credentials") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 error does not name credentials: %v", err)
	}
}

func TestAdminClient_non_2xx_carries_the_body(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("no leader"))
	}))
	defer srv.Close()

	err := NewAdminClient(srv.URL, "", "").Remove(context.Background(), "node-1")
	if err == nil || !strings.Contains(err.Error(), "no leader") || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want the status and body in the error, got %v", err)
	}
}

// Backup is allowed two minutes. A client-level timeout would silently cap it
// at the quick-read budget, so a large snapshot would fail as if the node were
// unreachable.
func TestAdminClient_backup_outlives_the_quick_timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(adminQuickTimeout + 500*time.Millisecond)
		w.Write([]byte("snapshot"))
	}))
	defer srv.Close()

	data, err := NewAdminClient(srv.URL, "", "").Backup(context.Background())
	if err != nil {
		t.Fatalf("backup cut short: %v", err)
	}
	if string(data) != "snapshot" {
		t.Fatalf("got %q", data)
	}
}

// Join must send id and addr as separate fields. Collapsing them is the exact
// mistake that made rqlite treat a moved address as a new member.
func TestAdminClient_join_sends_id_and_addr_separately(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := NewAdminClient(srv.URL, "", "").Join(context.Background(), "12D3Koo", "10.0.0.4:10101", true); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "12D3Koo" || body["addr"] != "10.0.0.4:10101" || body["voter"] != true {
		t.Fatalf("join body %#v", body)
	}
}

func TestReadRQLiteAuthFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("first named user", func(t *testing.T) {
		p := filepath.Join(dir, "auth.json")
		os.WriteFile(p, []byte(`[{"username":"orama","password":"pw","perms":["all"]},{"username":"other","password":"x"}]`), 0600)
		u, pw, err := readRQLiteAuthFile(p)
		if err != nil || u != "orama" || pw != "pw" {
			t.Fatalf("got %q/%q err %v", u, pw, err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, _, err := readRQLiteAuthFile(filepath.Join(dir, "nope.json")); err == nil {
			t.Fatal("want an error for a missing file")
		}
	})

	t.Run("unparseable", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		os.WriteFile(p, []byte(`not json`), 0600)
		if _, _, err := readRQLiteAuthFile(p); err == nil {
			t.Fatal("want an error for unparseable JSON")
		}
	})

	t.Run("no users", func(t *testing.T) {
		p := filepath.Join(dir, "empty.json")
		os.WriteFile(p, []byte(`[]`), 0600)
		if _, _, err := readRQLiteAuthFile(p); err == nil {
			t.Fatal("want an error when the file names no user")
		}
	})
}

// An unreadable auth file must degrade to unauthenticated, so the caller gets
// rqlite's own 401 rather than a start-up failure.
func TestAdminCredentialsFromFile_unreadable_is_empty(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "absent.json")} {
		u, p := adminCredentialsFromFile(path)
		if u != "" || p != "" {
			t.Fatalf("path %q yielded %q/%q", path, u, p)
		}
	}
}
