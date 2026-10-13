package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// anchorRoots are the repository directories a code anchor may start with.
var anchorRoots = []string{
	"core/", "chain/", "vault/", "sdk/", "sdk-vault/", "caddy/", "e2e/",
	"contracts/", "docs/", "website/", "plans/",
}

// identRe is what may follow the colon of an anchor: a Go, TypeScript or Zig
// identifier, optionally qualified (Type.Method) and optionally called.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*(\(\))?$`)

// codeAnchor is `path` or `path:Identifier` found in prose.
type codeAnchor struct {
	path  string
	ident string
	line  int
}

// extractAnchors finds code anchors in the inline code spans of a document.
// Spans holding placeholders (`<ns>`) or spaces are prose, not anchors.
func extractAnchors(lines []mdLine) []codeAnchor {
	var out []codeAnchor
	for _, l := range lines {
		if l.inCode {
			continue
		}
		for _, span := range codeSpans(l.text) {
			if a, ok := parseAnchor(span); ok {
				a.line = l.num
				out = append(out, a)
			}
		}
	}
	return out
}

func parseAnchor(span string) (codeAnchor, bool) {
	if strings.ContainsAny(span, " <>{}$") || !hasAnchorRoot(span) {
		return codeAnchor{}, false
	}
	path, ident, hasIdent := strings.Cut(span, ":")
	if hasIdent && !identRe.MatchString(ident) {
		return codeAnchor{}, false
	}
	return codeAnchor{path: path, ident: strings.TrimSuffix(ident, "()")}, true
}

func hasAnchorRoot(span string) bool {
	for _, r := range anchorRoots {
		if strings.HasPrefix(span, r) {
			return true
		}
	}
	return false
}

// checkAnchor resolves one anchor against the checkout: the path (or glob)
// must exist, and the identifier's last segment must appear as a word in the
// file, or in a file directly inside the directory.
func checkAnchor(root string, a codeAnchor) error {
	matches, err := filepath.Glob(filepath.Join(root, a.path))
	if err != nil {
		return fmt.Errorf("bad anchor pattern %q: %w", a.path, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("`%s` does not exist", a.path)
	}
	if a.ident == "" {
		return nil
	}
	parts := strings.Split(a.ident, ".")
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(parts[len(parts)-1]) + `\b`)
	for _, m := range matches {
		found, err := identIn(m, word)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}
	return fmt.Errorf("`%s` does not contain %s", a.path, a.ident)
}

func identIn(path string, word *regexp.Regexp) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("failed to stat %s: %w", path, err)
	}
	files := []string{path}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false, fmt.Errorf("failed to list %s: %w", path, err)
		}
		files = files[:0]
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return false, fmt.Errorf("failed to read %s: %w", f, err)
		}
		if word.Match(raw) {
			return true, nil
		}
	}
	return false, nil
}

// checkAnchors is the anchor gate for one document.
func checkAnchors(root, file string, lines []mdLine) []problem {
	var probs []problem
	for _, a := range extractAnchors(lines) {
		if err := checkAnchor(root, a); err != nil {
			probs = append(probs, problem{gate: "anchors", file: file, line: a.line, msg: err.Error()})
		}
	}
	return probs
}
