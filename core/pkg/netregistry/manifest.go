// Package netregistry is the list of networks the CLI knows: a manifest names a
// network's chain, the digest of its genesis, the seeds to join through and the
// release root its software is verified against.
//
// A network is trusted because its manifest was built into the CLI the operator
// installed, or because the operator confirmed a manifest they added by URL.
// Nothing a manifest points at is trusted on its own: the genesis and the
// release root are fetched or read, then checked against the digests the
// manifest carries.
package netregistry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// File names of a published network, the same in networks/<name>/, in the
// embedded registry, on the website under /networks/<name>/ and in a custom
// network's store.
const (
	ManifestFile    = "manifest.json"
	GenesisFile     = "genesis.json"
	ReleaseRootFile = "release-root.json"
)

// Channels a manifest may name: the nightly feed, the main line, and a dev
// branch as dev/<branch>.
const (
	ChannelNightly   = "nightly"
	ChannelMain      = "main"
	channelDevPrefix = "dev/"
)

// maxManifestBytes bounds a manifest. A real one is under 2 KiB; the bound keeps
// a hostile URL from filling memory before validation runs.
const maxManifestBytes = 64 * 1024

var (
	nameRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	chainIDRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$`)
	sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	semverRE    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	devBranchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	hostLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ErrGenesisMismatch and ErrRootMismatch say a file is not the one the manifest
// pins. Callers match them to refuse to continue.
var (
	ErrGenesisMismatch = errors.New("genesis does not match the manifest")
	ErrRootMismatch    = errors.New("release root does not match the manifest")
)

// Manifest is one network's published description.
type Manifest struct {
	// Name is the network's short name: stagenet, testnet, or an operator's own.
	Name string `json:"name"`
	// ChainID is the chain's id. A reset of the chain gets a new one.
	ChainID string `json:"chain_id"`
	// GenesisSHA256 is the SHA-256 of the genesis.json that goes with ChainID,
	// in lowercase hex.
	GenesisSHA256 string `json:"genesis_sha256"`
	// Seeds are DNS names of nodes a joiner reaches the chain through. Never IPs:
	// a seed is replaced without a new manifest.
	Seeds []string `json:"seeds"`
	// Channel is the release channel the network's nodes run.
	Channel string `json:"channel"`
	// MinVersion is the oldest orama version that may join, as X.Y.Z.
	MinVersion string `json:"min_version"`
	// ReleaseRepo is the https base URL of the network's release repository.
	ReleaseRepo string `json:"release_repo"`
	// ReleaseRootSHA256 is the SHA-256 of release-root.json, the TUF root the
	// network's releases are verified against, in lowercase hex.
	ReleaseRootSHA256 string `json:"release_root_sha256"`
	// Faucet says the network funds new operators from a faucet.
	Faucet bool `json:"faucet"`
}

// ParseManifest reads a manifest strictly: a field it does not know, a second
// document after the first, or any invalid value is an error.
func ParseManifest(data []byte) (*Manifest, error) {
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest is %d bytes; a manifest is under %d", len(data), maxManifestBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("parse manifest: trailing data after the manifest object")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks every field. The first problem is returned, naming the field.
func (m *Manifest) Validate() error {
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("manifest name %q: use 1-32 characters of a-z, 0-9 and -, starting with a letter", m.Name)
	}
	if !chainIDRE.MatchString(m.ChainID) {
		return fmt.Errorf("manifest chain_id %q: use 1-48 characters of a-z, 0-9 and -, starting and ending with a letter or digit", m.ChainID)
	}
	if !sha256HexRE.MatchString(m.GenesisSHA256) {
		return fmt.Errorf("manifest genesis_sha256 %q is not 64 lowercase hex characters", m.GenesisSHA256)
	}
	if err := validateSeeds(m.Seeds); err != nil {
		return err
	}
	if err := validateChannel(m.Channel); err != nil {
		return err
	}
	if !semverRE.MatchString(m.MinVersion) {
		return fmt.Errorf("manifest min_version %q is not X.Y.Z", m.MinVersion)
	}
	if err := validateHTTPSURL("release_repo", m.ReleaseRepo); err != nil {
		return err
	}
	if !sha256HexRE.MatchString(m.ReleaseRootSHA256) {
		return fmt.Errorf("manifest release_root_sha256 %q is not 64 lowercase hex characters", m.ReleaseRootSHA256)
	}
	return nil
}

func validateSeeds(seeds []string) error {
	if len(seeds) == 0 {
		return errors.New("manifest seeds: at least one seed is required")
	}
	seen := map[string]bool{}
	for _, s := range seeds {
		if err := validateSeedName(s); err != nil {
			return err
		}
		if seen[s] {
			return fmt.Errorf("manifest seeds: %q is listed twice", s)
		}
		seen[s] = true
	}
	return nil
}

func validateSeedName(s string) error {
	if net.ParseIP(s) != nil {
		return fmt.Errorf("manifest seed %q is an IP address; seeds are DNS names", s)
	}
	labels := strings.Split(s, ".")
	if len(s) > 253 || len(labels) < 2 {
		return fmt.Errorf("manifest seed %q is not a DNS name with at least two labels", s)
	}
	for _, l := range labels {
		if !hostLabelRE.MatchString(l) {
			return fmt.Errorf("manifest seed %q: label %q is not a lowercase DNS label", s, l)
		}
	}
	return nil
}

func validateChannel(c string) error {
	if c == ChannelNightly || c == ChannelMain {
		return nil
	}
	if branch, ok := strings.CutPrefix(c, channelDevPrefix); ok && devBranchRE.MatchString(branch) {
		return nil
	}
	return fmt.Errorf("manifest channel %q is not %s, %s or %s<branch>", c, ChannelNightly, ChannelMain, channelDevPrefix)
}

func validateHTTPSURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("manifest %s %q: %w", field, raw, err)
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("manifest %s %q must be an https:// URL", field, raw)
	case u.Hostname() == "":
		return fmt.Errorf("manifest %s %q has no host", field, raw)
	case u.User != nil:
		return fmt.Errorf("manifest %s %q must not carry a user or password", field, raw)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("manifest %s %q must not carry a query or fragment", field, raw)
	}
	return nil
}

// Digest returns the lowercase hex SHA-256 of data.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// VerifyGenesis checks genesis against the manifest's genesis_sha256.
func (m *Manifest) VerifyGenesis(genesis []byte) error {
	if got := Digest(genesis); got != m.GenesisSHA256 {
		return fmt.Errorf("%w: network %s (chain %s) pins sha256 %s, the file is %s",
			ErrGenesisMismatch, m.Name, m.ChainID, m.GenesisSHA256, got)
	}
	return nil
}

// VerifyRoot checks root against the manifest's release_root_sha256.
func (m *Manifest) VerifyRoot(root []byte) error {
	if got := Digest(root); got != m.ReleaseRootSHA256 {
		return fmt.Errorf("%w: network %s pins sha256 %s, the file is %s",
			ErrRootMismatch, m.Name, m.ReleaseRootSHA256, got)
	}
	return nil
}

// Marshal renders the manifest the way it is published: indented, one field
// per line, a trailing newline, so two publishes of the same facts are
// byte-identical.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(out, '\n'), nil
}
