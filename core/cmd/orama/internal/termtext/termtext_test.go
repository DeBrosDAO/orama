package termtext

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":            "plain text",
		"line\nand\ttab":        "line\nand\ttab",
		"\x1b]0;owned\x07 done": "?]0;owned? done",
		"bidi‮reversed":         "bidi?reversed",
		"":                      "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
