// Package artifacts gathers what explains a run after the fact — journals,
// monitor reports, inspector output, mesh, DNS and chain state — before the
// fleet is torn down. Every file is bounded and redacted, and the index says
// what was collected, from where, and what could not be.
package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// DirName is the artifacts directory under the run's artifact dir.
const DirName = "collected"

// IndexFile lists every collected file.
const IndexFile = "index.json"

// DefaultMaxBytes bounds one collected file.
const DefaultMaxBytes = 2 << 20

const truncatedMarker = "\n…[truncated by the e2e collector]\n"

// File is one collected artifact.
type File struct {
	// Path is relative to the collection dir.
	Path string `json:"path"`
	// Source is "cli" or a node name.
	Source  string `json:"source"`
	Command string `json:"command"`
	Bytes   int    `json:"bytes"`
	// Truncated is true when the output exceeded the size bound.
	Truncated bool `json:"truncated,omitempty"`
	// Error says why the item could not be collected (the file then holds
	// whatever partial output there was).
	Error string `json:"error,omitempty"`
}

// Index is what a collection produced.
type Index struct {
	Files []File `json:"files"`
}

// Failed returns the items that could not be collected.
func (ix Index) Failed() []File {
	var out []File
	for _, f := range ix.Files {
		if f.Error != "" {
			out = append(out, f)
		}
	}
	return out
}

// Collector gathers artifacts into dir.
type Collector interface {
	Collect(ctx context.Context, dir string) (Index, error)
}

// writer bounds, redacts and writes files, and builds the index.
type writer struct {
	dir      string
	maxBytes int
	red      *secrets.Redactor
	index    Index
}

func (w *writer) write(rel, source, command, content string, itemErr error) error {
	f := File{Path: rel, Source: source, Command: w.red.Redact(command)}
	content = w.red.Redact(content)
	if len(content) > w.maxBytes {
		content = strings.ToValidUTF8(content[:w.maxBytes], "") + truncatedMarker
		f.Truncated = true
	}
	if itemErr != nil {
		f.Error = w.red.Redact(itemErr.Error())
	}
	f.Bytes = len(content)
	path := filepath.Join(w.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write artifact %s: %w", path, err)
	}
	w.index.Files = append(w.index.Files, f)
	return nil
}

func (w *writer) finish() (Index, error) {
	sort.Slice(w.index.Files, func(i, j int) bool { return w.index.Files[i].Path < w.index.Files[j].Path })
	raw, err := json.MarshalIndent(w.index, "", "  ")
	if err != nil {
		return w.index, fmt.Errorf("failed to encode the artifact index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, IndexFile), raw, 0o600); err != nil {
		return w.index, fmt.Errorf("failed to write the artifact index: %w", err)
	}
	return w.index, nil
}

// LoadIndex reads a collection's index.
func LoadIndex(dir string) (Index, error) {
	raw, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		return Index{}, fmt.Errorf("failed to read artifact index in %s: %w", dir, err)
	}
	var ix Index
	if err := json.Unmarshal(raw, &ix); err != nil {
		return Index{}, fmt.Errorf("failed to parse artifact index in %s: %w", dir, err)
	}
	return ix, nil
}
