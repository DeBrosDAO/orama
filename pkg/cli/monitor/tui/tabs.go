package tui

import "strings"

// renderTabBar renders the tab bar with the active tab highlighted.
func renderTabBar(active int, width int) string {
	var parts []string
	for i, name := range tabNames {
		if i == active {
			parts = append(parts, activeTab.Render(name))
		} else {
			parts = append(parts, inactiveTab.Render(name))
		}
	}

	bar := strings.Join(parts, styleMuted.Render(" | "))

	// Pad to full width if needed
	if width > 0 {
		rendered := stripAnsi(bar)
		if len(rendered) < width {
			bar += strings.Repeat(" ", width-len(rendered))
		}
	}

	return bar
}

// stripAnsi removes ANSI escape codes for length calculation.
func stripAnsi(s string) string {
	var out []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEsc = false
			}
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
