package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args       []string
		cmd, book  string
		wantErrSub string
	}{
		{[]string{"check"}, "check", bookDir, ""},
		{[]string{"build", "-book", "docs/whitepaper/orama-whitepaper"}, "build", "docs/whitepaper/orama-whitepaper", ""},
		{[]string{"-book", "docs/x/", "gen"}, "gen", "docs/x", ""},
		{[]string{"check", "-book=./docs/x"}, "check", "docs/x", ""},
		{[]string{}, "", "", "usage"},
		{[]string{"check", "extra"}, "", "", "usage"},
		{[]string{"check", "-nope"}, "", "", "usage"},
		{[]string{"check", "-book", "/abs/dir"}, "", "", "inside the repository"},
		{[]string{"check", "-book", "../out"}, "", "", "inside the repository"},
	}
	for _, c := range cases {
		cmd, book, err := parseArgs(c.args)
		if c.wantErrSub != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
				t.Errorf("parseArgs(%v): want error containing %q, got %v", c.args, c.wantErrSub, err)
			}
			continue
		}
		if err != nil || cmd != c.cmd || book != c.book {
			t.Errorf("parseArgs(%v) = %q, %q, %v; want %q, %q", c.args, cmd, book, err, c.cmd, c.book)
		}
	}
}

func TestLoadBook_otherBookDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "short")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "title: S\nownership: false\nmax_words_total: 10\noutput: s\nvolumes:\n- number: 1\n  parts:\n  - title: P\n    chapters:\n    - file: a.md\n      max_words: 5\n"
	for path, body := range map[string]string{filepath.Join(root, "VERSION"): "1.0.0\n", filepath.Join(dir, "book.yaml"): manifest} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := findRepoRoot(dir, bookDir); err == nil {
		t.Fatal("want no root for the default book, which is absent")
	}
	got, err := findRepoRoot(dir, "docs/short")
	if err != nil || got != root {
		t.Fatalf("findRepoRoot = %q, %v; want %q", got, err, root)
	}
	b, err := loadBook(root, "docs/short")
	if err != nil {
		t.Fatal(err)
	}
	if b.Dir() != dir || b.Manifest.OwnershipOn() || b.Manifest.MaxWordsTotal != 10 || b.Chapters()[0].MaxWords != 5 {
		t.Fatalf("manifest options not loaded: %+v", b.Manifest)
	}
}

func TestOwnershipOn_defaultsToOn(t *testing.T) {
	if !(Manifest{}).OwnershipOn() {
		t.Fatal("ownership must default to on")
	}
	off := false
	if (Manifest{Ownership: &off}).OwnershipOn() {
		t.Fatal("ownership: false must turn the gate off")
	}
}

