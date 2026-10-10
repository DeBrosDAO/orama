package releaseverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/netguard"
)

// A release repository is a static directory served over HTTPS:
//
//	<base>/timestamp.json  <base>/snapshot.json  <base>/targets.json
//	<base>/<N>.root.json   every version of the root (rotate.go)
//	<base>/targets/<path>  the files the targets metadata names
//
// Nothing fetched is trusted until CheckFile has verified it against the
// adopted root, so the transport adds privacy and nothing else; it is still
// HTTPS, and plain HTTP only to this machine, so a test needs no certificate.
const (
	// targetsDir is where a repository serves its target files.
	targetsDir = "targets"
	// metadataTimeout bounds one metadata request.
	metadataTimeout = 30 * time.Second
	// targetTimeout bounds one archive download.
	targetTimeout = 30 * time.Minute
	// maxTargetBytes bounds an archive. A real one is a few hundred megabytes.
	maxTargetBytes = 4 << 30
	// maxRedirects bounds the redirects one request follows.
	maxRedirects = 5
	// metadataFilePerm: the fetched copy is private to the fetching process.
	metadataFilePerm = 0o600
	// dialTimeout and dialKeepAlive are the default transport's own.
	dialTimeout   = 30 * time.Second
	dialKeepAlive = 30 * time.Second
)

// ErrNotFound is a file the release repository answered 404 for. A root
// update reads it as "there is no newer root".
var ErrNotFound = errors.New("not found in the release repository")

// Repository is a release repository at BaseURL.
type Repository struct {
	BaseURL string
	// Client is the HTTP client to use; nil uses http.DefaultTransport with
	// redirects limited by checkRedirectTarget.
	Client *http.Client
}

// AllowLocalEnv, set to "1" in the environment of a process, lets
// ParseRepositoryURL accept http to, and https to, a loopback or private
// address, in a binary built with the localrepo tag (`orama maint build
// --test-local-release-repo`, which only the fleet e2e suite passes, to serve
// a repository from a node's loopback). A binary built without the tag ignores
// the variable, and the installed units never carry it. A Go test asks with
// AllowLocalRepositories instead and needs neither the variable nor the tag.
const AllowLocalEnv = "ORAMA_ALLOW_LOCAL_RELEASE_REPO"

// testAllowed is set by AllowLocalRepositories, for the length of a test.
var testAllowed atomic.Bool

func localAllowed() bool { return testAllowed.Load() || localAllowedByEnv() }

// testEnv is the part of testing.TB AllowLocalRepositories needs.
type testEnv interface {
	Helper()
	Cleanup(func())
}

// AllowLocalRepositories lets the test serve its repository from a loopback
// address, until the test ends. It takes a test so that only a test can call it.
func AllowLocalRepositories(t testEnv) {
	t.Helper()
	testAllowed.Store(true)
	t.Cleanup(func() { testAllowed.Store(false) })
}

// ParseRepositoryURL checks a repository URL: https, with a public host, no
// credentials, query or fragment. The repository is fetched by a process that
// runs as root on a cluster's overlay, so a host that names this machine or a
// private network (localhost, a loopback, private, link-local or carrier-grade
// NAT address) is refused: a cluster setting must not turn the agent into a
// way to reach the services only that network can. A name is judged as written
// here; the default client's connections are checked again at connect time
// against the same ranges, so a name that resolves to a private address, and a
// redirect to one, are refused at the socket.
func ParseRepositoryURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("release repository %q is not a URL with a host", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("release repository %q must not carry credentials, a query or a fragment", raw)
	}
	public := publicHost(u.Hostname())
	if u.Scheme != "https" && !plainHTTPAllowed(u.Scheme, public) {
		return nil, fmt.Errorf("release repository %q must be https", raw)
	}
	if !public && !localAllowed() {
		return nil, fmt.Errorf("release repository %q is on this machine or a private network; use the address the release repository is published at", raw)
	}
	return u, nil
}

