package view

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

func TestColorAllowed_ttyAndNoColor(t *testing.T) {
	cases := []struct {
		name    string
		noColor string
		tty     bool
		want    bool
	}{
		{"terminal, NO_COLOR unset", "", true, true},
		{"terminal, NO_COLOR set", "1", true, false},
		{"pipe, NO_COLOR unset", "", false, false},
		{"pipe, NO_COLOR set", "1", false, false},
	}
	for _, tc := range cases {
		if got := colorAllowed(tc.noColor, tc.tty); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestColorEnabled_notAFile(t *testing.T) {
	if ColorEnabled(&bytes.Buffer{}) {
		t.Fatal("color was enabled for a buffer")
	}
}

func TestColorEnabled_NO_COLOR(t *testing.T) {
	t.Setenv(noColorEnv, "1")
	if ThemeFor(&bytes.Buffer{}).Color {
		t.Fatal("NO_COLOR did not turn color off")
	}
}

// The plain theme must produce no escape sequences at all, whatever lipgloss
// thinks the terminal can do.
func TestNewTheme_plainHasNoANSI(t *testing.T) {
	th := NewTheme(false)
	for _, s := range []lipgloss.Style{th.OK, th.Warn, th.Crit, th.Muted, th.Bold, th.Header, th.State(cluster.StateOutage), th.Pct(99)} {
		if out := s.Render("text"); strings.Contains(out, "\x1b") || out != "text" {
			t.Fatalf("plain style rendered %q", out)
		}
	}
}

func TestThemePct_thresholds(t *testing.T) {
	th := NewTheme(true)
	cases := map[int]lipgloss.Style{0: th.OK, pctWarn - 1: th.OK, pctWarn: th.Warn, pctCrit: th.Crit, 100: th.Crit}
	for pct, want := range cases {
		if got := th.Pct(pct); got.GetForeground() != want.GetForeground() {
			t.Errorf("Pct(%d) has the wrong color", pct)
		}
	}
}

func TestHealthCell_unknownIsNotCritical(t *testing.T) {
	th := NewTheme(true)
	if got, want := healthCell(th, cluster.HealthUnknown), th.Muted.Render(string(cluster.HealthUnknown)); got != want {
		t.Fatalf("unknown renders %q, want the muted style %q: an old-release node is not a failure", got, want)
	}
}
