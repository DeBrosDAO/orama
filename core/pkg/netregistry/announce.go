package netregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AnnounceInput is a network that is announced before it exists: what a
// maintainer decides first, so that the creator of the chain and every joiner
// who reads the registry agree on the name, the chain id and the release root.
type AnnounceInput struct {
	// Dir is the repository's networks directory.
	Dir string
	// Name and ChainID are the network's name and the chain id it will have.
	Name    string
	ChainID string
	// ReleaseRoot is release-root.json; the manifest pins its digest.
	ReleaseRoot []byte
	// Channel, MinVersion and ReleaseRepo say where the network's releases come from.
	Channel     string
	MinVersion  string
	ReleaseRepo string
	// Seeds may be empty: the creator then gets the default seeds.
	Seeds []string
	// Faucet says the network will fund new operators from a faucet.
	Faucet bool
}

// ErrAlreadyCreated says a network cannot be announced because its chain exists.
var ErrAlreadyCreated = errors.New("the network is already created")

// Announce writes networks/<name>/ for a network whose chain does not exist yet:
// release-root.json and, last, manifest.json with no genesis digest. It replaces an
// earlier announcement of the same name; it refuses a network that is already
// created, because a reset gets a new chain id from `orama setup --create-network`
// and not a new announcement.
func Announce(in AnnounceInput) (*Manifest, error) {
	dir := filepath.Join(in.Dir, in.Name)
	prev, err := readPublished(dir)
	if err != nil {
		return nil, err
	}
	if prev != nil && !prev.Announced() {
		return nil, fmt.Errorf("%w: %s is published with chain %s and its genesis; a reset of it is made with orama setup --create-network %s --chain-id <new chain id>",
			ErrAlreadyCreated, in.Name, prev.ChainID, in.Name)
	}
	if !json.Valid(in.ReleaseRoot) {
		return nil, errors.New("the release root is not JSON")
	}
	m := &Manifest{
		Name: in.Name, ChainID: in.ChainID, Seeds: in.Seeds, Channel: in.Channel, MinVersion: in.MinVersion,
		ReleaseRepo: in.ReleaseRepo, ReleaseRootSHA256: Digest(in.ReleaseRoot), Faucet: in.Faucet,
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
	}{{ReleaseRootFile, in.ReleaseRoot}, {ManifestFile, manifest}} {
		if err := writeFileAtomic(filepath.Join(dir, f.name), f.data); err != nil {
			return nil, err
		}
	}
	return m, nil
}
