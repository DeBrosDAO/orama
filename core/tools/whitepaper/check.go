package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// problem is one gate failure.
type problem struct {
	gate string
	file string // book-relative, or a repository path for ownership
	line int
	msg  string
}

func (p problem) String() string {
	loc := p.file
	if p.line > 0 {
		loc = fmt.Sprintf("%s:%d", p.file, p.line)
	}
	return fmt.Sprintf("[%s] %s: %s", p.gate, loc, p.msg)
}

// document is one book file loaded for checking.
type document struct {
	file    string // book-relative
	chapter *Chapter
	lines   []mdLine
}

// runChecks runs every gate and returns the problems found.
func runChecks(b *Book) ([]problem, error) {
	files, err := trackedFiles(b.Root)
	if err != nil {
		return nil, err
	}
	docs, probs := loadDocuments(b)
	probs = append(probs, checkVersion(b)...)
	probs = append(probs, checkOwnership(b, files)...)

	anchors := map[string]map[string]bool{}
	for _, d := range docs {
		anchors[d.file] = anchorsOf(d.lines)
	}
	used := map[string]bool{}
	for _, d := range docs {
		probs = append(probs, lintMDX(d.file, d.lines)...)
		probs = append(probs, checkAnchors(b.Root, d.file, d.lines)...)
		probs = append(probs, lintLinks(b, d.file, d.lines, anchors)...)
		if d.chapter != nil {
			probs = append(probs, lintStructure(d.chapter, d.lines)...)
		}
		for _, l := range d.lines {
			for _, m := range imageRe.FindAllStringSubmatch(l.text, -1) {
				used[filepath.Base(m[2])] = true
			}
		}
	}
	probs = append(probs, checkDiagrams(b, used)...)
	probs = append(probs, checkGenerated(b)...)
	sort.SliceStable(probs, func(i, j int) bool { return probs[i].gate < probs[j].gate })
	return probs, nil
}

// loadDocuments reads every chapter and appendix in the manifest, and
// reports manifest entries without a file and book files without an entry.
func loadDocuments(b *Book) ([]document, []problem) {
	var docs []document
	var probs []problem
	listed := map[string]bool{}
	add := func(file string, ch *Chapter) {
		listed[file] = true
		raw, err := os.ReadFile(b.Path(file))
		if err != nil {
			probs = append(probs, problem{gate: "manifest", file: file, msg: "listed in book.yaml but missing"})
			return
		}
		docs = append(docs, document{file: file, chapter: ch, lines: splitLines(string(raw))})
	}
	for _, ch := range b.Chapters() {
		add(ch.File, ch)
	}
	for _, a := range b.Manifest.Appendices {
		add(a.File, nil)
	}
	for _, dir := range []string{"vol1", "vol2", "appendices"} {
		found, _ := filepath.Glob(b.Path(filepath.Join(dir, "*.md")))
		for _, f := range found {
			if r := rel(b, f); !listed[r] {
				probs = append(probs, problem{gate: "manifest", file: r, msg: "is not listed in book.yaml"})
			}
		}
	}
	return docs, probs
}

// checkVersion is the release gate: the book and every chapter must be
// stamped with /VERSION. `make bump` changes VERSION; the gate then fails
// until each chapter has been re-verified against the code and re-stamped.
func checkVersion(b *Book) []problem {
	want, err := repoVersion(b.Root)
	if err != nil {
		return []problem{{gate: "version", file: "VERSION", msg: err.Error()}}
	}
	var probs []problem
	if b.Manifest.Version != want {
		probs = append(probs, problem{gate: "version", file: "book.yaml",
			msg: fmt.Sprintf("version is %q but /VERSION is %q", b.Manifest.Version, want)})
	}
	var stale []string
	for _, ch := range b.Chapters() {
		if ch.Verified != want {
			stale = append(stale, fmt.Sprintf("%d", ch.Number))
		}
	}
	if len(stale) > 0 {
		probs = append(probs, problem{gate: "version", file: "book.yaml",
			msg: fmt.Sprintf("%d chapters are not verified against %s (chapters %s): re-verify each against the code and set verified: %s",
				len(stale), want, strings.Join(stale, ", "), want)})
	}
	return probs
}
