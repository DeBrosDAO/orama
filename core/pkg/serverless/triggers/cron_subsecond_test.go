package triggers

import (
	"testing"
	"time"
)

// TestParseCron_everySecond is the regression guard for bugboard #109's
// canonical use case: `*/1 * * * * *` (6-field, "every second"). The
// parser already supports 6-field expressions with seconds — this test
// pins that behavior so a future refactor of the 6-field branch can't
// silently break the ephemeral-state prune workload.
func TestParseCron_everySecond(t *testing.T) {
	c, err := ParseCron("*/1 * * * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	if !c.hasSeconds {
		t.Error("hasSeconds = false; want true for 6-field expression")
	}
	for s := 0; s < 60; s++ {
		if !c.seconds.match(s) {
			t.Errorf("seconds.match(%d) = false; want true for `*/1` (every second)", s)
		}
	}
}

// TestNext_everySecond verifies that `*/1 * * * * *` advances by
// exactly one second on each Next() call. If the cron scheduler is
// ticking every 1s and the expression matches every second, the
// dispatched next_run_at MUST land on the next whole second — not a
// minute later (which would defeat sub-second cron entirely).
func TestNext_everySecond(t *testing.T) {
	c, err := ParseCron("*/1 * * * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	start := time.Date(2026, 5, 21, 13, 14, 15, 0, time.UTC)
	got, err := c.Next(start)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 5, 21, 13, 14, 16, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(%s) = %s; want %s (every-second cron should advance 1s)",
			start.Format(time.RFC3339), got.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// And the next one is +1s from that.
	got2, _ := c.Next(got)
	want2 := want.Add(time.Second)
	if !got2.Equal(want2) {
		t.Errorf("Next(%s) = %s; want %s", got.Format(time.RFC3339),
			got2.Format(time.RFC3339), want2.Format(time.RFC3339))
	}
}

// TestParseCron_subSecondStep_validation covers a few practical
// sub-second-style expressions the operator might try, ensuring the
// parser rejects nothing legitimate. Negative coverage in the existing
// cron_parser_test.go for invalid expressions.
func TestParseCron_subSecondStep_validation(t *testing.T) {
	cases := []struct {
		expr string
		want bool // true = should parse OK
	}{
		{"*/1 * * * * *", true},  // every second
		{"*/5 * * * * *", true},  // every 5s
		{"*/30 * * * * *", true}, // every 30s (already tested in cron_parser_test.go)
		{"0 * * * * *", true},    // at second 0 of every minute (= once a minute, 6-field)
		{"*/2 */1 * * * *", true},
		{"*/1 * * * *", true},    // 5-field: every minute (NOT every second — different schedule!)
	}
	for _, tc := range cases {
		_, err := ParseCron(tc.expr)
		if (err == nil) != tc.want {
			t.Errorf("ParseCron(%q): err=%v; want parseable=%v", tc.expr, err, tc.want)
		}
	}
}
