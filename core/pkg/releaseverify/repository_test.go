package releaseverify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveRepo publishes files at the paths of a release repository: metadata at
// the root and archives below targets/.
func serveRepo(t *testing.T, files map[string][]byte, archives map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range archives {
		path := filepath.Join(dir, targetsDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRepository_fetchesMetadataAndAnArchiveThatVerify(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	repo := Repository{BaseURL: serveRepo(t, files, map[string][]byte{stableTarget: []byte("stable archive")})}

	dir := t.TempDir()
	if err := repo.FetchMetadata(context.Background(), dir, []string{"stable"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{TimestampFile, SnapshotFile, TargetsFile, "stable.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not saved: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "nightly.json")); err == nil {
		t.Error("a channel that was not asked for was fetched")
	}

	v, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.CreateTemp(t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	target := v.Targets[stableTarget]
	if err := repo.FetchTarget(context.Background(), target, dst); err != nil {
		t.Fatal(err)
	}
	if err := target.MatchOpen(dst); err != nil {
		t.Fatalf("the downloaded archive is not the target: %v", err)
	}
}

func TestRepository_missingFileIsAnError(t *testing.T) {
	repo := Repository{BaseURL: serveRepo(t, nil, nil)}
	err := repo.FetchMetadata(context.Background(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the repository's 404", err)
	}
}

func TestRepository_aRepositoryCannotMakeTheClientStoreMoreThanTheTargetLength(t *testing.T) {
	repo := Repository{BaseURL: serveRepo(t, nil, map[string][]byte{stableTarget: []byte("far more bytes than the metadata names")})}
	dst, err := os.CreateTemp(t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	err = repo.FetchTarget(context.Background(), Target{Path: stableTarget, Length: 4}, dst)
	if err == nil {
		t.Fatal("a download longer than its target was accepted")
	}
	if info, _ := dst.Stat(); info.Size() > 5 {
		t.Fatalf("%d bytes were stored for a 4-byte target", info.Size())
	}
}

func TestRepository_aTargetPathCannotLeaveTheTargetsDirectory(t *testing.T) {
	repo := Repository{BaseURL: serveRepo(t, nil, nil)}
	dst, err := os.CreateTemp(t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	for _, bad := range []string{"../root.json", "/etc/passwd", "a/../../b", "stable//x"} {
		if err := repo.FetchTarget(context.Background(), Target{Path: bad, Length: 1}, dst); err == nil {
			t.Errorf("target path %q was fetched", bad)
		}
	}
}

func TestParseRepositoryURL(t *testing.T) {
	for _, ok := range []string{
		"https://releases.example.org/tuf", "https://93.184.216.34/tuf", "https://releases.example.org:8443/a/b",
		"https://[2606:4700:4700::1111]/tuf", "https://100.63.255.255/x", "https://172.32.0.1/x",
	} {
		if _, err := ParseRepositoryURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "releases.example.org", "http://releases.example.org", "ftp://x/y", "https://u:p@x/y", "https://x/y?q=1", "https://x/y#f", "file:///etc",
		// This machine and private networks: not a place a release is published.
		"http://127.0.0.1:8080", "https://127.0.0.1/tuf", "https://localhost/x", "https://LOCALHOST./x", "https://repo.localhost/x", "https://[::1]:9/",
		"https://10.0.0.7/tuf", "https://172.16.0.1/x", "https://192.168.1.1/x", "https://169.254.169.254/latest", "https://100.64.0.1/x",
		"https://0.0.0.0/x", "https://[fe80::1]/x", "https://[fd00::1]/x", "https://224.0.0.1/x", "https://[::ffff:127.0.0.1]/x",
		"https://198.18.0.1/x", "https://203.0.113.9/x",
	} {
		if _, err := ParseRepositoryURL(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A test serves its repository from httptest on loopback and asks for it; the
// permission ends with the test.
func TestParseRepositoryURL_aTestCanAskForALocalRepository(t *testing.T) {
	const local = "http://127.0.0.1:8080"
	if _, err := ParseRepositoryURL(local); err == nil {
		t.Fatal("a loopback repository was accepted without being asked for")
	}
	t.Run("asked", func(t *testing.T) {
		AllowLocalRepositories(t)
		for _, ok := range []string{local, "http://localhost/x", "http://[::1]:9/", "https://10.0.0.7/tuf"} {
			if _, err := ParseRepositoryURL(ok); err != nil {
				t.Errorf("%s: %v", ok, err)
			}
		}
		if _, err := ParseRepositoryURL("http://releases.example.org"); err == nil {
			t.Error("plain http to a public host was accepted")
		}
	})
	if _, err := ParseRepositoryURL(local); err == nil {
		t.Fatal("the permission outlived the test that asked")
	}
}

// A repository cannot send the agent, which runs as root on the overlay, to a
// service only that network can reach: not by a redirect either.
func TestRepository_aRedirectToThisMachineOrAPrivateNetworkIsRefused(t *testing.T) {
	redirect := Repository{}.client().CheckRedirect
	remote := []*http.Request{{URL: mustParse(t, "https://releases.example.org/tuf/timestamp.json")}}
	for _, target := range []string{"https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest/meta-data", "https://10.0.0.1:10100/db/query"} {
		err := redirect(&http.Request{URL: mustParse(t, target)}, remote)
		if err == nil || !strings.Contains(err.Error(), "private network") {
			t.Errorf("redirect to %s: err = %v", target, err)
		}
	}
	// A CDN's signed URL carries a query.
	if err := redirect(&http.Request{URL: mustParse(t, "https://cdn.example.net/a?sig=abc")}, remote); err != nil {
		t.Errorf("a redirect with a query: %v", err)
	}
}

func TestRepository_aRedirectToPlainHTTPElsewhereIsRefused(t *testing.T) {
	plain := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://93.184.216.34/timestamp.json", http.StatusFound)
	}))
	plain.StartTLS()
	t.Cleanup(plain.Close)
	AllowLocalRepositories(t)
	repo := Repository{BaseURL: plain.URL, Client: plain.Client()}
	repo.Client.CheckRedirect = Repository{}.client().CheckRedirect
	err := repo.FetchMetadata(context.Background(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("err = %v, want the downgrade refused", err)
	}
}

// A repository on the internet cannot send a root-run client to a service on
// the client's own loopback, where plain HTTP is otherwise allowed.
func TestRepository_aRedirectToLoopbackFromAnotherHostIsRefused(t *testing.T) {
	redirect := Repository{}.client().CheckRedirect
	remote := []*http.Request{{URL: mustParse(t, "https://releases.example.org/tuf/timestamp.json")}}
	err := redirect(&http.Request{URL: mustParse(t, "http://127.0.0.1:9/timestamp.json")}, remote)
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("err = %v", err)
	}
	if err := redirect(&http.Request{URL: mustParse(t, "https://cdn.example.net/timestamp.json")}, remote); err != nil {
		t.Fatalf("a redirect to another https host: %v", err)
	}
	AllowLocalRepositories(t)
	loopback := []*http.Request{{URL: mustParse(t, "http://127.0.0.1:8080/timestamp.json")}}
	if err := redirect(&http.Request{URL: mustParse(t, "http://127.0.0.1:8080/other.json")}, loopback); err != nil {
		t.Fatalf("a loopback repository redirecting inside loopback: %v", err)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A name that resolves to the machine's own loopback passes the URL check (it is
// judged as written) and is refused at the socket.
func TestRepository_aNameThatResolvesToThisMachineIsRefusedAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	// The server is on 127.0.0.1; "127.0.0.1.nip.io"-style names are the shape of
	// the attack, but the test needs no DNS: the transport is exercised through
	// its dialer on the loopback address the name would resolve to.
	conn, err := repositoryTransport().DialContext(context.Background(), "tcp", srv.Listener.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("the default transport connected to the loopback")
	}
	if !strings.Contains(err.Error(), "internal network") {
		t.Fatalf("err = %v", err)
	}
	AllowLocalRepositories(t)
	conn, err = repositoryTransport().DialContext(context.Background(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("a test that asked for a local repository: %v", err)
	}
	conn.Close()
}