func TestRunChecks_ownershipOffSkipsGate(t *testing.T) {
	chapters := []Chapter{{File: "c.md", Title: "C", Template: templateNarrative, Verified: "1.0.0"}}
	files := map[string]string{"c.md": "# C\n\n> **At a glance.**\n\nBody words.\n"}
	b := testBook(t, "1.0.0", chapters, files)
	b.Manifest.Ownership = new(bool)
	if out, err := exec.Command("git", "-C", b.Root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git is unavailable: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(b.Root, "untracked-owner-less.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", b.Root, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	probs, err := runChecks(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		if p.gate == "ownership" {
			t.Fatalf("ownership gate ran with ownership: false: %v", p)
		}
	}
	b.Manifest.Ownership = nil
	probs, err = runChecks(b)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(probs, "ownership", "explained by no chapter") {
		t.Fatalf("ownership on: want the unowned file reported, got %v", probs)
	}
}

func TestProseWords(t *testing.T) {
	doc := "# Title here\n\n> **At a glance.**\n>\n> - one two\n\nSee [the docs](https://x.y/z) and `code span`.\n\n![alt text](a.svg)\n\n| a | b c |\n|---|---|\n\n```go\nfunc a() {}\n```\n\n1. step one\n"
	// Title here (2) + At a glance. (3) + one two (2) + See the docs and code span. (6) + step one (2)
	if got := proseWords(splitLines(doc)); got != 15 {
		t.Fatalf("proseWords = %d, want 15", got)
	}
	if got := proseWords(splitLines("")); got != 0 {
		t.Fatalf("empty document: %d words, want 0", got)
	}
}

func TestCheckWords(t *testing.T) {
	chapter := func(file string, max int) *Chapter { return &Chapter{File: file, MaxWords: max} }
	doc := func(ch *Chapter, n int) document {
		return document{file: ch.File, chapter: ch, lines: splitLines(strings.Repeat("word ", n))}
	}
	b := &Book{Manifest: Manifest{MaxWordsTotal: 30}}

	ok1, ok2 := chapter("a.md", 10), chapter("b.md", 10)
	if probs := checkWords(b, []document{doc(ok1, 10), doc(ok2, 3)}); len(probs) != 0 {
		t.Fatalf("within limits: want no problems, got %v", probs)
	}

	over, empty := chapter("c.md", 10), chapter("d.md", 10)
	probs := checkWords(b, []document{doc(over, 11), doc(empty, 0)})
	if !hasProblem(probs, "words", "11 words, over its max_words of 10") || !hasProblem(probs, "words", "has no prose") {
		t.Fatalf("want over-cap and empty chapter problems, got %v", probs)
	}

	big1, big2, big3 := chapter("e.md", 20), chapter("f.md", 20), chapter("g.md", 20)
	probs = checkWords(b, []document{doc(big1, 12), doc(big2, 12), doc(big3, 12)})
	if len(probs) != 1 || !hasProblem(probs, "words", "36 words, over max_words_total of 30") {
		t.Fatalf("want only the total problem, got %v", probs)
	}

	uncapped := chapter("h.md", 0)
	if probs := checkWords(&Book{}, []document{doc(uncapped, 0), doc(uncapped, 5000)}); len(probs) != 0 {
		t.Fatalf("no caps: want no problems, got %v", probs)
	}
}

func TestLintLinks_sharedDiagramsAllowed(t *testing.T) {
	b := testBook(t, "1.0.0", nil, nil)
	shared := filepath.Join(b.Root, "docs", "whitepaper", "technical-reference", "diagrams")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "ch01-a.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The short book sits beside the technical reference.
	b.Rel = "docs/whitepaper/orama-whitepaper"
	if err := os.MkdirAll(b.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "![ok](../technical-reference/diagrams/ch01-a.svg) [link](../technical-reference/diagrams/ch01-a.svg)\n" +
		"![gone](../technical-reference/diagrams/nope.svg) [gone](../technical-reference/diagrams/nope.svg) [other](../technical-reference/vol1/01.md)\n"
	probs := lintLinks(b, "ch01.md", splitLines(doc), map[string]map[string]bool{"ch01.md": {}})
	if len(probs) != 3 {
		t.Fatalf("want the missing image, missing diagram link and non-diagram link, got %v", probs)
	}
}

func TestBuildLayout_singleVolume(t *testing.T) {
	b := &Book{Rel: "docs/whitepaper/orama-whitepaper", Root: "/r", Manifest: Manifest{
		Output:        "orama-whitepaper",
		TypstTemplate: "../technical-reference/typst/template.typ",
		Volumes: []Volume{{Number: 1, Parts: []Part{
			{Title: "One", Chapters: []Chapter{{File: "a.md"}}},
			{Title: "Two", Chapters: []Chapter{{File: "b.md"}}},
		}}},
		Appendices: []Appendix{{File: "x.md"}},
	}}
	for i, ch := range b.Chapters() {
		ch.Number = i + 1
	}
	vols := volumes(b)
	if len(vols) != 1 || vols[0].name != "book" || len(vols[0].body) != 4 {
		t.Fatalf("want one volume holding both parts and chapters, got %+v", vols)
	}
	if got := typstRoot(b); got != "/r/docs/whitepaper" {
		t.Fatalf("typstRoot = %q, want the parent holding the sibling template", got)
	}
	if got, err := templateImport(b); err != nil || got != "../../technical-reference/typst/template.typ" {
		t.Fatalf("templateImport = %q, %v", got, err)
	}
	def := &Book{Root: "/r"}
	if got := typstRoot(def); got != def.Dir() {
		t.Fatalf("default book typstRoot = %q, want %q", got, def.Dir())
	}
	if got, _ := templateImport(def); got != "../typst/template.typ" {
		t.Fatalf("default templateImport = %q", got)
	}
}

func TestTypesetTemplateArgs(t *testing.T) {
	if got := (Typeset{}).templateArgs(); got != "" {
		t.Errorf("empty typeset must add no template arguments, got %q", got)
	}
	got := Typeset{TOCDepth: 1, BodySize: 10.5, MarginInside: 26, MarginOutside: 21, MarginVertical: 24, ChapterStart: chapterStartNext}.templateArgs()
	for _, want := range []string{"toc-depth: 1", "body-size: 10.5pt", "inside: 26mm, outside: 21mm, top: 24mm, bottom: 24mm", `chapter-start: "next"`} {
		if !strings.Contains(got, want) {
			t.Errorf("templateArgs() = %q; want it to contain %q", got, want)
		}
	}
}

func TestTypesetFilterMeta(t *testing.T) {
	if got := (Typeset{}).filterMeta(); len(got) != 0 {
		t.Errorf("empty typeset must add no filter metadata, got %v", got)
	}
	got := Typeset{DiagramMaxHeight: 300, DiagramScale: 0.4}.filterMeta()
	if len(got) != 2 || got["diagram_max_height"] != 300.0 || got["diagram_scale"] != 0.4 {
		t.Errorf("filterMeta() = %v", got)
	}
}

func TestTypesetValidate(t *testing.T) {
	cases := []struct {
		name string
		ts   Typeset
		want string
	}{
		{"zero value", Typeset{}, ""},
		{"all margins", Typeset{MarginInside: 1, MarginOutside: 1, MarginVertical: 1}, ""},
		{"partial margins", Typeset{MarginInside: 1}, "all or none"},
		{"negative margin", Typeset{MarginInside: -1, MarginOutside: 1, MarginVertical: 1}, "negative"},
		{"bad chapter start", Typeset{ChapterStart: "sideways"}, "odd, next or flow"},
		{"negative body size", Typeset{BodySize: -1}, "body_size"},
		{"negative toc depth", Typeset{TOCDepth: -1}, "toc_depth"},
	}
	for _, c := range cases {
		err := c.ts.validate()
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: validate() = %v; want error containing %q", c.name, err, c.want)
		}
	}
}