// checkRedirectTarget refuses a redirect to plain http or to a host
// ParseRepositoryURL would refuse. A redirect may carry a query (a CDN's signed
// URL), which a repository URL may not.
func checkRedirectTarget(u *url.URL) error {
	public := publicHost(u.Hostname())
	if u.Scheme != "https" && !plainHTTPAllowed(u.Scheme, public) {
		return fmt.Errorf("redirect to %s is not https", u.Redacted())
	}
	if !public && !localAllowed() {
		return fmt.Errorf("redirect to %s is to this machine or a private network", u.Redacted())
	}
	return nil
}

// plainHTTPAllowed is plain http to a local host, for a test that asked for one.
func plainHTTPAllowed(scheme string, publicHost bool) bool {
	return scheme == "http" && !publicHost && localAllowed()
}

// publicHost reports whether host, an IP literal or a name, is not this
// machine or a private network (pkg/netguard.Reserved is the one list of those
// ranges).
func publicHost(host string) bool {
	if host == "" {
		return false
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return false
	}
	ip := net.ParseIP(strings.SplitN(host, "%", 2)[0])
	return ip == nil || !netguard.Reserved(ip)
}

// FetchMetadata downloads timestamp.json, snapshot.json and targets.json into
// dir.
func (r Repository) FetchMetadata(ctx context.Context, dir string) error {
	for _, name := range []string{TimestampFile, SnapshotFile, TargetsFile} {
		data, err := r.getMetadata(ctx, name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, metadataFilePerm); err != nil {
			return fmt.Errorf("save %s: %w", name, err)
		}
	}
	return nil
}

func (r Repository) getMetadata(ctx context.Context, name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	resp, err := r.get(ctx, name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s from the release repository: %w", name, err)
	}
	if len(data) > maxMetadataBytes {
		return nil, fmt.Errorf("%s from the release repository is over %d bytes", name, maxMetadataBytes)
	}
	return data, nil
}

// FetchTarget downloads t into dst, which must be empty and positioned at its
// start. It stops at t.Length: the metadata says how long the file is, so a
// repository cannot make the client store more. Whether the bytes are the
// target is for CheckFile to say.
func (r Repository) FetchTarget(ctx context.Context, t Target, dst *os.File) error {
	if t.Length < 0 || t.Length > maxTargetBytes {
		return fmt.Errorf("target %s is %d bytes, outside what a release archive may be", t.Path, t.Length)
	}
	if path.Clean(t.Path) != t.Path || path.IsAbs(t.Path) || strings.HasPrefix(t.Path, "../") {
		return fmt.Errorf("target path %q is not a plain relative path", t.Path)
	}
	ctx, cancel := context.WithTimeout(ctx, targetTimeout)
	defer cancel()
	resp, err := r.get(ctx, targetsDir+"/"+t.Path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	n, err := io.Copy(dst, io.LimitReader(resp.Body, t.Length+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", t.Path, err)
	}
	if n != t.Length {
		return fmt.Errorf("%w: %s is %d bytes, the metadata says %d", ErrTargetHash, t.Path, n, t.Length)
	}
	return nil
}

// get requests rel below the repository's base URL.
func (r Repository) get(ctx context.Context, rel string) (*http.Response, error) {
	base, err := ParseRepositoryURL(r.BaseURL)
	if err != nil {
		return nil, err
	}
	u := base.JoinPath(strings.Split(rel, "/")...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", u, err)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", u, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %s: the release repository answered %s: %w", u, resp.Status, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %s: the release repository answered %s", u, resp.Status)
	}
	return resp, nil
}

func (r Repository) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Transport: repositoryTransport(), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		return checkRedirectTarget(req.URL)
	}}
}

// repositoryTransport is the default transport with every connection checked
// against the reserved ranges once its address is resolved (unless a test asked
// for a local repository).
func repositoryTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive, Control: func(network, address string, c syscall.RawConn) error {
		if localAllowed() {
			return nil
		}
		return netguard.GuardAddress(network, address, c)
	}}
	t.DialContext = dialer.DialContext
	return t
}
