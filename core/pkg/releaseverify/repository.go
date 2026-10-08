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
	"time"
)

// A release repository is a static directory served over HTTPS:
//
//	<base>/timestamp.json  <base>/snapshot.json  <base>/targets.json
//	<base>/<role>.json     one per delegated role (a channel)
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
)

// Repository is a release repository at BaseURL.
type Repository struct {
	BaseURL string
	// Client is the HTTP client to use; nil uses http.DefaultTransport with
	// redirects limited by allowedScheme.
	Client *http.Client
}

// ParseRepositoryURL checks a repository URL: https, or http to a loopback
// address, with a host and no credentials, query or fragment.
func ParseRepositoryURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("release repository %q is not a URL with a host", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("release repository %q must not carry credentials, a query or a fragment", raw)
	}
	if !allowedScheme(u) {
		return nil, fmt.Errorf("release repository %q must be https (http is allowed only to a loopback address)", raw)
	}
	return u, nil
}

func allowedScheme(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() || u.Hostname() == "localhost"
}

// FetchMetadata downloads timestamp.json, snapshot.json, targets.json and
// each of roles' <role>.json into dir.
func (r Repository) FetchMetadata(ctx context.Context, dir string, roles []string) error {
	names := []string{TimestampFile, SnapshotFile, TargetsFile}
	for _, role := range roles {
		if err := validRoleName(role); err != nil {
			return err
		}
		names = append(names, role+".json")
	}
	for _, name := range names {
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
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !allowedScheme(req.URL) {
			return fmt.Errorf("redirect to %s is not https", req.URL.Redacted())
		}
		return nil
	}}
}
