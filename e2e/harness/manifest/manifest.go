// Package manifest reads e2e/features/<id>/feature.yaml: what a feature package
// tests, when it runs, and what it needs from the fleet. The coverage gate reads
// covers; the stage runner reads stage, destructive and requires.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// FileName is the manifest file in every feature directory.
const FileName = "feature.yaml"

// Stage bounds: stages.yaml defines stages 1..MaxStage.
const (
	MinStage = 1
	MaxStage = 11
)

// MaxExtraNodes is how many on-demand servers one feature may ask for.
const MaxExtraNodes = 3

// ID prefixes of the coverage universe. A manifest's covers entries map onto
// these; so does every enumerated item and every waiver.
const (
	PrefixCLI    = "cli:"
	PrefixRoute  = "route:"
	PrefixMsg    = "msg:"
	PrefixQuery  = "query:"
	PrefixUnit   = "unit:"
	PrefixConfig = "config:"
	PrefixClaim  = "claim:"
)

// Requires is what a feature needs beyond the three core nodes.
type Requires struct {
	ExtraNodes int  `yaml:"extra_nodes"`
	Probe      bool `yaml:"probe"`
	Chain      bool `yaml:"chain"`
}

// Covers lists the shipped things a feature's tests exercise.
type Covers struct {
	CLI     []string `yaml:"cli"`
	Routes  []string `yaml:"routes"`
	Msgs    []string `yaml:"msgs"`
	Queries []string `yaml:"queries"`
	Units   []string `yaml:"units"`
	Config  []string `yaml:"config"`
	Claims  []string `yaml:"claims"`
}

// Manifest is one feature.yaml.
type Manifest struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Area        string   `yaml:"area"`
	Subtasks    []int    `yaml:"subtasks"`
	Stage       int      `yaml:"stage"`
	Destructive bool     `yaml:"destructive"`
	Requires    Requires `yaml:"requires"`
	Covers      Covers   `yaml:"covers"`

	// Dir is the feature directory's base name, set by the loader.
	Dir string `yaml:"-"`
}

// IDs returns every covers entry as a universe id, sorted.
func (c Covers) IDs() []string {
	var out []string
	add := func(prefix string, entries []string) {
		for _, e := range entries {
			out = append(out, prefix+e)
		}
	}
	add(PrefixCLI, c.CLI)
	add(PrefixRoute, c.Routes)
	add(PrefixMsg, c.Msgs)
	add(PrefixQuery, c.Queries)
	add(PrefixUnit, c.Units)
	add(PrefixConfig, c.Config)
	add(PrefixClaim, c.Claims)
	sort.Strings(out)
	return out
}

// Parse decodes one manifest strictly: unknown keys and trailing documents are
// errors, so a typo ("cover:") cannot silently cover nothing.
func Parse(raw []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest has more than one YAML document")
	}
	return &m, nil
}

// LoadAll reads and validates every features/<dir>/feature.yaml under
// featuresDir, sorted by id. It fails on the first invalid manifest, listing
// every problem of it. Ids are unique because each must equal its directory.
func LoadAll(featuresDir string) ([]Manifest, error) {
	paths, err := filepath.Glob(filepath.Join(featuresDir, "*", FileName))
	if err != nil {
		return nil, fmt.Errorf("failed to list manifests in %s: %w", featuresDir, err)
	}
	var out []Manifest
	for _, path := range paths {
		m, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LoadFile reads and validates one manifest.
func LoadFile(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest %s: %w", path, err)
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Dir = filepath.Base(filepath.Dir(path))
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
