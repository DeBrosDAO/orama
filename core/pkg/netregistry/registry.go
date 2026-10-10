package netregistry

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
)

// PublishedBaseURL is where networks/<name>/ is served: the website build copies
// the repository's networks/ directory there.
const PublishedBaseURL = "https://orama.network/networks/"

// embeddedDir is the build copy of the repository's networks/ directory. Go
// cannot embed a directory outside the module, so `make -C core sync-networks`
// copies each network's manifest, release root and Tor network file here, and
// TestEmbedded_matchesPublishedNetworks fails when the copy and networks/ differ.
// Genesis files are not embedded: a genesis is large, and it is fetched and
// checked against the manifest's digest.
const embeddedDir = "embedded"

//go:embed all:embedded
var embeddedFS embed.FS

// Network is a manifest together with the release root and the Tor network file it pins.
type Network struct {
	Manifest *Manifest
	// Root is release-root.json, already verified against the manifest.
	Root []byte
	// TorNetwork is tor-network.json, verified against the manifest; nil when the manifest
	// pins none.
	TorNetwork []byte
	// Source is the URL of the manifest.
	Source string
	// Builtin is true for a network built into this binary.
	Builtin bool
}

// ErrNotFound says no network has the name asked for.
var ErrNotFound = errors.New("no such network")

// Registry is a set of networks by name.
type Registry struct {
	nets map[string]*Network
}

// Embedded returns the networks built into this binary.
func Embedded() (*Registry, error) {
	return LoadFS(embeddedFS, embeddedDir)
}

// LoadFS reads a registry laid out like the embedded one (dir/<name>/manifest.json
// and release-root.json) and marks its networks built in.
func LoadFS(fsys fs.FS, dir string) (*Registry, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read the embedded network registry: %w", err)
	}
	r := &Registry{nets: map[string]*Network{}}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := loadNetworkDir(fsys, path.Join(dir, e.Name()), e.Name())
		if err != nil {
			return nil, fmt.Errorf("embedded network %q: %w", e.Name(), err)
		}
		n.Builtin = true
		n.Source = PublishedBaseURL + n.Manifest.Name + "/" + ManifestFile
		r.nets[n.Manifest.Name] = n
	}
	return r, nil
}

// loadNetworkDir reads dir/manifest.json and dir/release-root.json, parses the
// manifest and checks that it names wantName and that the root is the pinned one. A manifest
// that pins a Tor network file needs dir/tor-network.json, checked the same way.
func loadNetworkDir(fsys fs.FS, dir, wantName string) (*Network, error) {
	manifestData, err := fs.ReadFile(fsys, path.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFile, err)
	}
	m, err := ParseManifest(manifestData)
	if err != nil {
		return nil, err
	}
	if m.Name != wantName {
		return nil, fmt.Errorf("the manifest names network %q but sits in %q", m.Name, wantName)
	}
	root, err := fs.ReadFile(fsys, path.Join(dir, ReleaseRootFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ReleaseRootFile, err)
	}
	if err := m.VerifyRoot(root); err != nil {
		return nil, err
	}
	n := &Network{Manifest: m, Root: root}
	if m.TorNetworkSHA256 == "" {
		return n, nil
	}
	if n.TorNetwork, err = fs.ReadFile(fsys, path.Join(dir, TorNetworkFile)); err != nil {
		return nil, fmt.Errorf("the manifest pins a Tor network file and reading it failed: %w", err)
	}
	if err := m.VerifyTorNetwork(n.TorNetwork); err != nil {
		return nil, err
	}
	return n, nil
}

// Get returns the network called name.
func (r *Registry) Get(name string) (*Network, error) {
	if n, ok := r.nets[name]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("%w %q (known: %s)", ErrNotFound, name, joinNames(r.Names()))
}

// Names returns the network names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.nets))
	for name := range r.nets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Merge returns a registry of r's networks and extra's. A name in both is an
// error: a custom network never shadows a built-in one.
func (r *Registry) Merge(extra *Registry) (*Registry, error) {
	out := &Registry{nets: map[string]*Network{}}
	for name, n := range r.nets {
		out.nets[name] = n
	}
	for name, n := range extra.nets {
		if _, taken := out.nets[name]; taken {
			return nil, fmt.Errorf("network name %q is used twice", name)
		}
		out.nets[name] = n
	}
	return out, nil
}

// GenesisURL is where this network's genesis.json is served: beside its manifest.
func (n *Network) GenesisURL() (string, error) {
	return siblingURL(n.Source, GenesisFile)
}

// siblingURL returns the URL of file in the directory of manifestURL.
func siblingURL(manifestURL, file string) (string, error) {
	u, err := url.Parse(manifestURL)
	if err != nil {
		return "", fmt.Errorf("parse manifest URL %q: %w", manifestURL, err)
	}
	ref, err := url.Parse(file)
	if err != nil {
		return "", fmt.Errorf("parse file name %q: %w", file, err)
	}
	return u.ResolveReference(ref).String(), nil
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
