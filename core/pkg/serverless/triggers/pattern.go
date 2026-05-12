package triggers

import (
	"fmt"
	"strings"
)

// MaxPatternLength is the maximum allowed glob pattern length.
const MaxPatternLength = 256

// ValidatePattern checks that a glob pattern is well-formed.
// Empty patterns and unbalanced character classes return an error.
// Patterns longer than MaxPatternLength are rejected to keep DB scans bounded.
func ValidatePattern(p string) error {
	if p == "" {
		return fmt.Errorf("empty pattern")
	}
	if len(p) > MaxPatternLength {
		return fmt.Errorf("pattern too long: %d > %d", len(p), MaxPatternLength)
	}
	open := 0
	for i, c := range p {
		switch c {
		case '[':
			open++
		case ']':
			open--
			if open < 0 {
				return fmt.Errorf("unmatched ']' at position %d", i)
			}
		}
	}
	if open != 0 {
		return fmt.Errorf("unmatched '['")
	}
	return nil
}

// IsWildcard reports whether the pattern contains any glob metacharacter.
// Useful for choosing between exact-match cache keys and wildcard scans.
func IsWildcard(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// PatternMatches returns true when topic matches pattern under Orama's glob
// semantics:
//   - '*'  matches zero or more characters EXCEPT ':'
//   - '**' matches zero or more characters INCLUDING ':' (deep wildcard)
//   - '?'  matches exactly one character (any)
//   - '[abc]' / '[!abc]' character classes
//
// SQLite's GLOB is the first-pass filter (in pubsub_store.go); this
// post-filter enforces segment boundaries for single-'*' patterns since
// SQLite GLOB treats '*' as "any chars including separators".
func PatternMatches(pattern, topic string) bool {
	if strings.Contains(pattern, "**") {
		// Deep wildcards already accept across segment boundaries — SQLite GLOB
		// already accepted this row. No further filtering needed.
		return true
	}
	return strictGlobMatch(pattern, topic)
}

// strictGlobMatch implements glob matching where '*' does NOT cross ':'.
// Recursive backtracking matcher; bounded length keeps it cheap.
func strictGlobMatch(pattern, s string) bool {
	pi, si := 0, 0
	starPi, starSi := -1, -1

	for si < len(s) {
		if pi < len(pattern) {
			pc := pattern[pi]
			switch pc {
			case '?':
				pi++
				si++
				continue
			case '*':
				// Remember position so we can backtrack.
				starPi = pi
				starSi = si
				pi++
				continue
			case '[':
				end := strings.IndexByte(pattern[pi+1:], ']')
				if end < 0 {
					return false
				}
				class := pattern[pi+1 : pi+1+end]
				if matchClass(class, s[si]) {
					pi += end + 2
					si++
					continue
				}
			default:
				if pc == s[si] {
					pi++
					si++
					continue
				}
			}
		}
		// No match at this position — try to extend the last '*' if any,
		// but '*' must not cross a ':' segment separator.
		if starPi >= 0 && s[starSi] != ':' {
			starSi++
			pi = starPi + 1
			si = starSi
			continue
		}
		return false
	}

	// Consume any trailing '*' in the pattern.
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// matchClass reports whether c matches the SQLite-style character class body
// (between '[' and ']'). Supports negation with leading '!'.
func matchClass(class string, c byte) bool {
	if class == "" {
		return false
	}
	negate := false
	if class[0] == '!' {
		negate = true
		class = class[1:]
	}
	for i := 0; i < len(class); i++ {
		if class[i] == c {
			return !negate
		}
	}
	return negate
}
