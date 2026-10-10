package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/operatorview"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// stubSource is a Source whose snapshot is fixed and which never streams.
type stubSource struct {
	mode monitor.Mode
	snap *cluster.ClusterSnapshot
}

func (s stubSource) Mode() monitor.Mode { return s.mode }
func (s stubSource) Snapshot(context.Context) (*cluster.ClusterSnapshot, error) {
	return s.snap, nil
}
func (s stubSource) Watch(context.Context, time.Duration) <-chan monitor.Update { return nil }

var fixedNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func testSnapshot() *cluster.ClusterSnapshot {
	snap := &cluster.ClusterSnapshot{Environment: "devnet", CollectedAt: fixedNow.Add(-2 * time.Second)}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		state := "Follower"
		if i == 0 {
			state = "Leader"
		}
		snap.Nodes = append(snap.Nodes, cluster.CollectionStatus{Node: cluster.NodeRef{Host: host}, Report: &report.NodeReport{
			PublicIP:  host,
			Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: 200},
			RQLite:    &report.RQLiteReport{Responsive: true, RaftState: state},
			Olric:     &report.OlricReport{ServiceActive: true, MemberlistUp: true},
			IPFS:      &report.IPFSReport{DaemonActive: true, ClusterActive: true},
			Vault:     &report.VaultReport{ServiceActive: true, Responsive: true},
			WireGuard: &report.WireGuardReport{InterfaceUp: true},
			Traffic:   &report.TrafficReport{RPS: float64(i + 1)},
		}})
	}
	return snap
}

func testModel() model {
	m := newModel(tui(stubSource{mode: monitor.ModeAPI, snap: testSnapshot()}), view.NewTheme(false), func() time.Time { return fixedNow })
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	return next.(model)
}

func tui(src monitor.Source) Config {
	return Config{Source: src, Env: "devnet", Interval: 5 * time.Second}
}

func withSnapshot(m model) model {
	next, _ := m.Update(updateMsg{u: monitor.Update{State: monitor.LinkLive, Snapshot: testSnapshot()}, ok: true})
	return next.(model)
}

func press(m model, k tea.KeyMsg) model {
	next, _ := m.Update(k)
	return next.(model)
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestUpdate_snapshotIsShownAndItsTrafficRecorded(t *testing.T) {
	m := withSnapshot(testModel())
	if m.snap == nil || m.link.state != monitor.LinkLive {
		t.Fatalf("snapshot not applied: link %v", m.link.state)
	}
	if got := m.rps.Values(); len(got) != 1 || got[0] != 6 {
		t.Fatalf("rps history = %v, want the cluster total 6", got)
	}
	if v := m.View(); !strings.Contains(v, "✓ All systems operational · 3/3 nodes · updated 0s ago") || !strings.Contains(v, "● live") {
		t.Fatalf("view does not lead with the verdict:\n%s", v)
	}
}

func TestUpdate_serverErrorKeepsTheSnapshot(t *testing.T) {
	m := withSnapshot(testModel())
	next, _ := m.Update(updateMsg{u: monitor.Update{State: monitor.LinkLive, Err: errors.New("peer timed out")}, ok: true})
	m = next.(model)
	if m.snap == nil || m.notice == nil || !strings.Contains(m.View(), "! peer timed out") {
		t.Fatal("a gateway-reported error replaced the data or was not shown")
	}
}

func TestUpdate_reconnectShowsTheLinkAndAgesTheData(t *testing.T) {
	m := withSnapshot(testModel())
	next, _ := m.Update(updateMsg{u: monitor.Update{State: monitor.LinkReconnecting, Err: errors.New("stream closed"),
		RetryIn: 4 * time.Second, Attempt: 2}, ok: true})
	m = next.(model)
	v := m.View()
	if !strings.Contains(v, "↻ reconnecting in 4s (attempt 2): stream closed") || strings.Contains(v, "STALE") {
		t.Fatalf("seconds-old data during a reconnect:\n%s", v)
	}
	m.now = func() time.Time { return fixedNow.Add(time.Minute) }
	if v := m.View(); !strings.Contains(v, "STALE: updated 1m00s ago") {
		t.Fatalf("old data during a reconnect was not marked stale:\n%s", v)
	}
}

func TestUpdate_sourceStoppedIsAFailure(t *testing.T) {
	next, cmd := testModel().Update(updateMsg{ok: false})
	m := next.(model)
	if m.link.state != monitor.LinkFailed || cmd != nil {
		t.Fatalf("link %v, cmd %v", m.link.state, cmd)
	}
	if !strings.Contains(m.View(), "--ssh") {
		t.Fatal("a failed source without data does not point at --ssh")
	}
}

func TestHandleKey_tabs(t *testing.T) {
	m := withSnapshot(testModel())
	if m = press(m, runes("5")); m.tab != tabTraffic {
		t.Fatalf("4 went to %v", m.tab)
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyTab}); m.tab != tabChain {
		t.Fatalf("tab went to %v", m.tab)
	}
	if m = press(m, runes("1")); m.tab != tabOverview {
		t.Fatalf("1 went to %v", m.tab)
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyShiftTab}); m.tab != tabAlerts {
		t.Fatalf("shift+tab from the first tab went to %v, want the last", m.tab)
	}
}

