package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// subsystemSections are the level-2 headings of a subsystem chapter, in
// order (STYLE.md, "Chapter skeleton").
var subsystemSections = []string{
	"Why it exists",
	"The model",
	"How it works",
	"State it owns",
	"Lifecycle",
	"Failure modes",
	"Trust and security",
	"Limits and scale",
	"Design decisions",
	"Known gaps",
	"Verify it yourself",
}

// glanceMarker opens the At-a-glance block.
const glanceMarker = "> **At a glance.**"

var (
	footnoteRe   = regexp.MustCompile(`\[\^`)
	headingAttrs = regexp.MustCompile(`^#{1,6} .*\{#`)
)

// lintMDX enforces the Markdown restrictions the website's MDX pipeline
// needs (STYLE.md, "Markdown restrictions").
func lintMDX(file string, lines []mdLine) []problem {
	var probs []problem
	for _, l := range lines {
		if l.inCode {
			continue
		}
		prose := stripCodeSpans(l.text)
		for _, bad := range []struct{ token, why string }{
			{"{", "a curly brace outside code"},
			{"}", "a curly brace outside code"},
			{"<", "a bare '<' outside code (write \"less than\" or use a code span)"},
		} {
			if strings.Contains(prose, bad.token) {
				probs = append(probs, problem{gate: "markdown", file: file, line: l.num, msg: bad.why})
			}
		}
		if footnoteRe.MatchString(prose) {
			probs = append(probs, problem{gate: "markdown", file: file, line: l.num, msg: "a footnote"})
		}
		if headingAttrs.MatchString(l.text) {
			probs = append(probs, problem{gate: "markdown", file: file, line: l.num, msg: "a heading attribute"})
		}
	}
	return probs
}

// lintStructure checks the chapter skeleton: title, At-a-glance block and,
// for subsystem chapters, the exact level-2 sections in order.
func lintStructure(ch *Chapter, lines []mdLine) []problem {
	var probs []problem
	hs := headings(lines)
	if len(hs) == 0 || hs[0].level != 1 || hs[0].text != ch.Title {
		probs = append(probs, problem{gate: "structure", file: ch.File,
			msg: fmt.Sprintf("must open with the level-1 heading %q", "# "+ch.Title)})
	}
	if !hasGlance(lines) {
		probs = append(probs, problem{gate: "structure", file: ch.File,
			msg: fmt.Sprintf("needs the At-a-glance block (%q) right after the title", glanceMarker)})
	}
	if ch.Template != templateSubsystem {
		return probs
	}
	var got []string
	for _, h := range hs {
		if h.level == 2 {
			got = append(got, h.text)
		}
	}
	if strings.Join(got, "|") != strings.Join(subsystemSections, "|") {
		probs = append(probs, problem{gate: "structure", file: ch.File,
			msg: fmt.Sprintf("level-2 sections are %q; a subsystem chapter needs exactly %q", got, subsystemSections)})
	}
	return probs
}

// hasGlance reports whether the first non-blank line after the title opens
// the At-a-glance block.
func hasGlance(lines []mdLine) bool {
	seenTitle := false
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		if t == "" {
			continue
		}
		if !seenTitle {
			seenTitle = strings.HasPrefix(t, "# ")
			continue
		}
		return strings.HasPrefix(t, glanceMarker)
	}
	return false
}

// lintLinks checks images and relative links: images must exist, and links
// to book files must name a file in the manifest and an anchor it defines.
func lintLinks(b *Book, file string, lines []mdLine, anchors map[string]map[string]bool) []problem {
	var probs []problem
	dir := filepath.Dir(file)
	for _, l := range lines {
		if l.inCode {
			continue
		}
		prose := stripCodeSpans(l.text)
		for _, m := range imageRe.FindAllStringSubmatch(prose, -1) {
			if !fileExists(b.Path(filepath.Join(dir, m[2]))) {
				probs = append(probs, problem{gate: "links", file: file, line: l.num, msg: fmt.Sprintf("image %s does not exist", m[2])})
			}
		}
		for _, m := range linkRe.FindAllStringSubmatch(prose, -1) {
			if msg := checkLink(b, dir, file, m[3], anchors); msg != "" {
				probs = append(probs, problem{gate: "links", file: file, line: l.num, msg: msg})
			}
		}
	}
	return probs
}

// sharedDiagrams is the directory of the deep book's diagrams, which a short
// edition may link to and embed from its own chapters.
const sharedDiagrams = "../technical-reference/diagrams/"

func checkLink(b *Book, dir, file, target string, anchors map[string]map[string]bool) string {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return ""
	}
	path, frag, _ := strings.Cut(target, "#")
	dest := file
	if path != "" {
		dest = filepath.ToSlash(filepath.Clean(filepath.Join(dir, path)))
	}
	known, ok := anchors[dest]
	if !ok && strings.HasPrefix(dest, sharedDiagrams) && fileExists(b.Path(dest)) {
		return ""
	}
	if !ok {
		return fmt.Sprintf("link %s points at %s, which is not a book file", target, dest)
	}
	if frag != "" && !known[frag] {
		return fmt.Sprintf("link %s: %s has no heading with anchor #%s", target, dest, frag)
	}
	return ""
}
