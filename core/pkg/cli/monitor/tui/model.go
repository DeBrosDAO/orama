package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

const (
	tabOverview = iota
	tabNodes
	tabServices
	tabMesh
	tabDNS
	tabNamespaces
	tabAlerts
	tabCount
)

var tabNames = []string{"Overview", "Nodes", "Services", "WG Mesh", "DNS", "Namespaces", "Alerts"}

// snapshotMsg carries the result of a background collection.
type snapshotMsg struct {
	snap *monitor.ClusterSnapshot
	err  error
}

// tickMsg fires on each refresh interval.
type tickMsg time.Time

// model is the root Bubbletea model for the Orama monitor TUI.
type model struct {
	cfg        monitor.CollectorConfig
	interval   time.Duration
	activeTab  int
	viewport   viewport.Model
	width      int
	height     int
	snapshot   *monitor.ClusterSnapshot
	loading    bool
	lastError  error
	lastUpdate time.Time
	quitting   bool
}

// newModel creates a fresh model with default viewport dimensions.
func newModel(cfg monitor.CollectorConfig, interval time.Duration) model {
	vp := viewport.New(80, 24)
	return model{
		cfg:      cfg,
		interval: interval,
		viewport: vp,
		loading:  true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(doCollect(m.cfg), tickCmd(m.interval))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.String() == "q" || msg.String() == "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case msg.String() == "tab" || msg.String() == "l":
			m.activeTab = (m.activeTab + 1) % tabCount
			m.updateContent()
			m.viewport.GotoTop()
			return m, nil

		case msg.String() == "shift+tab" || msg.String() == "h":
			m.activeTab = (m.activeTab - 1 + tabCount) % tabCount
			m.updateContent()
			m.viewport.GotoTop()
			return m, nil

		case msg.String() == "r":
			if !m.loading {
				m.loading = true
				return m, doCollect(m.cfg)
			}
			return m, nil

		default:
			// Delegate scrolling to viewport
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve 4 lines: header, tab bar, blank separator, footer
		vpHeight := msg.Height - 4
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.Width = msg.Width
		m.viewport.Height = vpHeight
		m.updateContent()
		return m, nil

	case snapshotMsg:
		m.loading = false
		if msg.err != nil {
			m.lastError = msg.err
		} else {
			m.snapshot = msg.snap
			m.lastError = nil
			m.lastUpdate = time.Now()
		}
		m.updateContent()
		return m, nil

	case tickMsg:
		if !m.loading {
			m.loading = true
			cmds = append(cmds, doCollect(m.cfg))
		}
		cmds = append(cmds, tickCmd(m.interval))
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	// Header
	var header string
	if m.snapshot != nil {
		ago := time.Since(m.lastUpdate).Truncate(time.Second)
		header = headerStyle.Render(fmt.Sprintf(
			"Orama Monitor — %s — Last: %s (%s ago)",
			m.snapshot.Environment,
			m.lastUpdate.Format("15:04:05"),
			ago,
		))
	} else if m.loading {
		header = headerStyle.Render("Orama Monitor — collecting...")
	} else if m.lastError != nil {
		header = headerStyle.Render(fmt.Sprintf("Orama Monitor — error: %v", m.lastError))
	} else {
		header = headerStyle.Render("Orama Monitor")
	}

	if m.loading && m.snapshot != nil {
		header += styleMuted.Render("  (refreshing...)")
	}

	// Tab bar
	tabs := renderTabBar(m.activeTab, m.width)

	// Footer
	footer := footerStyle.Render("tab: switch | j/k: scroll | r: refresh | q: quit")

	return header + "\n" + tabs + "\n" + m.viewport.View() + "\n" + footer
}

// updateContent renders the active tab and sets it on the viewport.
func (m *model) updateContent() {
	w := m.width
	if w == 0 {
		w = 80
	}

	var content string
	switch m.activeTab {
	case tabOverview:
		content = renderOverview(m.snapshot, w)
	case tabNodes:
		content = renderNodes(m.snapshot, w)
	case tabServices:
		content = renderServicesTab(m.snapshot, w)
	case tabMesh:
		content = renderWGMesh(m.snapshot, w)
	case tabDNS:
		content = renderDNSTab(m.snapshot, w)
	case tabNamespaces:
		content = renderNamespacesTab(m.snapshot, w)
	case tabAlerts:
		content = renderAlertsTab(m.snapshot, w)
	}

	m.viewport.SetContent(content)
}

// doCollect returns a tea.Cmd that runs monitor.CollectOnce in a goroutine.
func doCollect(cfg monitor.CollectorConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		snap, err := monitor.CollectOnce(ctx, cfg)
		return snapshotMsg{snap: snap, err: err}
	}
}

// tickCmd returns a tea.Cmd that fires a tickMsg after the given interval.
func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Run starts the TUI program with the given collector config.
func Run(cfg monitor.CollectorConfig) error {
	m := newModel(cfg, 30*time.Second)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
