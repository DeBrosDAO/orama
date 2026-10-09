package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The words gate keeps a short edition short. A chapter's words are its prose:
// every line outside fenced code blocks and outside tables (lines starting
// with "|"), with Markdown syntax removed (heading, quote and list markers,
// image syntax, link targets) and inline code kept as words. A word is a
// whitespace-separated token holding at least one letter or digit.
var (
	wordImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	wordLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	wordMarkRe  = regexp.MustCompile(`^(\s*>)*\s*(#{1,6}\s+|[-*+]\s+|\d+[.)]\s+)?`)
)

// proseWords counts the prose words of a document.
func proseWords(lines []mdLine) int {
	n := 0
	for _, l := range lines {
		if l.inCode || strings.HasPrefix(strings.TrimSpace(l.text), "|") {
			continue
		}
		text := wordImageRe.ReplaceAllString(l.text, "")
		text = wordLinkRe.ReplaceAllString(text, "$1")
		text = wordMarkRe.ReplaceAllString(text, "")
		for _, tok := range strings.Fields(text) {
			if strings.IndexFunc(tok, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
				n++
			}
		}
	}
	return n
}

// checkWords is the words gate: a chapter with max_words must have prose and
// stay within it, and the chapters together stay within max_words_total.
func checkWords(b *Book, docs []document) []problem {
	var probs []problem
	total := 0
	for _, d := range docs {
		n := proseWords(d.lines)
		total += n
		if d.chapter == nil || d.chapter.MaxWords <= 0 {
			continue
		}
		switch {
		case n == 0:
			probs = append(probs, problem{gate: "words", file: d.file, msg: "has no prose; a chapter with max_words must say something"})
		case n > d.chapter.MaxWords:
			probs = append(probs, problem{gate: "words", file: d.file,
				msg: fmt.Sprintf("has %d words, over its max_words of %d: cut %d", n, d.chapter.MaxWords, n-d.chapter.MaxWords)})
		}
	}
	if max := b.Manifest.MaxWordsTotal; max > 0 && total > max {
		probs = append(probs, problem{gate: "words", file: "book.yaml",
			msg: fmt.Sprintf("the book has %d words, over max_words_total of %d: cut %d", total, max, total-max)})
	}
	return probs
}
