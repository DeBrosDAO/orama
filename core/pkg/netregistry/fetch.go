package netregistry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"
)

const (
	// fetchTimeout bounds one request, headers and body.
	fetchTimeout = 60 * time.Second
	// maxRedirects bounds the redirects one request follows.
	maxRedirects = 5
	// maxRootBytes bounds a release root. A TUF root is a few KiB.
	maxRootBytes = 1 << 20
	// maxGenesisBytes bounds a genesis. One that carries the standard contracts
	// is a few MiB.
	maxGenesisBytes = 64 << 20
)

// ErrGenesisUnpublished says the network's manifest is published but its
// genesis is not. A network's genesis is written when the chain starts, so a
// manifest can be out before it.
var ErrGenesisUnpublished = errors.New("the network has no genesis published yet")

// NewHTTPClient returns the client the registry fetches with: bounded in time,
// and refusing a redirect to anything but https, so an https URL cannot be
// downgraded on the way.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("refusing a redirect to %s: only https is followed", req.URL.Redacted())
			}
			return nil
		},
	}
}

// statusError is a response that was not 200.
type statusError struct {
	url    string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d", e.url, e.status)
}

// get fetches rawURL over https and returns at most limit bytes; a longer body
// is an error rather than a truncated answer.
func get(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("%q is not an https:// URL; networks are fetched over https only", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("fetch %s: the answer came over %s, not https", rawURL, resp.Request.URL.Scheme)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{url: rawURL, status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes allowed", rawURL, limit)
	}
	return body, nil
}

// FetchNetwork fetches the manifest at manifestURL and the release root beside
// it, and checks the root against the manifest. The URL must end in
// /manifest.json, because the root and genesis are named relative to it.
func FetchNetwork(ctx context.Context, client *http.Client, manifestURL string) (*Network, error) {
	u, err := url.Parse(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("parse manifest URL %q: %w", manifestURL, err)
	}
	if path.Base(u.Path) != ManifestFile {
		return nil, fmt.Errorf("manifest URL %q must end in /%s: the genesis and release root are fetched beside it", manifestURL, ManifestFile)
	}
	manifestData, err := get(ctx, client, manifestURL, maxManifestBytes)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestURL, err)
	}
	rootURL, err := siblingURL(manifestURL, ReleaseRootFile)
	if err != nil {
		return nil, err
	}
	root, err := get(ctx, client, rootURL, maxRootBytes)
	if err != nil {
		return nil, fmt.Errorf("the manifest of network %s names a release root that cannot be fetched: %w", m.Name, err)
	}
	if err := m.VerifyRoot(root); err != nil {
		return nil, fmt.Errorf("%s: %w", rootURL, err)
	}
	return &Network{Manifest: m, Root: root, Source: manifestURL}, nil
}

// FetchGenesis fetches this network's genesis and checks it against the
// manifest. A genesis that is not there yet is ErrGenesisUnpublished; a network
// that is only announced has none to fetch, ErrNotCreated.
func (n *Network) FetchGenesis(ctx context.Context, client *http.Client) ([]byte, error) {
	if err := n.Manifest.CheckCreated(); err != nil {
		return nil, err
	}
	genesisURL, err := n.GenesisURL()
	if err != nil {
		return nil, err
	}
	genesis, err := get(ctx, client, genesisURL, maxGenesisBytes)
	var status *statusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil, fmt.Errorf("%w: network %s (chain %s) is listed but %s answers 404; its genesis is published when the chain starts",
			ErrGenesisUnpublished, n.Manifest.Name, n.Manifest.ChainID, genesisURL)
	}
	if err != nil {
		return nil, err
	}
	if err := n.Manifest.VerifyGenesis(genesis); err != nil {
		return nil, fmt.Errorf("%s: %w", genesisURL, err)
	}
	return genesis, nil
}
