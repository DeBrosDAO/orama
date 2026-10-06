// Package standard is the set of contracts every Orama chain ships in genesis, already stored so
// they exist from height 1 even though upload is closed until upload_sunset_height (plans/open-
// network/track-c-chain.md C9). The wasm files are built from pinned upstream source by build.sh;
// manifest.json records the exact source commit, patch, toolchain and sha256 of each one.
//
// Code ids are the manifest order, starting at 1. Nothing here is loaded from the network.
package standard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

//go:embed manifest.json wasm/*.wasm
var files embed.FS

// Source pins where a contract's code came from.
type Source struct {
	Repo         string `json:"repo"`
	Tag          string `json:"tag"`
	Commit       string `json:"commit"`
	Package      string `json:"package"`
	CrateVersion string `json:"crate_version"`
	Patch        string `json:"patch,omitempty"`
}

// Contract is one manifest entry with its wasm bytes.
type Contract struct {
	Name     string `json:"name"`
	Standard string `json:"standard"`
	Role     string `json:"role"`
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
	Source   Source `json:"source"`
	Wasm     []byte `json:"-"`
	// CodeID is the genesis code id: the 1-based manifest position.
	CodeID uint64 `json:"-"`
}

// Manifest is manifest.json.
type Manifest struct {
	Toolchain struct {
		Rust         string `json:"rust"`
		Target       string `json:"target"`
		WasmOpt      string `json:"wasm_opt"`
		WasmOptFlags string `json:"wasm_opt_flags"`
	} `json:"toolchain"`
	Contracts []Contract `json:"contracts"`
}

// Load reads the manifest and every wasm file, and refuses any file whose sha256 differs from
// the manifest or any manifest entry without a pinned source.
func Load() (Manifest, error) {
	raw, err := files.ReadFile("manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to read the standard contracts manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("failed to parse the standard contracts manifest: %w", err)
	}
	if len(m.Contracts) == 0 {
		return Manifest{}, fmt.Errorf("the standard contracts manifest lists no contracts")
	}
	seen := map[string]struct{}{}
	for i := range m.Contracts {
		c := &m.Contracts[i]
		if _, dup := seen[c.Name]; dup {
			return Manifest{}, fmt.Errorf("standard contract %q is listed twice", c.Name)
		}
		seen[c.Name] = struct{}{}
		if c.Source.Repo == "" || len(c.Source.Commit) != 40 {
			return Manifest{}, fmt.Errorf("standard contract %q has no pinned source commit", c.Name)
		}
		wasm, err := files.ReadFile(c.Artifact)
		if err != nil {
			return Manifest{}, fmt.Errorf("standard contract %q: %w", c.Name, err)
		}
		sum := sha256.Sum256(wasm)
		if got := hex.EncodeToString(sum[:]); got != c.SHA256 {
			return Manifest{}, fmt.Errorf("standard contract %q: %s hashes to %s, manifest pins %s", c.Name, c.Artifact, got, c.SHA256)
		}
		c.Wasm = wasm
		c.CodeID = uint64(i + 1)
	}
	return m, nil
}
