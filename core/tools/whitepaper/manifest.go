package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// bookDir is the book's directory relative to the repository root.
const bookDir = "docs/whitepaper/technical-reference"

// Chapter templates (book.yaml `template`).
const (
	templateSubsystem = "subsystem"
	templateNarrative = "narrative"
)

// Manifest is book.yaml.
type Manifest struct {
	Title      string     `yaml:"title"`
	Subtitle   string     `yaml:"subtitle"`
	Version    string     `yaml:"version"`
	Exclude    []string   `yaml:"exclude"`
	Volumes    []Volume   `yaml:"volumes"`
	Appendices []Appendix `yaml:"appendices"`
}

// Volume is one printed volume.
type Volume struct {
	Number int    `yaml:"number"`
	Title  string `yaml:"title"`
	Parts  []Part `yaml:"parts"`
}

// Part groups chapters inside a volume.
type Part struct {
	Title    string    `yaml:"title"`
	Chapters []Chapter `yaml:"chapters"`
}

// Chapter is one chapter file and the code it explains.
type Chapter struct {
	File     string   `yaml:"file"`
	Title    string   `yaml:"title"`
	Template string   `yaml:"template"`
	Verified string   `yaml:"verified"`
	Owns     []string `yaml:"owns"`

	// Number is the chapter's position in the whole book, from 1. It is
	// assigned on load, not read from the file.
	Number int `yaml:"-"`
}

// Appendix is one appendix file.
type Appendix struct {
	File      string `yaml:"file"`
	Title     string `yaml:"title"`
	Generated bool   `yaml:"generated"`

	// Letter is A, B, ... in manifest order, assigned on load.
	Letter string `yaml:"-"`
}

// Book is a loaded manifest bound to a repository checkout.
type Book struct {
	Root     string // repository root, absolute
	Manifest Manifest
}

// Dir is the book directory, absolute.
func (b *Book) Dir() string { return filepath.Join(b.Root, bookDir) }

// Path resolves a book-relative file to an absolute path.
func (b *Book) Path(rel string) string { return filepath.Join(b.Dir(), rel) }

// Chapters returns every chapter in book order.
func (b *Book) Chapters() []*Chapter {
	var out []*Chapter
	for vi := range b.Manifest.Volumes {
		for pi := range b.Manifest.Volumes[vi].Parts {
			part := &b.Manifest.Volumes[vi].Parts[pi]
			for ci := range part.Chapters {
				out = append(out, &part.Chapters[ci])
			}
		}
	}
	return out
}

// loadBook reads book.yaml from the repository at root.
func loadBook(root string) (*Book, error) {
	path := filepath.Join(root, bookDir, "book.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the book manifest %s: %w", path, err)
	}
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to parse the book manifest %s: %w", path, err)
	}
	b := &Book{Root: root, Manifest: m}
	for i, ch := range b.Chapters() {
		ch.Number = i + 1
	}
	for i := range b.Manifest.Appendices {
		b.Manifest.Appendices[i].Letter = string(rune('A' + i))
	}
	return b, nil
}

// findRepoRoot walks up from dir to the directory holding VERSION and the
// book manifest.
func findRepoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s: %w", dir, err)
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		if fileExists(filepath.Join(cur, "VERSION")) && fileExists(filepath.Join(cur, bookDir, "book.yaml")) {
			return cur, nil
		}
		if filepath.Dir(cur) == cur {
			return "", fmt.Errorf("no repository root (VERSION and %s/book.yaml) above %s", bookDir, abs)
		}
	}
}

// repoVersion reads /VERSION.
func repoVersion(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", fmt.Errorf("failed to read VERSION: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
