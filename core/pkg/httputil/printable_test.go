package httputil

import "testing"

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain text 123":           "plain text 123",
		"":                         "",
		"red \x1b[31mred\x1b[0m":   "red [31mred[0m",
		"line\nbreak\rreturn\ttab": "linebreakreturntab",
		"bell\a nul\x00 del\x7f":   "bell nul del",
		"c1 \u009b31m":             "c1 31m",
		"override ‮gnp.exe":        "override gnp.exe",
		"zero​width⁠joiner":        "zerowidthjoiner",
		"ünïcödé ✓":                "ünïcödé ✓",
		"bad \xff\xfe utf8":        "bad  utf8",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintableMax(t *testing.T) {
	if got := PrintableMax("héllo\x1bworld", 7); got != "héllowo" {
		t.Errorf("got %q", got)
	}
	if got := PrintableMax("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := PrintableMax("anything", 0); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestOneLine(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"one line is unchanged":         {"plain text 123", "plain text 123"},
		"empty":                         {"", ""},
		"only line breaks":              {"\n\r\n  \n", ""},
		"lines are kept apart":          {"first\nsecond\r\nthird", "first | second | third"},
		"blank lines and edges dropped": {"\n\n  a  \n\n\n b \n", "a | b"},
		"tabs are spaces":               {"a\tb", "a b"},
		"unicode separators end lines":  {"a b c\u0085d", "a | b | c | d"},
		"escape sequences are removed":  {"ok\x1b[2J\nforged\a", "ok[2J | forged"},
		"format characters are removed": {"gnp‮.exe\nzero​width", "gnp.exe | zerowidth"},
		"a line of only controls drops": {"a\n\x1b\x07\nb", "a | b"},
		"invalid utf-8 is removed":      {"bad \xff\xfe utf8", "bad  utf8"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := OneLine(tc.in); got != tc.want {
				t.Errorf("OneLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
