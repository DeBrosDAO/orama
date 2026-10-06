package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = max(msg.Height-chromeLines, 1)
		m.render()
		return m, nil
	case updateMsg:
		return m.handleUpdate(msg)
	case refreshMsg:
		m.refreshing = false
		switch {
		case msg.err != nil:
			m.notice = msg.err
		case m.snap == nil || !msg.snap.CollectedAt.Before(m.snap.CollectedAt):
			// A slow refresh must not replace a newer streamed snapshot.
			m.applySnapshot(msg.snap)
		}
		m.render()
		return m, nil
	case tickMsg:
		m.render()
		return m, tick()
	}
	return m, nil
}

// handleUpdate applies one event from the source and waits for the next.
func (m model) handleUpdate(msg updateMsg) (tea.Model, tea.Cmd) {
	if !msg.ok {
		if m.link.state != monitor.LinkFailed {
			m.link = linkStatus{state: monitor.LinkFailed, err: errSourceStopped, since: m.now()}
		}
		m.render()
		return m, nil
	}
	u := msg.u
	switch {
	case u.Snapshot != nil:
		m.link = fromUpdate(u, m.now())
		m.applySnapshot(u.Snapshot)
		m.recordTraffic(u.Snapshot)
	case u.State == monitor.LinkLive && u.Err != nil:
		// The stream is up and the gateway reported a problem: the link is
		// live even when this is the first event on a new connection.
		m.link = linkStatus{state: monitor.LinkLive, since: m.now()}
		m.notice = u.Err
	default:
		m.link = fromUpdate(u, m.now())
	}
	m.render()
	return m, waitForUpdate(m.updates)
}

// applySnapshot shows a new snapshot, keeping the node selection on the same
// host when it is still there.
func (m *model) applySnapshot(snap *cluster.ClusterSnapshot) {
	m.snap = snap
	m.receivedAt = m.now()
	m.notice = nil
	if i := nodeIndex(snap, m.selectedHost); i >= 0 {
		m.nodeCursor = i
		return
	}
	m.nodeCursor = min(m.nodeCursor, max(len(snap.Nodes)-1, 0))
	if !m.nodeDetail {
		m.selectedHost = selectedHostAt(snap, m.nodeCursor)
	}
}

// recordTraffic adds a streamed snapshot's cluster rps to the sparkline. A
// manual refresh is not recorded, so the samples stay one per interval.
func (m *model) recordTraffic(snap *cluster.ClusterSnapshot) {
	if totals := view.AggregateTraffic(snap).Totals; totals.Reporting > 0 {
		m.rps.Push(totals.RPS)
	}
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case m.showHelp:
		if key.Matches(msg, keys.Help, keys.Back) {
			m.showHelp = false
			m.render()
		}
		return m, nil
	case key.Matches(msg, keys.Help):
		m.showHelp = true
		m.render()
		return m, nil
	case key.Matches(msg, keys.NextTab):
		m.setTab(m.tab.next(1))
	case key.Matches(msg, keys.PrevTab):
		m.setTab(m.tab.next(-1))
	case key.Matches(msg, keys.JumpTab):
		if t, ok := tabForKey(msg.String()); ok {
			m.setTab(t)
		}
	case key.Matches(msg, keys.Refresh):
		if m.refreshing {
			return m, nil
		}
		m.refreshing = true
		m.render()
		return m, m.refreshCmd()
	default:
		return m.handleTabKey(msg)
	}
	return m, nil
}

// handleTabKey handles the keys whose meaning depends on the tab: selection
// and drill-down on Nodes, severity filters on Alerts, scrolling elsewhere.
func (m model) handleTabKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.tab == tabNodes && !m.nodeDetail && key.Matches(msg, keys.Up, keys.Down):
		m.moveCursor(msg)
	case m.tab == tabNodes && !m.nodeDetail && key.Matches(msg, keys.Enter):
		m.selectedHost = selectedHostAt(m.snap, m.nodeCursor)
		m.nodeDetail = m.selectedHost != ""
		m.render()
		m.viewport.GotoTop()
	case m.tab == tabNodes && m.nodeDetail && key.Matches(msg, keys.Back):
		m.nodeDetail = false
		m.selectedHost = selectedHostAt(m.snap, m.nodeCursor)
		m.render()
		m.scrollToCursor()
	case m.tab == tabAlerts && key.Matches(msg, keys.Critical, keys.Warning, keys.Info, keys.All):
		m.alertFilter = filterForKey(msg)
		m.render()
		m.viewport.GotoTop()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) setTab(t tab) {
	m.tab = t
	m.nodeDetail = false
	m.render()
	m.viewport.GotoTop()
}

func (m *model) moveCursor(msg tea.KeyMsg) {
	if m.snap == nil || len(m.snap.Nodes) == 0 {
		return
	}
	if key.Matches(msg, keys.Up) {
		m.nodeCursor = max(m.nodeCursor-1, 0)
	} else {
		m.nodeCursor = min(m.nodeCursor+1, len(m.snap.Nodes)-1)
	}
	m.selectedHost = selectedHostAt(m.snap, m.nodeCursor)
	m.render()
	m.scrollToCursor()
}

// scrollToCursor keeps the selected node's row inside the viewport.
func (m *model) scrollToCursor() {
	line := nodeTableFirstRow + m.nodeCursor
	switch {
	case line < m.viewport.YOffset:
		m.viewport.SetYOffset(line)
	case line >= m.viewport.YOffset+m.viewport.Height:
		m.viewport.SetYOffset(line - m.viewport.Height + 1)
	}
}

func filterForKey(msg tea.KeyMsg) view.SeverityFilter {
	switch {
	case key.Matches(msg, keys.Critical):
		return view.FilterCritical
	case key.Matches(msg, keys.Warning):
		return view.FilterWarning
	case key.Matches(msg, keys.Info):
		return view.FilterInfo
	default:
		return view.FilterAll
	}
}
