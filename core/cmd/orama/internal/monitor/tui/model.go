// Package tui is the live `orama monitor` view: a full-screen terminal UI fed
// by a monitor.Source, with a tab per aspect of the cluster.
package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Live view constants.
const (
	// rpsHistoryPoints is how many cluster-rps samples the traffic sparkline
	// keeps: the session's recent history, in memory only.
	rpsHistoryPoints = 60
	// clockTick redraws the ages and countdowns on screen.
	clockTick = time.Second
	// chromeLines is the height of everything around the viewport: the title
	// and verdict lines, the tab bar and the footer.
	chromeLines = 4
	// refreshTimeout bounds a manual refresh.
	refreshTimeout = 90 * time.Second
)

// Config is what the live view shows and how often.
type Config struct {
	Source   monitor.Source
	Env      string
	Interval time.Duration
}

// model is the live view's state. bubbletea copies it on every update, so
// the fields are values or pointers that are safe to share.
type model struct {
	cfg      Config
	theme    view.Theme
	updates  <-chan monitor.Update
	cancel   context.CancelFunc
	now      func() time.Time
	viewport viewport.Model
	width    int
	height   int

	snap *cluster.ClusterSnapshot
	// receivedAt is when snap arrived here. Its age on screen is measured
	// from this local time, so a gateway whose clock differs from this
	// machine's neither hides stale data nor flags fresh data.
	receivedAt time.Time
	link       linkStatus
	notice     error
	refreshing bool
	rps        *view.Series

	tab        tab
	nodeCursor int
	// selectedHost is the node the cursor is on, so the selection (and an
	// open node detail) follows the host when a snapshot reorders or drops
	// nodes.
	selectedHost string
	nodeDetail   bool
	alertFilter  view.SeverityFilter
	showHelp     bool
}

// Run shows the live view until the operator quits.
func Run(cfg Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(cfg, view.ThemeFor(os.Stdout), time.Now)
	m.cancel = cancel
	m.updates = cfg.Source.Watch(ctx, cfg.Interval)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("run the monitor UI: %w", err)
	}
	return nil
}

func newModel(cfg Config, theme view.Theme, now func() time.Time) model {
	return model{
		cfg:      cfg,
		theme:    theme,
		now:      now,
		viewport: viewport.New(0, 0),
		rps:      view.NewSeries(rpsHistoryPoints),
		link:     linkStatus{state: monitor.LinkConnecting, since: now()},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(waitForUpdate(m.updates), tick())
}

// updateMsg carries one event from the source. ok is false once the source
// has stopped.
type updateMsg struct {
	u  monitor.Update
	ok bool
}

// refreshMsg is the result of a manual refresh.
type refreshMsg struct {
	snap *cluster.ClusterSnapshot
	err  error
}

// tickMsg redraws the clock-dependent parts of the screen.
type tickMsg time.Time

func waitForUpdate(ch <-chan monitor.Update) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		u, ok := <-ch
		return updateMsg{u: u, ok: ok}
	}
}

func tick() tea.Cmd {
	return tea.Tick(clockTick, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) refreshCmd() tea.Cmd {
	src := m.cfg.Source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		snap, err := src.Snapshot(ctx)
		return refreshMsg{snap: snap, err: err}
	}
}
