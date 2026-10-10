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
