package rqlite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The remote-curl commands are shell run on a node over SSH. These tests run
// them for real, locally, against a node.yaml in a temp dir and an httptest
// server that — like rqlited with -auth — answers 401 without credentials.

func shellTestServer(t *testing.T) (port int, seenPaths *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(requireBasicAuth(testUser, testPass, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		_, _ = w.Write([]byte(`{"store":{"raft":{"state":"Leader"}}}`))
	})))
	t.Cleanup(srv.Close)
	p, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	return p, &paths
}

func shellNodeYAML(t *testing.T, adv string, port int, user, pass string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("database:\n")
	b.WriteString("  rqlite_port: " + strconv.Itoa(port) + "\n")
	if user != "" {
		b.WriteString(`  rqlite_username: "` + user + `"` + "\n")
	}
	if pass != "" {
		b.WriteString(`  rqlite_password: "` + pass + `"` + "\n")
	}
	b.WriteString("discovery:\n")
	if adv != "" {
		b.WriteString(`  http_adv_address: "` + adv + `"` + "\n")
	}
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runShell(t *testing.T, script string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	cmd := exec.Command("sh", "-c", script)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Happy path: host from http_adv_address (port stripped), port from
// rqlite_port, credentials from node.yaml, path and query preserved.
func TestIndexShellCurl_authenticatesAgainstTheAdvertisedAddress(t *testing.T) {
	port, paths := shellTestServer(t)
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, testUser, testPass)

	out, stderr, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status?pretty&x=1")+" | grep -o Leader")
	if err != nil {
		t.Fatalf("command failed: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(out) != "Leader" {
		t.Fatalf("stdout %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/status?pretty&x=1" {
		t.Fatalf("server saw %v", *paths)
	}
}

// The password must never be on curl's command line, where ps shows it.
func TestIndexShellCurl_passwordNotInArgv(t *testing.T) {
	script := indexShellCurl("/nonexistent/node.yaml", "", "-sf", "/status")
	if strings.Contains(script, testPass) {
		t.Fatal("password embedded in the script")
	}
	if !strings.Contains(script, "curl -K -") {
		t.Fatalf("credentials are not passed on stdin: %s", script)
	}
}

func TestIndexShellCurl_wrongCredentialsFail(t *testing.T) {
	port, _ := shellTestServer(t)
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, testUser, "wrong")
	if _, _, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status")); err == nil {
		t.Fatal("a 401 was reported as success")
	}
}

// Missing address or credentials fail loudly instead of curling localhost
// unauthenticated.
func TestIndexShellCurl_missingConfigFails(t *testing.T) {
	port, paths := shellTestServer(t)
	cases := map[string]string{
		"no advertise address": shellNodeYAML(t, "", port, testUser, testPass),
		"no password":          shellNodeYAML(t, "127.0.0.1:1", port, testUser, ""),
		"no config file":       filepath.Join(t.TempDir(), "absent.yaml"),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, stderr, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status"))
			if err == nil {
				t.Fatal("succeeded without an address or credentials")
			}
			if !strings.Contains(stderr, "missing from") {
				t.Errorf("stderr %q does not explain the failure", stderr)
			}
		})
	}
	if len(*paths) != 0 {
		t.Fatalf("requests were sent: %v", *paths)
	}
}

// A namespace instance is addressed by a shell expression (its HTTP_ADDR) and
// uses the cluster credentials from node.yaml.
func TestInstanceShellCurl_usesGivenAddress(t *testing.T) {
	port, paths := shellTestServer(t)
	cfg := shellNodeYAML(t, "", 1, testUser, testPass)
	script := "ADDR=127.0.0.1:" + strconv.Itoa(port) + "; " + instanceShellCurl(cfg, "", "$ADDR", "-sf", "/readyz")
	if _, stderr, err := runShell(t, script); err != nil {
		t.Fatalf("command failed: %v (stderr %q)", err, stderr)
	}
	if len(*paths) != 1 || (*paths)[0] != "/readyz" {
		t.Fatalf("server saw %v", *paths)
	}
}

// Inside a double-quoted curl config value, backslash is an escape character.
// An unescaped \ or " in a credential changes what curl sends or breaks the
// config line.
func TestShellCurlConfigQuote_escapesBackslashAndQuote(t *testing.T) {
	for in, want := range map[string]string{
		"0123456789abcdef": "0123456789abcdef",
		`pa"ss`:            `pa\"ss`,
		`pa\ss`:            `pa\\ss`,
		`a\"b`:             `a\\\"b`,
	} {
		cmd := exec.Command("sh", "-c", shellCurlConfigQuote+`_rq_q "$1"`, "sh", in)
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(got) != want {
			t.Errorf("_rq_q(%q) = %q, want %q", in, got, want)
		}
	}
}

// End to end: a password with a backslash reaches rqlite unchanged.
func TestIndexShellCurl_passwordWithBackslash(t *testing.T) {
	const pass = `se\cret`
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got, _ = r.BasicAuth()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, testUser, pass)
	if _, stderr, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status")); err != nil {
		t.Fatalf("command failed: %v (stderr %q)", err, stderr)
	}
	if got != pass {
		t.Fatalf("server received password %q, want %q", got, pass)
	}
}

// A node.yaml with no auth file and no credentials is a 0.122.x node, whose
// index rqlited runs without -auth: it is called without credentials, so the
// rolling upgrade can read its raft state before upgrading it.
func TestIndexShellCurl_preAuthenticationNodeIsCalledWithoutCredentials(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, sawAuth = r.BasicAuth()
		_, _ = w.Write([]byte(`{"store":{"raft":{"state":"Follower"}}}`))
	}))
	t.Cleanup(srv.Close)
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, "", "")
	out, stderr, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status")+" | grep -o Follower")
	if err != nil {
		t.Fatalf("command failed: %v (stderr %q)", err, stderr)
	}
	if strings.TrimSpace(out) != "Follower" || sawAuth {
		t.Fatalf("stdout %q, credentials sent: %v", out, sawAuth)
	}
}

// Pointed at an rqlited that enforces -auth, the pre-authentication shape
// still fails: it is not a way around the credentials.
func TestIndexShellCurl_preAuthenticationShapeDoesNotPassAuth(t *testing.T) {
	port, _ := shellTestServer(t)
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, "", "")
	if _, _, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status")); err == nil {
		t.Fatal("an unauthenticated call succeeded against an rqlited that requires credentials")
	}
}

// A current node.yaml names its auth file; losing the credentials from it is
// an error, not a pre-authentication node.
func TestIndexShellCurl_authFileWithoutCredentialsFails(t *testing.T) {
	port, paths := shellTestServer(t)
	cfg := shellNodeYAML(t, "127.0.0.1:1", port, "", "")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("database_extra:\n  rqlite_auth_file: \"/opt/orama/.orama/secrets/rqlite-auth.json\"\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, stderr, err := runShell(t, indexShellCurl(cfg, "", "-sf", "/status"))
	if err == nil || !strings.Contains(stderr, "missing from") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if len(*paths) != 0 {
		t.Fatalf("requests were sent: %v", *paths)
	}
}
