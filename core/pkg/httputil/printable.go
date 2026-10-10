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
