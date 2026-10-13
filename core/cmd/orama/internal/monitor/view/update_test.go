package view

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

func TestUpdateCell(t *testing.T) {
	th := NewTheme(false)
	cases := []struct {
		name string
		n    *updatenotice.Notice
		want string
	}{
		{"nothing to report", nil, "-"},
		{"a newer release on notify", &updatenotice.Notice{State: updatenotice.StateAvailable, Mode: "notify", Candidate: "0.3.1"}, "0.3.1 (notify)"},
		{"a newer release queued on auto", &updatenotice.Notice{State: updatenotice.StateAvailable, Mode: "auto", Candidate: "0.3.1"}, "0.3.1 (upgrade)"},
		{"a refusal", &updatenotice.Notice{State: updatenotice.StateRefused, Candidate: "0.3.1"}, "refused 0.3.1"},
		{"a refusal that names no release", &updatenotice.Notice{State: updatenotice.StateRefused}, "refused --"},
		{"a rollback", &updatenotice.Notice{State: updatenotice.StateFailed, Candidate: "0.3.1"}, "0.3.1 rolled back"},
	}
	for _, c := range cases {
		if got := UpdateCell(th, c.n); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUpdateSentence_saysWhatThePolicyWillDoAndWhy(t *testing.T) {
	n := &updatenotice.Notice{State: updatenotice.StateAvailable, Mode: "auto", Channel: "stable", Current: "0.3.0",
		Candidate: "0.3.1", Reason: "newer release 0.3.1 is outside the maintenance window"}
	got := UpdateSentence(n)
	for _, want := range []string{"release 0.3.1 is available on the stable channel", "running 0.3.0", "policy (auto) will upgrade", "outside the maintenance window"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	refused := UpdateSentence(&updatenotice.Notice{State: updatenotice.StateRefused, Mode: "auto", Channel: "stable", Current: "0.3.0",
		Candidate: "0.3.1", Reason: "this machine is a validator, upgrade it by hand"})
	if !strings.Contains(refused, "was refused") || !strings.Contains(refused, "validator") {
		t.Errorf("refusal: %q", refused)
	}
	if UpdateSentence(nil) != "" || UpdateSentence(&updatenotice.Notice{State: "weird"}) != "" {
		t.Error("no finding produced a sentence")
	}
}

func TestUpgradeSentence(t *testing.T) {
	chain := func(height int64, avg float64, u *report.ChainUpgrade) *report.ChainReport {
		return &report.ChainReport{LatestHeight: height, AvgBlockTimeSec: avg, Upgrade: u}
	}
	cases := []struct {
		name string
		c    *report.ChainReport
		want []string
	}{
		{"no chain", nil, nil},
		{"no plan", chain(10, 5, nil), nil},
		{"staged with a time", chain(1000, 6, &report.ChainUpgrade{Name: "v0-4-0", Height: 1600, Staged: true}),
			[]string{"v0-4-0 at height 1600", "600 blocks left", "about 1h00m", "binary staged"}},
		{"not staged", chain(1000, 6, &report.ChainUpgrade{Name: "v0-4-0", Height: 1600}), []string{"binary NOT staged"}},
		{"no block time measured", chain(1000, 0, &report.ChainUpgrade{Name: "v0-4-0", Height: 1600, Staged: true}),
			[]string{"600 blocks left)"}},
		{"height reached", chain(1600, 6, &report.ChainUpgrade{Name: "v0-4-0", Height: 1600}), []string{"the height is reached"}},
		{"cannot be staged", chain(1, 6, &report.ChainUpgrade{Name: "X", Height: 2, StageError: "bad name"}), []string{"cannot be staged: bad name"}},
		{"plan unreadable", &report.ChainReport{UpgradeError: "HTTP 500"}, []string{"could not be read: HTTP 500"}},
		{"staged and matching", chain(1000, 6, &report.ChainUpgrade{Name: "v2", Height: 1600, Staged: true, Checksums: report.ChecksumsMatch}),
			[]string{"binary staged on this node and matches the plan"}},
		{"staged and not matching", chain(1000, 6, &report.ChainUpgrade{Name: "v2", Height: 1600, Staged: true, Checksums: report.ChecksumsMismatch, ChecksumProblem: "the staged oramad is not the one the plan names"}),
			[]string{"staged binary does NOT match the plan: the staged oramad is not the one the plan names"}},
		{"the plan names no checksum", chain(1000, 6, &report.ChainUpgrade{Name: "v2", Height: 1600, Staged: true, Checksums: report.ChecksumsUnnamed, ChecksumProblem: "no checksum"}),
			[]string{"the plan names no checksum to check it against: no checksum"}},
		{"unreadable staged files", chain(1000, 6, &report.ChainUpgrade{Name: "v2", Height: 1600, Staged: true, Checksums: report.ChecksumsUnchecked, ChecksumProblem: "permission denied"}),
			[]string{"could not be checked against the plan: permission denied"}},
	}
	for _, c := range cases {
		got := UpgradeSentence(c.c)
		if c.want == nil && got != "" {
			t.Errorf("%s: %q, want none", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
	}
}

func TestCountdownTo(t *testing.T) {
	c := &report.ChainReport{LatestHeight: 100, AvgBlockTimeSec: 2.5}
	cd := CountdownTo(c, &report.ChainUpgrade{Height: 140})
	if cd.BlocksLeft != 40 || !cd.HasETA || cd.ETA != 100*time.Second {
		t.Fatalf("%+v", cd)
	}
	if cd := CountdownTo(c, &report.ChainUpgrade{Height: 90}); cd.BlocksLeft != -10 || cd.HasETA {
		t.Fatalf("a height already passed: %+v", cd)
	}
}

// A node, a release repository or a governance proposal writes these fields: none of them may
// put an escape sequence, a line break or an unbounded text on the operator's terminal.
func TestUpdateRendering_stripsWhatActsOnATerminal(t *testing.T) {
	const hostile = "evil\x1b[2J\x1b]0;owned\x07\nsecond line‮"
	n := &updatenotice.Notice{State: updatenotice.StateRefused, Mode: hostile, Candidate: hostile, Channel: hostile, Current: hostile, Reason: hostile}
	c := &report.ChainReport{LatestHeight: 10, Upgrade: &report.ChainUpgrade{Name: hostile, Height: 20, StageError: hostile}}
	mismatch := &report.ChainReport{LatestHeight: 10, Upgrade: &report.ChainUpgrade{Name: hostile, Height: 20, Staged: true, Checksums: report.ChecksumsMismatch, ChecksumProblem: hostile}}
	for name, got := range map[string]string{
		"cell":            UpdateCell(NewTheme(false), n),
		"update sentence": UpdateSentence(n),
		"upgrade":         UpgradeSentence(c),
		"upgrade error":   UpgradeSentence(&report.ChainReport{UpgradeError: hostile}),
		"checksum":        UpgradeSentence(mismatch),
	} {
		if strings.ContainsAny(got, "\x1b\x07\n‮") {
			t.Errorf("%s keeps a terminal control character: %q", name, got)
		}
		if !strings.Contains(got, "evil[2J]0;owned | second line") {
			t.Errorf("%s lost the printable text: %q", name, got)
		}
	}
	long := &updatenotice.Notice{State: updatenotice.StateFailed, Candidate: "1", Current: "0", Reason: strings.Repeat("x", 5000)}
	if got := UpdateSentence(long); len(got) > 2*maxReportedRunes {
		t.Errorf("an unbounded reason reached the terminal: %d bytes", len(got))
	}
	if got := UpdateCell(NewTheme(false), nil); got != noUpdateCell {
		t.Errorf("no finding renders %q, want %q", got, noUpdateCell)
	}
}

// A height set far beyond any real schedule shows its blocks only: its time would overflow.
func TestCountdownTo_aHeightFarAwayHasNoETA(t *testing.T) {
	c := &report.ChainReport{LatestHeight: 1, AvgBlockTimeSec: 5}
	cd := CountdownTo(c, &report.ChainUpgrade{Height: 1 << 62})
	if cd.HasETA || cd.BlocksLeft != (1<<62)-1 {
		t.Fatalf("countdown = %+v, want blocks only", cd)
	}
}

func TestUpgradeNeedsAttention(t *testing.T) {
	for name, tc := range map[string]struct {
		u    *report.ChainUpgrade
		want bool
	}{
		"no plan":              {nil, false},
		"not staged":           {&report.ChainUpgrade{}, true},
		"staged, unchecked":    {&report.ChainUpgrade{Staged: true}, false},
		"staged and matching":  {&report.ChainUpgrade{Staged: true, Checksums: report.ChecksumsMatch}, false},
		"staged, mismatching":  {&report.ChainUpgrade{Staged: true, Checksums: report.ChecksumsMismatch}, true},
		"staged, plan unnamed": {&report.ChainUpgrade{Staged: true, Checksums: report.ChecksumsUnnamed}, true},
	} {
		if got := UpgradeNeedsAttention(tc.u); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
