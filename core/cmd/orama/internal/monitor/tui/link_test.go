package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
)

func TestLinkLabel_eachState(t *testing.T) {
	th := view.NewTheme(false)
	now := fixedNow
	cases := []struct {
		name string
		mode monitor.Mode
		link linkStatus
		want string
	}{
		{"live", monitor.ModeAPI, linkStatus{state: monitor.LinkLive}, "● live"},
		{"live over ssh", monitor.ModeSSH, linkStatus{state: monitor.LinkLive}, "● live (ssh)"},
		{"connecting", monitor.ModeAPI, linkStatus{state: monitor.LinkConnecting}, "◌ connecting…"},
		{"connecting again", monitor.ModeAPI, linkStatus{state: monitor.LinkConnecting, attempt: 2}, "◌ connecting (attempt 3)…"},
		{"reconnect countdown", monitor.ModeAPI, linkStatus{state: monitor.LinkReconnecting, err: errors.New("closed"),
			retryIn: 8 * time.Second, attempt: 3, since: now.Add(-3 * time.Second)}, "↻ reconnecting in 5s (attempt 3): closed"},
		{"countdown never negative", monitor.ModeAPI, linkStatus{state: monitor.LinkReconnecting, retryIn: time.Second,
			attempt: 1, since: now.Add(-time.Minute)}, "↻ reconnecting in 0s (attempt 1): no reason given"},
		{"failed", monitor.ModeAPI, linkStatus{state: monitor.LinkFailed, err: errors.New("HTTP 401")}, "✗ stopped: HTTP 401"},
	}
	for _, tc := range cases {
		if got := linkLabel(th, tc.mode, tc.link, now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsStale_ageAndLinkState(t *testing.T) {
	interval := 5 * time.Second
	cases := []struct {
		name  string
		age   time.Duration
		mode  monitor.Mode
		state monitor.LinkState
		want  bool
	}{
		{"fresh", 2 * time.Second, monitor.ModeAPI, monitor.LinkLive, false},
		{"three intervals old", 16 * time.Second, monitor.ModeAPI, monitor.LinkLive, true},
		{"ssh allows the collection time", 40 * time.Second, monitor.ModeSSH, monitor.LinkLive, false},
		{"reconnecting with fresh data", 3 * time.Second, monitor.ModeAPI, monitor.LinkReconnecting, false},
		{"reconnecting with old data", 20 * time.Second, monitor.ModeAPI, monitor.LinkConnecting, true},
		{"link failed", 0, monitor.ModeAPI, monitor.LinkFailed, true},
	}
	for _, tc := range cases {
		if got := isStale(tc.age, interval, tc.mode, tc.state); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTabNext_wraps(t *testing.T) {
	if tabOverview.next(-1) != tabAlerts || tabAlerts.next(1) != tabOverview || tabNodes.next(2) != tabTraffic {
		t.Fatal("tab arithmetic does not wrap")
	}
}

func TestTabForKey_digitsOnly(t *testing.T) {
	for k, want := range map[string]tab{"1": tabOverview, "5": tabChain, "9": tabAlerts} {
		if got, ok := tabForKey(k); !ok || got != want {
			t.Errorf("%s: got %v, %v", k, got, ok)
		}
	}
	for _, k := range []string{"0", "a", "10", ""} {
		if _, ok := tabForKey(k); ok {
			t.Errorf("%q jumped to a tab", k)
		}
	}
}

func TestRenderTabBar_compactsWhenNarrow(t *testing.T) {
	th := view.NewTheme(false)
	wide := renderTabBar(th, tabTraffic, 0)
	if !strings.Contains(wide, "1 Overview") || !strings.Contains(wide, "9 Alerts") {
		t.Fatalf("wide bar: %q", wide)
	}
	narrow := renderTabBar(th, tabTraffic, 60)
	if lipgloss.Width(narrow) > 60 || !strings.Contains(narrow, "4 Traffic") || strings.Contains(narrow, "Overview") {
		t.Fatalf("narrow bar (%d cells): %q", lipgloss.Width(narrow), narrow)
	}
}
