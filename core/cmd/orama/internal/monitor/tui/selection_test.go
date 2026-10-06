package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

func stream(m model, snap *cluster.ClusterSnapshot) model {
	next, _ := m.Update(updateMsg{u: monitor.Update{State: monitor.LinkLive, Snapshot: snap}, ok: true})
	return next.(model)
}

// The selection follows the host, not the row, when a snapshot reorders nodes.
func TestApplySnapshot_selectionFollowsTheHost(t *testing.T) {
	m := press(press(withSnapshot(testModel()), runes("2")), tea.KeyMsg{Type: tea.KeyDown})
	reordered := testSnapshot()
	reordered.Nodes[0], reordered.Nodes[1] = reordered.Nodes[1], reordered.Nodes[0]
	m = stream(m, reordered)
	if m.nodeCursor != 0 || m.selectedHost != "2.2.2.2" {
		t.Fatalf("cursor %d on %q, want 0 on 2.2.2.2", m.nodeCursor, m.selectedHost)
	}
}

// A node that leaves the snapshot while its detail is open, or a snapshot with
// no nodes at all, must not crash the view.
func TestNodeDetailContent_nodeLeavesTheSnapshot(t *testing.T) {
	m := press(press(press(withSnapshot(testModel()), runes("2")), tea.KeyMsg{Type: tea.KeyDown}), tea.KeyMsg{Type: tea.KeyEnter})
	shorter := testSnapshot()
	shorter.Nodes = shorter.Nodes[:1]
	m = stream(m, shorter)
	if v := m.viewport.View(); !strings.Contains(v, "2.2.2.2 is not in the latest snapshot") {
		t.Fatalf("detail for a departed node:\n%s", v)
	}
	empty := testSnapshot()
	empty.Nodes = nil
	m = stream(m, empty)
	_ = m.View()
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.nodeDetail || m.nodeCursor != 0 || m.selectedHost != "" {
		t.Fatalf("after esc on an empty snapshot: detail %v cursor %d host %q", m.nodeDetail, m.nodeCursor, m.selectedHost)
	}
	if m = press(m, tea.KeyMsg{Type: tea.KeyEnter}); m.nodeDetail {
		t.Fatal("a detail opened on an empty snapshot")
	}
}

// A slow manual refresh must not replace a newer streamed snapshot or add a
// sparkline sample.
func TestUpdate_olderRefreshIsIgnored(t *testing.T) {
	m := withSnapshot(testModel())
	older := testSnapshot()
	older.CollectedAt = older.CollectedAt.Add(-time.Minute)
	older.Environment = "older"
	next, _ := m.Update(refreshMsg{snap: older})
	m = next.(model)
	if m.snap.Environment == "older" || len(m.rps.Values()) != 1 {
		t.Fatalf("an older refresh replaced the snapshot (env %q, %d samples)", m.snap.Environment, len(m.rps.Values()))
	}
	newer := testSnapshot()
	newer.CollectedAt = newer.CollectedAt.Add(time.Second)
	next, _ = m.Update(refreshMsg{snap: newer})
	if m = next.(model); m.snap != newer || len(m.rps.Values()) != 1 {
		t.Fatal("a newer refresh was not shown, or was recorded as a stream sample")
	}
}

// An error event as the first event of a connection means the stream is up.
func TestUpdate_errorEventMarksTheLinkLive(t *testing.T) {
	next, _ := testModel().Update(updateMsg{u: monitor.Update{State: monitor.LinkLive, Err: errors.New("peer timed out")}, ok: true})
	m := next.(model)
	if m.link.state != monitor.LinkLive || m.notice == nil {
		t.Fatalf("link %v notice %v", m.link.state, m.notice)
	}
}

// The age on screen runs from when the snapshot arrived here, so a gateway
// clock that is ahead or behind does not change it.
func TestVerdictLine_ageFromLocalReceiveTime(t *testing.T) {
	snap := testSnapshot()
	snap.CollectedAt = fixedNow.Add(10 * time.Minute) // a gateway clock far ahead
	m := stream(testModel(), snap)
	m.now = func() time.Time { return fixedNow.Add(3 * time.Second) }
	if v := m.verdictLine(); !strings.Contains(v, "updated 3s ago") || strings.Contains(v, "STALE") {
		t.Fatalf("got %q", v)
	}
	snap.CollectedAt = fixedNow.Add(-time.Hour) // far behind
	m = stream(testModel(), snap)
	if v := m.verdictLine(); strings.Contains(v, "STALE") {
		t.Fatalf("a gateway clock behind ours flagged fresh data: %q", v)
	}
}

func TestErrText_cleansControlCharacters(t *testing.T) {
	if got := errText(errors.New("bad\x1b[2J\x07news")); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("got %q", got)
	}
}
