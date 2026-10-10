package httputil

import (
	"strings"
	"unicode"
)

// Printable returns s without the characters that act on a terminal or hide text in it: control
// characters (escape sequences among them), and the invisible format characters that reorder or
// hide what is shown (a right-to-left override, zero-width characters). Text a chain, a gateway or
// a node sent is untrusted, and it reaches an operator's terminal in errors and in the prompt they
// approve a transaction from: run it through Printable first. It is the one implementation; do not
// write another.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar {
			return -1
		}
		return r
	}, s)
}

// PrintableMax is Printable cut to at most max runes.
func PrintableMax(s string, max int) string {
	runes := []rune(Printable(s))
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

// LineJoiner separates the lines of a multi-line text that OneLine puts on one line.
const LineJoiner = " | "

// OneLine is Printable for text that may span several lines, such as the stderr of a
// remote command or the log of a refused transaction. Printable removes the line breaks
// and so runs the last word of one line into the first of the next; OneLine keeps the
// lines apart, trimmed and joined with " | ", so the text cannot start a line of its
// own in an error and still reads as it was written.
func OneLine(s string) string {
	lines := strings.FieldsFunc(s, isLineBreak)
	kept := lines[:0]
	for _, line := range lines {
		if line = strings.TrimSpace(Printable(strings.ReplaceAll(line, "\t", " "))); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, LineJoiner)
}

// isLineBreak reports whether r ends a line: LF, CR, VT, FF, NEL and the Unicode line and
// paragraph separators.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', ' ', ' ':
		return true
	}
	return false
}
