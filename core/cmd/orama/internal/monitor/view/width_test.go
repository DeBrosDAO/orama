package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// red wraps s in a raw ANSI color, as a colored cell reaches the padding.
func red(s string) string { return "\x1b[31m" + s + "\x1b[0m" }

func TestPadRight_countsVisibleWidthNotBytes(t *testing.T) {
	colored := PadRight(red("OK"), 6)
	if w := lipgloss.Width(colored); w != 6 {
		t.Fatalf("colored cell padded to %d cells, want 6", w)
	}
	if plain := PadRight("OK", 6); plain != "OK    " {
		t.Fatalf("plain cell = %q", plain)
	}
}

func TestPadRight_edgeCases(t *testing.T) {
	if got := PadRight("", 3); got != "   " {
		t.Errorf("empty: %q", got)
	}
	if got := PadRight("toolong", 3); got != "toolong" {
		t.Errorf("wider than the column: %q", got)
	}
	if got := PadRight("✓ ok", 5); lipgloss.Width(got) != 5 {
		t.Errorf("multi-byte rune: width %d", lipgloss.Width(got))
	}
}

// The bug this replaces: fmt's %-Ns counts escape bytes, so a colored cell
// came out narrower than a plain one and every column after it shifted.
func TestTable_alignsColoredAndPlainCells(t *testing.T) {
	out := Table(NewTheme(false), "", []string{"HOST", "STATE", "AGE"}, [][]string{
		{"1.1.1.1", red("Leader"), "2s"},
		{"2.2.2.2", "Follower", "3s"},
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines: %q", len(lines), out)
	}
	col := func(line, cell string) int { return lipgloss.Width(line[:strings.Index(line, cell)]) }
	if col(lines[1], "2s") != col(lines[2], "3s") || col(lines[0], "AGE") != col(lines[2], "3s") {
		t.Fatalf("the last column does not line up:\n%s", out)
	}
}

func TestTable_shortRowsAndNoRows(t *testing.T) {
	out := Table(NewTheme(false), "  ", []string{"A", "B", "C"}, [][]string{{"x"}})
	if !strings.Contains(out, "  x\n") {
		t.Fatalf("a short row was not written: %q", out)
	}
	if out := Table(NewTheme(false), "", []string{"A"}, nil); out != "A\n" {
		t.Fatalf("header only: %q", out)
	}
}

func TestTruncate_boundsAndEllipsis(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 8, "this is…"},
		{"anything", 0, ""},
	}
	for _, tc := range cases {
		if got := Truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
