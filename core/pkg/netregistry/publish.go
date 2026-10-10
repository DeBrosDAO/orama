package netregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PublishInput is what a chain deploy knows about the network it just made.
type PublishInput struct {
	// Dir is the repository's networks directory.
	Dir string
	// Name is the network's name.
	Name string
	// ChainID and Genesis are the chain that was just built.
	ChainID string
	Genesis []byte
	// ReleaseRoot is release-root.json. Empty keeps the one already published.
	ReleaseRoot []byte
	// The fields below override the published manifest when set. A first
	// publish of a network must set all of them.
	Seeds       []string
	Channel     string
	MinVersion  string
	ReleaseRepo string
	Faucet      *bool
}

// ErrGenesisChange says a publish would change the genesis of a chain id that
// is already published.
var ErrGenesisChange = errors.New("the chain id is already published with a different genesis")

// Publish writes networks/<name>/ for a chain: genesis.json, release-root.json
// and, last, manifest.json, so a reader that finds a manifest finds the files it
// names. It refuses to give an already published chain id a different genesis:
// every reset of a network gets a new chain id.
func Publish(in PublishInput) (*Manifest, error) {
	dir := filepath.Join(in.Dir, in.Name)
	prev, err := readPublished(dir)
	if err != nil {
		return nil, err
	}
	if err := checkGenesisChainID(in.Genesis, in.ChainID); err != nil {
		return nil, err
	}
	root, err := publishedRoot(dir, in.ReleaseRoot)
	if err != nil {
		return nil, err
	}
	m := merge(prev, in)
	m.GenesisSHA256 = Digest(in.Genesis)
	m.ReleaseRootSHA256 = Digest(root)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if prev != nil && prev.ChainID == m.ChainID && prev.GenesisSHA256 != m.GenesisSHA256 {
		return nil, fmt.Errorf("%w: chain %s has genesis sha256 %s, the new genesis is %s; a reset needs a new chain id",
			ErrGenesisChange, m.ChainID, prev.GenesisSHA256, m.GenesisSHA256)
	}
	manifest, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{GenesisFile, in.Genesis}, {ReleaseRootFile, root}, {ManifestFile, manifest}} {
		if err := writeFileAtomic(filepath.Join(dir, f.name), f.data); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// readPublished returns the manifest already in dir, or nil when there is none.
func readPublished(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the published manifest: %w", err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("the published manifest in %s is invalid: %w", dir, err)
	}
	return m, nil
}

func publishedRoot(dir string, given []byte) ([]byte, error) {
	if len(given) > 0 {
		return given, nil
	}
	root, err := os.ReadFile(filepath.Join(dir, ReleaseRootFile))
	if err != nil {
		return nil, fmt.Errorf("no release root given and none published in %s: %w", dir, err)
	}
	return root, nil
}

// merge builds the new manifest from the published one, with in's overrides.
func merge(prev *Manifest, in PublishInput) *Manifest {
	m := &Manifest{}
	if prev != nil {
		*m = *prev
	}
	m.Name, m.ChainID = in.Name, in.ChainID
	if in.Seeds != nil {
		m.Seeds = in.Seeds
	}
	if in.Channel != "" {
		m.Channel = in.Channel
	}
	if in.MinVersion != "" {
		m.MinVersion = in.MinVersion
	}
	if in.ReleaseRepo != "" {
		m.ReleaseRepo = in.ReleaseRepo
	}
	if in.Faucet != nil {
		m.Faucet = *in.Faucet
	}
	return m
}

// checkGenesisChainID refuses a genesis that is not for chainID.
func checkGenesisChainID(genesis []byte, chainID string) error {
	var doc struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(genesis, &doc); err != nil {
		return fmt.Errorf("the genesis is not JSON: %w", err)
	}
	if doc.ChainID != chainID {
		return fmt.Errorf("the genesis is for chain %q, not %q", doc.ChainID, chainID)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary file for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set the mode of %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
