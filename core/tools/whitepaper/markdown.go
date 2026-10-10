package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// mdLine is one line of a chapter with whether it sits inside a fenced code
// block.
type mdLine struct {
	num    int // 1-based
	text   string
	inCode bool
}

// heading is an ATX heading outside code.
type heading struct {
	level int
	text  string
	line  int
}

var (
	codeSpanRe = regexp.MustCompile("`[^`]*`")
	imageRe    = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	linkRe     = regexp.MustCompile(`(^|[^!])\[([^\]]*)\]\(([^)\s]+)\)`)
	headingRe  = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
)

// splitLines splits a document and marks fenced code blocks. The fence lines
// themselves count as code.
func splitLines(doc string) []mdLine {
	var out []mdLine
	inCode := false
	fence := ""
	for i, text := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(text)
		if !inCode && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inCode, fence = true, trimmed[:3]
			out = append(out, mdLine{num: i + 1, text: text, inCode: true})
			continue
		}
		if inCode && strings.HasPrefix(trimmed, fence) {
			inCode = false
			out = append(out, mdLine{num: i + 1, text: text, inCode: true})
			continue
		}
		out = append(out, mdLine{num: i + 1, text: text, inCode: inCode})
	}
	return out
}

// headings returns the ATX headings outside code.
func headings(lines []mdLine) []heading {
	var hs []heading
	for _, l := range lines {
		if l.inCode {
			continue
		}
		if m := headingRe.FindStringSubmatch(l.text); m != nil {
			hs = append(hs, heading{level: len(m[1]), text: m[2], line: l.num})
		}
	}
	return hs
}

// codeSpans returns the inline code spans of a line, without backticks.
func codeSpans(text string) []string {
	var out []string
	for _, s := range codeSpanRe.FindAllString(text, -1) {
		out = append(out, strings.Trim(s, "`"))
	}
	return out
}

// stripCodeSpans removes inline code spans so prose checks do not trip on
// code.
func stripCodeSpans(text string) string {
	return codeSpanRe.ReplaceAllString(text, "")
}

// slugger produces GitHub-style heading anchors, the same ones rehype-slug
// (website) and pandoc's gfm_auto_identifiers (print) generate.
type slugger struct{ seen map[string]int }

func newSlugger() *slugger { return &slugger{seen: map[string]int{}} }

func (s *slugger) slug(text string) string {
	base := githubSlug(text)
	n, dup := s.seen[base]
	s.seen[base] = n + 1
	if !dup {
		return base
	}
	return base + "-" + strconv.Itoa(n)
}

// githubSlug lower-cases, drops everything but letters, digits, spaces,
// hyphens and underscores, and turns spaces into hyphens. Inline code and
// emphasis markers are dropped first, as the renderers see plain text.
func githubSlug(text string) string {
	text = strings.NewReplacer("`", "", "*", "").Replace(text)
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// anchorsOf returns every heading anchor a document defines.
func anchorsOf(lines []mdLine) map[string]bool {
	s := newSlugger()
	out := map[string]bool{}
	for _, h := range headings(lines) {
		out[s.slug(h.text)] = true
	}
	return out
}
