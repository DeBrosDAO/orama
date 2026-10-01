package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

func newRQLiteTestLogger() *logging.ColoredLogger {
	l, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	return l
}

func TestRqliteBaseURL(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"simple URL", "http://10.0.0.1:10000", "http://10.0.0.1:10000"},
		{"strips query params", "http://10.0.0.1:10000?foo=bar", "http://10.0.0.1:10000"},
		{"strips trailing slash", "http://10.0.0.1:10000/", "http://10.0.0.1:10000"},
		{"strips both", "http://10.0.0.1:10000/?foo=bar", "http://10.0.0.1:10000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := &Gateway{cfg: &Config{RQLiteDSN: tt.dsn}}
			got := gw.rqliteBaseURL()
			if got != tt.want {
				t.Errorf("rqliteBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRqliteExportHandler_MethodNotAllowed(t *testing.T) {
	gw := &Gateway{cfg: &Config{RQLiteDSN: "http://localhost:5001"}}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/rqlite/export", nil)
		rr := httptest.NewRecorder()
		gw.rqliteExportHandler(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: got status %d, want %d", method, rr.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestRqliteExportHandler_Success(t *testing.T) {
	backupData := "fake-sqlite-binary-data"

	mockRQLite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/db/backup" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(backupData))
	}))
	defer mockRQLite.Close()

	gw := &Gateway{
		cfg:    &Config{RQLiteDSN: mockRQLite.URL},
		logger: newRQLiteTestLogger(),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/rqlite/export", nil)
	rr := httptest.NewRecorder()
	gw.rqliteExportHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}

	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "rqlite-export.db") {
		t.Errorf("Content-Disposition = %q, want to contain 'rqlite-export.db'", cd)
	}

	if body := rr.Body.String(); body != backupData {
		t.Errorf("body = %q, want %q", body, backupData)
	}
}

func TestRqliteExportHandler_RQLiteError(t *testing.T) {
	mockRQLite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("rqlite internal error"))
	}))
	defer mockRQLite.Close()

	gw := &Gateway{
		cfg:    &Config{RQLiteDSN: mockRQLite.URL},
		logger: newRQLiteTestLogger(),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/rqlite/export", nil)
	rr := httptest.NewRecorder()
	gw.rqliteExportHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestRqliteImportHandler_MethodNotAllowed(t *testing.T) {
	gw := &Gateway{cfg: &Config{RQLiteDSN: "http://localhost:5001"}}

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/rqlite/import", nil)
		rr := httptest.NewRecorder()
		gw.rqliteImportHandler(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: got status %d, want %d", method, rr.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestRqliteImportHandler_WrongContentType(t *testing.T) {
	gw := &Gateway{cfg: &Config{RQLiteDSN: "http://localhost:5001"}}

	req := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader("data"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	gw.rqliteImportHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestRqliteImportHandler_Success(t *testing.T) {
	importData := nsbackup.SQLiteMagic + "fake-sqlite-binary-data"
	var receivedBody string

	mockRQLite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/db/load" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", ct)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockRQLite.Close()

	gw := &Gateway{
		cfg:    &Config{RQLiteDSN: mockRQLite.URL},
		logger: newRQLiteTestLogger(),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(importData))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	gw.rqliteImportHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	if receivedBody != importData {
		t.Errorf("RQLite received body %q, want %q", receivedBody, importData)
	}
}

func TestRqliteImportHandler_RQLiteError(t *testing.T) {
	mockRQLite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("load failed"))
	}))
	defer mockRQLite.Close()

	gw := &Gateway{
		cfg:    &Config{RQLiteDSN: mockRQLite.URL},
		logger: newRQLiteTestLogger(),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(nsbackup.SQLiteMagic))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	gw.rqliteImportHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// On a namespace gateway a whole-database export or import is the owner's.
// It needed only db:read / db:write, so a developer could read every key and
// grant, or load a database that makes them the owner.
func TestRqliteWholeDatabase_ownerOnlyOnANamespaceGateway(t *testing.T) {
	reached := 0
	mockRQLite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	}))
	defer mockRQLite.Close()
	gw := &Gateway{cfg: &Config{RQLiteDSN: mockRQLite.URL, ClientNamespace: "anchat"}, logger: newRQLiteTestLogger(), loadGuard: &fakeLoadGuard{}}

	requests := func(role auth.Role) []*http.Request {
		exp := httptest.NewRequest(http.MethodGet, "/v1/rqlite/export", nil)
		imp := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(nsbackup.SQLiteMagic))
		imp.Header.Set("Content-Type", "application/octet-stream")
		out := []*http.Request{}
		for _, r := range []*http.Request{exp, imp} {
			ctx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, "anchat")
			out = append(out, markGrant(r.WithContext(ctx), &auth.Grant{Role: role}))
		}
		return out
	}
	serve := func(r *http.Request) int {
		rr := httptest.NewRecorder()
		if r.Method == http.MethodGet {
			gw.rqliteExportHandler(rr, r)
		} else {
			gw.rqliteImportHandler(rr, r)
		}
		return rr.Code
	}

	for _, role := range []auth.Role{auth.RoleDeveloper, auth.RoleAdmin, auth.RoleRuntime} {
		for _, r := range requests(role) {
			if code := serve(r); code != http.StatusForbidden {
				t.Errorf("%s %s as %s: status %d, want 403", r.Method, r.URL.Path, role, code)
			}
		}
	}
	if reached != 0 {
		t.Fatalf("a refused request reached RQLite %d times", reached)
	}
	for _, r := range requests(auth.RoleOwner) {
		if code := serve(r); code != http.StatusOK {
			t.Errorf("%s %s as the owner: status %d, want 200", r.Method, r.URL.Path, code)
		}
	}
}
