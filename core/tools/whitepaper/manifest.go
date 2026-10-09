package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// bookDir is the default book's directory relative to the repository root;
// `-book` selects another.
const bookDir = "docs/whitepaper/technical-reference"

// defaultTypstTemplate is the typst template, book-relative, used when a
// manifest names none.
const defaultTypstTemplate = "typst/template.typ"

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

	// Ownership turns the ownership gate off when set to false: a short
	// edition does not own code, the deep book it derives from does. Unset
	// means on.
	Ownership *bool `yaml:"ownership"`
	// MaxWordsTotal caps the prose words of the whole book (the words gate);
	// zero means no cap.
	MaxWordsTotal int `yaml:"max_words_total"`
	// Output, when set, builds every chapter into one PDF named
	// <output>-v<version>.pdf, with no appendices volume.
	Output string `yaml:"output"`
	// TypstTemplate is the typst template, book-relative; it may live in
	// another book's directory. Default: typst/template.typ.
	TypstTemplate string `yaml:"typst_template"`
	// Typeset overrides the template's layout for this book only; every
	// field left zero keeps the template default.
	Typeset Typeset `yaml:"typeset"`
}

// Chapter-start modes (typeset.chapter_start): "odd" is the template default.
const (
	chapterStartOdd  = "odd"  // chapters and parts open on a right-hand page
	chapterStartNext = "next" // on the next page, left or right
	chapterStartFlow = "flow" // chapters follow each other without page breaks
)

// Typeset is the book.yaml `typeset` block: layout options a short edition
// uses to fit its page budget. A zero field is "template default".
type Typeset struct {
	// TOCDepth is the heading depth of the table of contents (default 2).
	TOCDepth int `yaml:"toc_depth"`
	// BodySize is the body text size in pt (default 10.5).
	BodySize float64 `yaml:"body_size"`
	// MarginInside, MarginOutside and MarginVertical are page margins in mm
	// (defaults 28, 22 and 26); a margin is set all or none.
	MarginInside   float64 `yaml:"margin_inside"`
	MarginOutside  float64 `yaml:"margin_outside"`
	MarginVertical float64 `yaml:"margin_vertical"`
	// ChapterStart is odd, next or flow (default odd).
	ChapterStart string `yaml:"chapter_start"`
	// DiagramMaxWidth and DiagramMaxHeight cap a diagram's printed size in pt
	// (defaults 440 and 620); DiagramScale is pt per SVG px (default 0.5).
	DiagramMaxWidth  float64 `yaml:"diagram_max_width"`
	DiagramMaxHeight float64 `yaml:"diagram_max_height"`
	DiagramScale     float64 `yaml:"diagram_scale"`
}

// validate rejects a layout the template cannot typeset.
func (t Typeset) validate() error {
	switch t.ChapterStart {
	case "", chapterStartOdd, chapterStartNext, chapterStartFlow:
	default:
		return fmt.Errorf("typeset.chapter_start %q must be odd, next or flow", t.ChapterStart)
	}
	margins := 0
	for _, m := range []float64{t.MarginInside, t.MarginOutside, t.MarginVertical} {
		if m < 0 {
			return fmt.Errorf("typeset margins must not be negative")
		}
		if m > 0 {
			margins++
		}
	}
	if margins != 0 && margins != 3 {
		return fmt.Errorf("typeset.margin_inside, margin_outside and margin_vertical are set all or none")
	}
	for name, v := range map[string]float64{"body_size": t.BodySize, "diagram_max_width": t.DiagramMaxWidth,
		"diagram_max_height": t.DiagramMaxHeight, "diagram_scale": t.DiagramScale} {
		if v < 0 {
			return fmt.Errorf("typeset.%s must not be negative", name)
		}
	}
	if t.TOCDepth < 0 {
		return fmt.Errorf("typeset.toc_depth must not be negative")
	}
	return nil
}

// templateArgs are the extra arguments of the template's `book` call, empty
// for a book with no typeset block.
func (t Typeset) templateArgs() string {
	var args []string
	if t.TOCDepth > 0 {
		args = append(args, fmt.Sprintf("toc-depth: %d", t.TOCDepth))
	}
	if t.BodySize > 0 {
		args = append(args, fmt.Sprintf("body-size: %gpt", t.BodySize))
	}
	if t.MarginInside > 0 {
		args = append(args, fmt.Sprintf("margin: (inside: %gmm, outside: %gmm, top: %gmm, bottom: %gmm)",
			t.MarginInside, t.MarginOutside, t.MarginVertical, t.MarginVertical))
	}
	if t.ChapterStart != "" {
		args = append(args, fmt.Sprintf("chapter-start: %q", t.ChapterStart))
	}
	if len(args) == 0 {
		return ""
	}
	return ", " + strings.Join(args, ", ")
}

// filterMeta are the diagram options for the pandoc filter, only those set.
func (t Typeset) filterMeta() map[string]any {
	meta := map[string]any{}
	if t.DiagramMaxWidth > 0 {
		meta["diagram_max_width"] = t.DiagramMaxWidth
	}
	if t.DiagramMaxHeight > 0 {
		meta["diagram_max_height"] = t.DiagramMaxHeight
	}
	if t.DiagramScale > 0 {
		meta["diagram_scale"] = t.DiagramScale
	}
	return meta
}

// OwnershipOn reports whether the ownership gate applies to the book.
func (m Manifest) OwnershipOn() bool { return m.Ownership == nil || *m.Ownership }

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
	// MaxWords caps the chapter's prose words (the words gate); zero means
	// no cap.
	MaxWords int `yaml:"max_words"`

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
	Rel      string // book directory relative to Root; empty means bookDir
	Manifest Manifest
}

// RelDir is the book directory relative to the repository root.
func (b *Book) RelDir() string {
	if b.Rel == "" {
		return bookDir
	}
	return b.Rel
}

// Dir is the book directory, absolute.
func (b *Book) Dir() string { return filepath.Join(b.Root, b.RelDir()) }

// TemplatePath is the typst template, absolute.
func (b *Book) TemplatePath() string {
	if b.Manifest.TypstTemplate == "" {
		return b.Path(defaultTypstTemplate)
	}
	return b.Path(b.Manifest.TypstTemplate)
}

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

// loadBook reads book.yaml of the book at rel inside the repository at root.
func loadBook(root, rel string) (*Book, error) {
	path := filepath.Join(root, rel, "book.yaml")
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
	if err := m.Typeset.validate(); err != nil {
		return nil, fmt.Errorf("invalid book manifest %s: %w", path, err)
	}
	b := &Book{Root: root, Rel: rel, Manifest: m}
	for i, ch := range b.Chapters() {
		ch.Number = i + 1
	}
	for i := range b.Manifest.Appendices {
		b.Manifest.Appendices[i].Letter = string(rune('A' + i))
	}
	return b, nil
}

// cleanBookRel validates a `-book` value: a directory inside the repository,
// given relative to its root.
func cleanBookRel(book string) (string, error) {
	rel := filepath.ToSlash(filepath.Clean(book))
	if filepath.IsAbs(book) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("-book %q must be a directory inside the repository, relative to its root", book)
	}
	return rel, nil
}

// findRepoRoot walks up from dir to the directory holding VERSION and the
// manifest of the book at rel.
func findRepoRoot(dir, rel string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s: %w", dir, err)
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		if fileExists(filepath.Join(cur, "VERSION")) && fileExists(filepath.Join(cur, rel, "book.yaml")) {
			return cur, nil
		}
		if filepath.Dir(cur) == cur {
			return "", fmt.Errorf("no repository root (VERSION and %s/book.yaml) above %s", rel, abs)
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
