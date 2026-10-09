package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testBook builds a minimal book in a temporary repository: VERSION, the
// manifest and the given book-relative files.
func testBook(t *testing.T, version string, chapters []Chapter, files map[string]string) *Book {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("VERSION", version+"\n")
	write(filepath.Join(bookDir, "book.yaml"), "title: T\n")
	for rel, body := range files {
		write(filepath.Join(bookDir, rel), body)
	}
	b := &Book{Root: root, Manifest: Manifest{Version: version, Volumes: []Volume{{Number: 1, Parts: []Part{{Chapters: chapters}}}}}}
	for i, ch := range b.Chapters() {
		ch.Number = i + 1
	}
	return b
}

func hasProblem(probs []problem, gate, substr string) bool {
	for _, p := range probs {
		if p.gate == gate && strings.Contains(p.msg, substr) {
			return true
		}
	}
	return false
}

func TestCheckOwnership_everyFileOwned(t *testing.T) {
	b := testBook(t, "1.0.0", []Chapter{{File: "c.md", Owns: []string{"core/pkg/a/", "Makefile"}}}, nil)
	b.Manifest.Exclude = []string{"docs/"}
	files := []string{"core/pkg/a/a.go", "Makefile", "docs/x.md"}
	if probs := checkOwnership(b, files); len(probs) != 0 {
		t.Fatalf("want no problems, got %v", probs)
	}
}

func TestCheckOwnership_newPackageWithoutOwner(t *testing.T) {
	b := testBook(t, "1.0.0", []Chapter{{File: "c.md", Owns: []string{"core/pkg/a/"}}}, nil)
	files := []string{"core/pkg/a/a.go", "core/pkg/new/x.go", "core/pkg/new/y.go"}
	probs := checkOwnership(b, files)
	if !hasProblem(probs, "ownership", "core/pkg/new/ (2 tracked files)") {
		t.Fatalf("want the new package reported once with its file count, got %v", probs)
	}
}

func TestCheckOwnership_rejectsBroadDuplicateAndDeadEntries(t *testing.T) {
	b := testBook(t, "1.0.0", []Chapter{
		{File: "a.md", Owns: []string{"core/pkg/", "core/pkg/x/"}},
		{File: "b.md", Owns: []string{"core/pkg/x/", "core/pkg/gone/"}},
	}, nil)
	probs := checkOwnership(b, []string{"core/pkg/x/x.go"})
	for _, want := range []string{"too broad", "already owned", "matches no tracked file"} {
		if !hasProblem(probs, "ownership", want) {
			t.Errorf("want a problem containing %q, got %v", want, probs)
		}
	}
}

func TestOwnerOf_longestPrefixWins(t *testing.T) {
	b := testBook(t, "1.0.0", []Chapter{
		{File: "ns.md", Owns: []string{"core/pkg/namespace/"}},
		{File: "rec.md", Owns: []string{"core/pkg/namespace/tenant_reconciler.go"}},
	}, nil)
	if got := ownerOf("core/pkg/namespace/tenant_reconciler.go", b); got == nil || got.File != "rec.md" {
		t.Fatalf("want rec.md, got %v", got)
	}
	if got := ownerOf("core/pkg/namespace/blueprint.go", b); got == nil || got.File != "ns.md" {
		t.Fatalf("want ns.md, got %v", got)
	}
	if got := ownerOf("chain/app/app.go", b); got != nil {
		t.Fatalf("want no owner, got %v", got.File)
	}
}

