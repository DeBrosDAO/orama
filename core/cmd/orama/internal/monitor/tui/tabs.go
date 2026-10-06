package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
)

// tab is one page of the live view.
type tab int

const (
	tabOverview tab = iota
	tabNodes
	tabServices
	tabTraffic
	tabChain
	tabMesh
	tabDNS
	tabNamespaces
	tabAlerts
	tabCount
)

var tabNames = [tabCount]string{"Overview", "Nodes", "Services", "Traffic", "Chain", "Mesh", "DNS", "Namespaces", "Alerts"}

// next is the tab delta steps away, wrapping at either end.
func (t tab) next(delta int) tab {
	return tab((int(t) + delta%int(tabCount) + int(tabCount)) % int(tabCount))
}

// tabForKey is the tab a digit key jumps to: "1" is the first tab.
func tabForKey(k string) (tab, bool) {
	if len(k) != 1 || k[0] < '1' || k[0] > '9' {
		return 0, false
	}
	t := tab(k[0] - '1')
	return t, t < tabCount
}

// renderTabBar numbers each tab so the digit that jumps to it is visible. When
// the full bar is wider than the terminal, inactive tabs shrink to their digit.
func renderTabBar(t view.Theme, active tab, width int) string {
	bar := tabBar(t, active, false)
	if width > 0 && lipgloss.Width(bar) > width {
		bar = tabBar(t, active, true)
	}
	return bar
}

func tabBar(t view.Theme, active tab, compact bool) string {
	parts := make([]string, 0, tabCount)
	for i, name := range tabNames {
		digit := string(rune('1' + i))
		switch {
		case tab(i) == active:
			parts = append(parts, t.TabActive.Render(digit+" "+name))
		case compact:
			parts = append(parts, t.TabInactive.Render(digit))
		default:
			parts = append(parts, t.TabInactive.Render(digit+" "+name))
		}
	}
	return strings.Join(parts, t.Muted.Render("│"))
}
