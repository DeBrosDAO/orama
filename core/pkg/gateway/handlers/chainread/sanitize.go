package chainread

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	// logMaxRunes bounds the log a refused transaction is answered with.
	logMaxRunes = 512
	// codespaceMaxLen bounds a codespace; the SDK's are short lowercase words.
	codespaceMaxLen = 64

	redacted = "[redacted]"
)

var (
	// A node's own message can name where it runs: a file in its source tree or on its disk, an
	// address and port. A transaction's refusal is useful to a wallet without any of it. The SDK
	// wraps its own location in brackets ("[cosmos/cosmos-sdk@v0.54.4/baseapp/baseapp.go:1066]"),
	// which is replaced whole so no bracket is left over.
	sourceLocation = regexp.MustCompile(`\[[^\s\[\]]+\.go:\d+(?::\d+)?\]|[^\s\[\]]+\.go:\d+(?::\d+)?`)
	filesystemPath = regexp.MustCompile(`/(?:var|home|root|etc|tmp|opt|usr|srv|data|mnt|proc|run|lib)(?:/[\w.@+\-]+)*`)
	ipv4Address    = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`)
	ipv6Address    = regexp.MustCompile(`\[?(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}\]?(?::\d{1,5})?`)
	stackTrace     = regexp.MustCompile(`(?s)goroutine \d+.*`)
	codespacePat   = regexp.MustCompile(`^[A-Za-z0-9_.\-]*$`)
)

// sanitizeLog makes a node's log of a refused transaction safe to hand to any caller: a stack
// trace, source locations, filesystem paths and network addresses are replaced, control characters
// and invalid UTF-8 are dropped, and the result is cut to logMaxRunes. What is left is the SDK's
// own reason ("insufficient fees; got ... required ..."), which a wallet shows.
func sanitizeLog(log string) string {
	log = stackTrace.ReplaceAllString(log, redacted)
	log = sourceLocation.ReplaceAllString(log, redacted)
	log = filesystemPath.ReplaceAllString(log, redacted)
	log = ipv4Address.ReplaceAllString(log, redacted)
	log = ipv6Address.ReplaceAllString(log, redacted)
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(log, "") {
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := []rune(strings.TrimSpace(b.String()))
	if len(out) > logMaxRunes {
		out = out[:logMaxRunes]
	}
	return string(out)
}

// sanitizeCodespace returns the codespace if it has the shape of one, and "" otherwise.
func sanitizeCodespace(codespace string) string {
	if len(codespace) > codespaceMaxLen || !codespacePat.MatchString(codespace) {
		return ""
	}
	return codespace
}
