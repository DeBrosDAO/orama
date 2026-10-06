package rqlite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

const (
	testUser = "orama"
	testPass = "0123456789abcdef"
)

// requireBasicAuth answers 401 unless the request carries user:pass, as
// rqlited started with -auth does.
func requireBasicAuth(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// testEndpoint is the Endpoint of an httptest server, with the test
// credentials.
func testEndpoint(t *testing.T, serverURL string) Endpoint {
	t.Helper()
	ep, err := NewEndpoint(strings.TrimPrefix(serverURL, "http://"), testUser, testPass)
	if err != nil {
		t.Fatalf("endpoint for %s: %v", serverURL, err)
	}
	return ep
}

func TestNewEndpoint_happyPath(t *testing.T) {
	ep, err := NewEndpoint("10.0.0.1:10100", testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Host != "10.0.0.1" || ep.Port != 10100 || ep.Username != testUser || ep.Password != testPass {
		t.Fatalf("got %+v", ep)
	}
	if got := ep.BaseURL(); got != "http://10.0.0.1:10100" {
		t.Errorf("BaseURL = %q", got)
	}
	if got := ep.CredentialedURL(); got != "http://orama:"+testPass+"@10.0.0.1:10100" {
		t.Errorf("CredentialedURL = %q", got)
	}
	dsn := ep.SQLDSN(ReadConsistencyWeak)
	for _, want := range []string{"orama:" + testPass + "@10.0.0.1:10100", "disableClusterDiscovery=true", "level=weak"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("SQLDSN %q missing %q", dsn, want)
		}
	}
}

// Formatting an Endpoint (logs, errors) must never print the password.
func TestEndpoint_stringHasNoPassword(t *testing.T) {
	ep, err := NewEndpoint("10.0.0.1:10100", testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	if s := ep.String(); strings.Contains(s, testPass) || s != "http://10.0.0.1:10100" {
		t.Fatalf("String() = %q", s)
	}
}

func TestNewEndpoint_rejects(t *testing.T) {
	cases := map[string]struct{ addr, user, pass string }{
		"empty address":    {"", testUser, testPass},
		"no port":          {"10.0.0.1", testUser, testPass},
		"empty host":       {":10100", testUser, testPass},
		"wildcard v4":      {"0.0.0.0:10100", testUser, testPass},
		"wildcard v6":      {"[::]:10100", testUser, testPass},
		"zero port":        {"10.0.0.1:0", testUser, testPass},
		"port too large":   {"10.0.0.1:70000", testUser, testPass},
		"missing user":     {"10.0.0.1:10100", "", testPass},
		"missing password": {"10.0.0.1:10100", testUser, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewEndpoint(tc.addr, tc.user, tc.pass); err == nil {
				t.Fatalf("NewEndpoint(%q, %q, %q) accepted", tc.addr, tc.user, tc.pass)
			}
		})
	}
}

func TestIndexEndpoint_usesAdvertiseHostAndConfiguredPort(t *testing.T) {
	ep, err := IndexEndpoint(
		&config.DatabaseConfig{RQLitePort: 10100, RQLiteUsername: testUser, RQLitePassword: testPass},
		&config.DiscoveryConfig{HttpAdvAddress: "10.0.0.7:10100"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ep.HostPort() != "10.0.0.7:10100" {
		t.Fatalf("HostPort = %q, want the WireGuard advertise host", ep.HostPort())
	}
}

// The bug this exists for: an empty or wildcard advertise address used to be
// silently turned into 127.0.0.1, where rqlited does not listen.
func TestIndexEndpoint_emptyOrWildcardAdvertiseIsAnError(t *testing.T) {
	db := &config.DatabaseConfig{RQLitePort: 10100, RQLiteUsername: testUser, RQLitePassword: testPass}
	for _, adv := range []string{"", "0.0.0.0:10100", "[::]:10100", ":10100"} {
		if ep, err := IndexEndpoint(db, &config.DiscoveryConfig{HttpAdvAddress: adv}); err == nil {
			t.Errorf("advertise %q resolved to %s", adv, ep)
		}
	}
}

// A current node.yaml names the auth file; one that lost its credentials is
// an error, never read as the pre-authentication shape.
func TestIndexEndpoint_missingCredentialsIsAnError(t *testing.T) {
	_, err := IndexEndpoint(
		&config.DatabaseConfig{RQLitePort: 10100, RQLiteAuthFile: "/opt/orama/.orama/secrets/rqlite-auth.json"},
		&config.DiscoveryConfig{HttpAdvAddress: "10.0.0.7:10100"},
	)
	if err == nil || !strings.Contains(err.Error(), "rqlite_password") {
		t.Fatalf("want an error naming the missing credentials, got %v", err)
	}
}

// 0.122.x wrote no auth file and no credentials and ran the index rqlited
// without -auth. The upgrade to this release reads that node before it stops
// it (quorum, leadership, the raft identity), so such a config is reached
// without credentials — and only such a config.
func TestIndexEndpoint_preAuthenticationConfigHasNoCredentials(t *testing.T) {
	ep, err := IndexEndpoint(
		&config.DatabaseConfig{RQLitePort: 5001},
		&config.DiscoveryConfig{HttpAdvAddress: "10.0.0.7:5001"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ep.HostPort() != "10.0.0.7:5001" || ep.Username != "" || ep.Password != "" {
		t.Fatalf("got %s with user %q", ep, ep.Username)
	}

	half := &config.DatabaseConfig{RQLitePort: 5001, RQLiteUsername: "orama"}
	if _, err := IndexEndpoint(half, &config.DiscoveryConfig{HttpAdvAddress: "10.0.0.7:5001"}); err == nil {
		t.Fatal("a user without a password was read as a pre-authentication config")
	}
}

func TestIndexEndpoint_nilConfig(t *testing.T) {
	if _, err := IndexEndpoint(nil, nil); err == nil {
		t.Fatal("nil config accepted")
	}
}

// endpointFromFile reads the endpoint from a node.yaml anchored at its own
// directory.
func endpointFromFile(path string) (Endpoint, error) {
	return EndpointFromNodeConfig(rootfs.At(filepath.Dir(path)), path)
}

func writeNodeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const renderedNodeYAML = `node:
  id: "node-1"
database:
  rqlite_port: 10100
  rqlite_username: "orama"
  rqlite_password: "0123456789abcdef"
discovery:
  http_adv_address: "10.0.0.3:10100"
  raft_adv_address: "10.0.0.3:10101"
some_key_from_a_newer_release: true
`

func TestEndpointFromNodeConfig_happyPath(t *testing.T) {
	ep, err := endpointFromFile(writeNodeYAML(t, renderedNodeYAML))
	if err != nil {
		t.Fatal(err)
	}
	if ep.HostPort() != "10.0.0.3:10100" || ep.Username != testUser || ep.Password != testPass {
		t.Fatalf("got %+v", ep)
	}
}

func TestEndpointFromNodeConfig_missingFile(t *testing.T) {
	_, err := endpointFromFile(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil || !strings.Contains(err.Error(), "absent.yaml") {
		t.Fatalf("want an error naming the file, got %v", err)
	}
}

// node.yaml belongs to the orama user and the CLI reads it as root: a symlink
// planted in its place must not be followed to a root-only file.
func TestEndpointFromNodeConfig_symlinkRefused(t *testing.T) {
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, ".orama", "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := writeNodeYAML(t, renderedNodeYAML)
	path := filepath.Join(anchor, ".orama", "configs", "node.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if ep, err := EndpointFromNodeConfig(rootfs.At(anchor), path); !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("resolved %s through a symlink, err = %v", ep, err)
	}
	if _, err := JoinAddressFromNodeConfig(rootfs.At(anchor), path); !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("join address read through a symlink, err = %v", err)
	}
}

func TestEndpointFromNodeConfig_missingAddressOrCredentials(t *testing.T) {
	cases := map[string]string{
		"no advertise address": strings.Replace(renderedNodeYAML, `  http_adv_address: "10.0.0.3:10100"`+"\n", "", 1),
		"wildcard advertise":   strings.Replace(renderedNodeYAML, `"10.0.0.3:10100"`, `"0.0.0.0:10100"`, 1),
		"no password":          strings.Replace(renderedNodeYAML, `  rqlite_password: "0123456789abcdef"`+"\n", "", 1),
		"not yaml":             "{{{",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if ep, err := endpointFromFile(writeNodeYAML(t, body)); err == nil {
				t.Fatalf("resolved %s", ep)
			}
		})
	}
}

func TestEndpointFromDSN(t *testing.T) {
	t.Run("credentials in the DSN win", func(t *testing.T) {
		ep, err := EndpointFromDSN("http://orama:"+testPass+"@10.0.0.4:10200?level=weak", "x", "y")
		if err != nil {
			t.Fatal(err)
		}
		if ep.HostPort() != "10.0.0.4:10200" || ep.Username != testUser || ep.Password != testPass {
			t.Fatalf("got %+v", ep)
		}
	})
	t.Run("explicit credentials fill a bare DSN", func(t *testing.T) {
		ep, err := EndpointFromDSN("http://10.0.0.4:10200", testUser, testPass)
		if err != nil {
			t.Fatal(err)
		}
		if ep.Username != testUser || ep.Password != testPass {
			t.Fatalf("got %+v", ep)
		}
	})
	t.Run("errors never carry the password", func(t *testing.T) {
		_, err := EndpointFromDSN("http://orama:"+testPass+"@:10200", "", "")
		if err == nil {
			t.Fatal("empty host accepted")
		}
		if strings.Contains(err.Error(), testPass) {
			t.Fatalf("error leaks the password: %v", err)
		}
	})
	for _, dsn := range []string{"", "not a url", "http://0.0.0.0:10100"} {
		if _, err := EndpointFromDSN(dsn, testUser, testPass); err == nil {
			t.Errorf("EndpointFromDSN(%q) accepted", dsn)
		}
	}
}

// Admin calls through an Endpoint authenticate: rqlited with -auth answers 401
// otherwise, which callers used to read as "rqlite is down".
func TestEndpointAdmin_sendsCredentials(t *testing.T) {
	srv := httptest.NewServer(requireBasicAuth(testUser, testPass, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"store":{"raft":{"state":"Leader"}}}`))
	})))
	defer srv.Close()

	status, err := testEndpoint(t, srv.URL).Admin().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Store.Raft.State != "Leader" {
		t.Fatalf("state %q", status.Store.Raft.State)
	}

	wrong := testEndpoint(t, srv.URL)
	wrong.Password = "wrong"
	if _, err := wrong.Admin().Status(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want a 401 with wrong credentials, got %v", err)
	}
}

// rqlited is plain HTTP on the WireGuard overlay. An https DSN would be
// silently downgraded by every URL an Endpoint builds, so it is refused.
func TestEndpointFromDSN_refusesNonHTTPScheme(t *testing.T) {
	for _, dsn := range []string{"https://10.0.0.4:10200", "https://orama:" + testPass + "@10.0.0.4:10200", "ftp://10.0.0.4:10200"} {
		_, err := EndpointFromDSN(dsn, testUser, testPass)
		if err == nil || !strings.Contains(err.Error(), "scheme") {
			t.Errorf("EndpointFromDSN(%q) = %v, want a scheme error", RedactDSN(dsn), err)
		}
		if err != nil && strings.Contains(err.Error(), testPass) {
			t.Errorf("error leaks the password: %v", err)
		}
	}
}