func TestHandleKey_helpAndQuit(t *testing.T) {
	m := press(testModel(), runes("?"))
	if !m.showHelp || !strings.Contains(m.viewport.View(), "toggle help") {
		t.Fatal("? did not open the help")
	}
	if m = press(m, runes("4")); m.tab != tabOverview {
		t.Fatal("keys acted behind the help overlay")
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyEsc}); m.showHelp {
		t.Fatal("esc did not close the help")
	}
	cancelled := false
	m.cancel = func() { cancelled = true }
	if _, cmd := m.Update(runes("q")); cmd == nil || !cancelled {
		t.Fatal("q did not quit and stop the source")
	}
}

func TestHandleTabKey_nodeSelectionAndDetail(t *testing.T) {
	m := press(withSnapshot(testModel()), runes("3"))
	down := tea.KeyMsg{Type: tea.KeyDown}
	for range 5 {
		m = press(m, down)
	}
	if m.nodeCursor != 2 {
		t.Fatalf("cursor = %d, want it clamped to the last node", m.nodeCursor)
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyEnter}); !m.nodeDetail || !strings.Contains(m.viewport.View(), "3.3.3.3") {
		t.Fatal("enter did not open the selected node")
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyEsc}); m.nodeDetail {
		t.Fatal("esc did not go back")
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyUp}); m.nodeCursor != 1 {
		t.Fatalf("up: cursor = %d", m.nodeCursor)
	}
}

func TestHandleTabKey_enterWithoutDataOpensNothing(t *testing.T) {
	m := press(press(testModel(), runes("3")), tea.KeyMsg{Type: tea.KeyEnter})
	if m.nodeDetail {
		t.Fatal("a node detail opened with no snapshot")
	}
}

func TestHandleTabKey_alertFiltersOnlyOnTheAlertsTab(t *testing.T) {
	m := press(withSnapshot(testModel()), runes("c"))
	if m.alertFilter != view.FilterAll {
		t.Fatal("c changed the alert filter outside the Alerts tab")
	}
	m = press(press(m, runes("0")), runes("w"))
	if m.alertFilter != view.FilterWarning || !strings.Contains(m.footer(), "filter: warning") {
		t.Fatalf("w: filter %q", m.alertFilter)
	}
	if m = press(m, runes("a")); m.alertFilter != view.FilterAll {
		t.Fatal("a did not clear the filter")
	}
}

func TestTabContent_everyTabWithAndWithoutData(t *testing.T) {
	empty := testModel()
	full := withSnapshot(testModel())
	for tb := tabOverview; tb < tabCount; tb++ {
		empty.tab, full.tab = tb, tb
		if out := empty.tabContent(80); !strings.Contains(out, "Collecting") {
			t.Errorf("%s without data: %q", tabNames[tb], out)
		}
		if out := full.tabContent(80); out == "" {
			t.Errorf("%s rendered nothing", tabNames[tb])
		}
	}
}

// The Operator tab says how to name an account when there is none, waits for the first reading, then
// shows the account; a reading schedules the next one.
func TestOperatorTab_hintReadingThenAccount(t *testing.T) {
	m := withSnapshot(testModel())
	m.tab = tabOperator
	if out := m.tabContent(80); !strings.Contains(out, "--operator") {
		t.Fatalf("no operator configured: %q", out)
	}
	if m.operatorCmd(0) != nil {
		t.Fatal("a model with no operator reads an account")
	}

	m.cfg.Operator = func(context.Context) operatorview.Summary {
		return operatorview.Summary{Address: "orama1abc", Earnings: "12", Spendable: "3", Bonded: "1000"}
	}
	if out := m.tabContent(80); !strings.Contains(out, "Reading") {
		t.Fatalf("before the first reading: %q", out)
	}
	msg := m.operatorCmd(0)()
	next, cmd := m.Update(msg)
	m = next.(model)
	out := m.tabContent(80)
	for _, want := range []string{"orama1abc", "12 norama", "3 norama", "1000 norama"} {
		if !strings.Contains(out, want) {
			t.Errorf("Operator tab misses %q: %q", want, out)
		}
	}
	if cmd == nil {
		t.Error("a reading did not schedule the next one")
	}
}