func TestParseAnchor(t *testing.T) {
	cases := []struct {
		span, path, ident string
		ok                bool
	}{
		{"core/pkg/rqlite/eviction.go", "core/pkg/rqlite/eviction.go", "", true},
		{"core/pkg/rqlite/eviction.go:SafeToRemoveVoter", "core/pkg/rqlite/eviction.go", "SafeToRemoveVoter", true},
		{"core/pkg/x.go:Type.Method()", "core/pkg/x.go", "Type.Method", true},
		{"core/pkg/<ns>/x.go", "", "", false},
		{"orama node install", "", "", false},
		{"/etc/orama/archive-signers", "", "", false},
		{"core/pkg/x.go:10", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		a, ok := parseAnchor(c.span)
		if ok != c.ok || a.path != c.path || a.ident != c.ident {
			t.Errorf("parseAnchor(%q) = %+v, %v; want path %q ident %q ok %v", c.span, a, ok, c.path, c.ident, c.ok)
		}
	}
}

func TestCheckAnchor(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "core", "pkg", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\nfunc SafeToRemove() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := []codeAnchor{
		{path: "core/pkg/p/p.go"},
		{path: "core/pkg/p/p.go", ident: "SafeToRemove"},
		{path: "core/pkg/p/", ident: "SafeToRemove"},
		{path: "core/pkg/p/*.go"},
	}
	for _, a := range ok {
		if err := checkAnchor(root, a); err != nil {
			t.Errorf("checkAnchor(%+v): %v", a, err)
		}
	}
	bad := []codeAnchor{
		{path: "core/pkg/p/missing.go"},
		{path: "core/pkg/p/p.go", ident: "Safe"},
		{path: "core/pkg/p/p.go", ident: "Gone"},
	}
	for _, a := range bad {
		if err := checkAnchor(root, a); err == nil {
			t.Errorf("checkAnchor(%+v): want an error", a)
		}
	}
}

func TestLintMDX(t *testing.T) {
	doc := "# T\n\nUses `map[string]{}` in code.\n\n```go\nif a < b {}\n```\n\nBad { brace.\n\na < b\n\nNote[^1]\n"
	probs := lintMDX("c.md", splitLines(doc))
	if len(probs) != 3 {
		t.Fatalf("want brace, '<' and footnote problems on prose only, got %v", probs)
	}
	for _, p := range probs {
		if p.line < 9 {
			t.Errorf("flagged code at line %d: %v", p.line, p)
		}
	}
}

func TestLintStructure(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("# Cache\n\n> **At a glance.**\n>\n> - x\n\n")
	for _, s := range subsystemSections {
		sb.WriteString("## " + s + "\n\ntext\n\n### Sub\n\n")
	}
	ch := &Chapter{File: "c.md", Title: "Cache", Template: templateSubsystem}
	if probs := lintStructure(ch, splitLines(sb.String())); len(probs) != 0 {
		t.Fatalf("want a valid chapter, got %v", probs)
	}
	broken := strings.Replace(sb.String(), "## Lifecycle", "## Life cycle", 1)
	broken = strings.Replace(broken, "> **At a glance.**", "Intro.", 1)
	probs := lintStructure(ch, splitLines(broken))
	if !hasProblem(probs, "structure", "At-a-glance") || !hasProblem(probs, "structure", "level-2 sections") {
		t.Fatalf("want glance and section problems, got %v", probs)
	}
	narrative := &Chapter{File: "n.md", Title: "Cache", Template: templateNarrative}
	if probs := lintStructure(narrative, splitLines("# Cache\n\n> **At a glance.**\n\n## Anything\n")); len(probs) != 0 {
		t.Fatalf("narrative chapters choose their own sections, got %v", probs)
	}
}

func TestGithubSlug(t *testing.T) {
	cases := map[string]string{
		"The boot graph":                "the-boot-graph",
		"Failure modes":                 "failure-modes",
		"`SafeToRemoveVoter` and quorum": "safetoremovevoter-and-quorum",
		"Raft: bootstrap vs. rejoin":    "raft-bootstrap-vs-rejoin",
		"Pub/sub":                       "pubsub",
	}
	for in, want := range cases {
		if got := githubSlug(in); got != want {
			t.Errorf("githubSlug(%q) = %q, want %q", in, got, want)
		}
	}
	s := newSlugger()
	if a, b2 := s.slug("Sub"), s.slug("Sub"); a != "sub" || b2 != "sub-1" {
		t.Errorf("duplicates: got %q, %q", a, b2)
	}
}

func TestLintLinks(t *testing.T) {
	b := testBook(t, "1.0.0", nil, map[string]string{"diagrams/ch01-a.svg": "<svg/>"})
	anchors := map[string]map[string]bool{
		"vol1/01-a.md": {"intro": true},
		"vol1/02-b.md": {"the-model": true},
	}
	doc := "![ok](../diagrams/ch01-a.svg) ![gone](../diagrams/ch01-x.svg)\n" +
		"[ok](02-b.md#the-model) [self](#intro) [bad](02-b.md#nope) [missing](03-c.md) [web](https://x.y)\n"
	probs := lintLinks(b, "vol1/01-a.md", splitLines(doc), anchors)
	if len(probs) != 3 {
		t.Fatalf("want missing image, missing anchor and unknown file, got %v", probs)
	}
}

func TestCheckVersion(t *testing.T) {
	b := testBook(t, "0.3.0", []Chapter{{File: "a.md", Verified: "0.3.0"}, {File: "b.md", Verified: "0.2.9"}}, nil)
	probs := checkVersion(b)
	if !hasProblem(probs, "version", "1 chapters are not verified against 0.3.0 (chapters 2)") {
		t.Fatalf("want chapter 2 reported, got %v", probs)
	}
	b.Manifest.Version = "0.2.9"
	if !hasProblem(checkVersion(b), "version", `version is "0.2.9" but /VERSION is "0.3.0"`) {
		t.Fatal("want the manifest version mismatch reported")
	}
	b2 := testBook(t, "0.3.0", []Chapter{{File: "a.md", Verified: "0.3.0"}}, nil)
	if probs := checkVersion(b2); len(probs) != 0 {
		t.Fatalf("want no problems, got %v", probs)
	}
}

func TestCheckDiagrams(t *testing.T) {
	src := "a -> b\n"
	b := testBook(t, "1.0.0", nil, map[string]string{"diagrams/ch01-a.d2": src, "diagrams/orphan.svg": "<svg/>"})
	hash, err := sourceHash(b.Path("diagrams/ch01-a.d2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.Path("diagrams/ch01-a.svg"), []byte("<svg/>\n"+stampPrefix+hash+" -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probs := checkDiagrams(b, map[string]bool{"ch01-a.svg": true})
	if len(probs) != 1 || !hasProblem(probs, "diagrams", "has no D2 source") {
		t.Fatalf("want only the orphan SVG reported, got %v", probs)
	}
	if err := os.WriteFile(b.Path("diagrams/ch01-a.d2"), []byte("a -> c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasProblem(checkDiagrams(b, map[string]bool{}), "diagrams", "stale") {
		t.Fatal("want a stale SVG reported after the source changed")
	}
}
