// Package termtext makes text from elsewhere (a node, a log, a certificate authority) safe to print
// on the operator's terminal.
package termtext

import (
	"strings"
	"unicode"
)

// Clean replaces every control, format and line-separator character but newline and tab with '?':
// such text could otherwise move the cursor, retitle the window or rewrite what the terminal shows.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), r == unicode.ReplacementChar:
			return '?'
		}
		return r
	}, s)
}
