package view

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// columnGap is the space between table columns.
const columnGap = "  "

// ellipsis marks a truncated cell.
const ellipsis = "…"

// PadRight pads s with spaces to width visible cells. It measures with
// lipgloss.Width, so a string carrying ANSI color codes lines up with a plain
// one: fmt's %-Ns counts the escape bytes and misaligns every colored cell.
func PadRight(s string, width int) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// Truncate shortens plain text to at most max visible cells, marking the cut.
// It is for unstyled text such as hosts and error messages; style after
// truncating.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+lipgloss.Width(ellipsis) > max {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + ellipsis
}

// Table renders rows under headers with every column as wide as its widest
// visible cell. Cells may carry ANSI styles. Each line starts with indent.
func Table(t Theme, indent string, headers []string, rows [][]string) string {
	widths := columnWidths(headers, rows)
	var b strings.Builder
	head := make([]string, len(headers))
	for i, h := range headers {
		head[i] = t.Header.Render(h)
	}
	writeRow(&b, indent, head, widths)
	for _, r := range rows {
		writeRow(&b, indent, r, widths)
	}
	return b.String()
}

func columnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && lipgloss.Width(c) > widths[i] {
				widths[i] = lipgloss.Width(c)
			}
		}
	}
	return widths
}

func writeRow(b *strings.Builder, indent string, cells []string, widths []int) {
	b.WriteString(indent)
	for i, c := range cells {
		if i >= len(widths) {
			break
		}
		if i == len(cells)-1 || i == len(widths)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(PadRight(c, widths[i]))
		b.WriteString(columnGap)
	}
	b.WriteString("\n")
}

// Rule is a horizontal line of the given width.
func Rule(t Theme, width int) string {
	if width <= 0 {
		width = DefaultWidth
	}
	return t.Muted.Render(strings.Repeat("─", width))
}

// DefaultWidth is the width used before a terminal reports its own.
const DefaultWidth = 80
