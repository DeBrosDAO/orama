package triggers

import (
	"strings"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// ParseCron — accept paths
// ----------------------------------------------------------------------------

func TestParseCron_fiveField(t *testing.T) {
	c, err := ParseCron("0 3 * * *") // every day at 03:00 UTC
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	if c.hasSeconds {
		t.Error("hasSeconds = true, want false for 5-field expression")
	}
	if !c.minutes.match(0) {
		t.Error("minutes mask missing 0")
	}
	if c.minutes.match(1) {
		t.Error("minutes mask should not match 1")
	}
	if !c.hours.match(3) {
		t.Error("hours mask missing 3")
	}
}

func TestParseCron_sixField(t *testing.T) {
	c, err := ParseCron("*/30 * * * * *") // every 30 seconds
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	if !c.hasSeconds {
		t.Error("hasSeconds = false, want true for 6-field expression")
	}
	if !c.seconds.match(0) || !c.seconds.match(30) {
		t.Error("seconds mask should match 0 and 30")
	}
	if c.seconds.match(15) {
		t.Error("seconds mask should NOT match 15 for */30")
	}
}

func TestParseCron_listsAndRanges(t *testing.T) {
	c, err := ParseCron("0,15,30,45 9-17 * * 1-5") // every 15min during business hours, weekdays
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	for _, m := range []int{0, 15, 30, 45} {
		if !c.minutes.match(m) {
			t.Errorf("minutes mask missing %d", m)
		}
	}
	for _, h := range []int{9, 12, 17} {
		if !c.hours.match(h) {
			t.Errorf("hours mask missing %d", h)
		}
	}
	if c.hours.match(8) || c.hours.match(18) {
		t.Error("hours mask should not match outside 9-17")
	}
	for _, d := range []int{1, 2, 3, 4, 5} {
		if !c.dow.match(d) {
			t.Errorf("dow mask missing %d", d)
		}
	}
	if c.dow.match(0) || c.dow.match(6) {
		t.Error("dow mask should not match weekend (0 or 6)")
	}
}

func TestParseCron_sundayNormalisation(t *testing.T) {
	// Cron permits 0 OR 7 for Sunday. Both should be normalised to 0.
	c7, err := ParseCron("0 0 * * 7")
	if err != nil {
		t.Fatalf("ParseCron 7: %v", err)
	}
	if !c7.dow.match(0) {
		t.Error("dow 7 should normalise to match 0 (Sunday)")
	}
	if c7.dow.match(7) {
		t.Error("after normalisation, bit 7 should be cleared (clamped to 0..6)")
	}
}

// ----------------------------------------------------------------------------
// ParseCron — reject paths
// ----------------------------------------------------------------------------

func TestParseCron_rejectMalformed(t *testing.T) {
	cases := []struct {
		expr   string
		reason string
	}{
		{"", "empty"},
		{"   ", "whitespace-only"},
		{"* * *", "too few fields"},
		{"* * * * * * *", "too many fields"},
		{"60 * * * *", "minute out of range"},
		{"* 24 * * *", "hour out of range"},
		{"* * 0 * *", "day-of-month under range"},
		{"* * 32 * *", "day-of-month over range"},
		{"* * * 13 *", "month over range"},
		{"* * * 0 *", "month under range"},
		{"* * * * 8", "day-of-week over range"},
		{"abc * * * *", "non-numeric"},
		{"5-2 * * * *", "inverted range"},
		{"*/0 * * * *", "zero step"},
		{"*/-1 * * * *", "negative step"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			if _, err := ParseCron(tc.expr); err == nil {
				t.Errorf("ParseCron(%q) succeeded; expected error (%s)", tc.expr, tc.reason)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Next() — correctness
// ----------------------------------------------------------------------------

func TestNext_dailyAt03UTC(t *testing.T) {
	c, _ := ParseCron("0 3 * * *")
	now := time.Date(2025, 5, 7, 12, 30, 0, 0, time.UTC)
	got, err := c.Next(now)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2025, 5, 8, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

func TestNext_strictlyAfter(t *testing.T) {
	// When `after` is exactly on a matching minute, Next must return the
	// FOLLOWING match, not `after` itself.
	c, _ := ParseCron("0 3 * * *")
	now := time.Date(2025, 5, 7, 3, 0, 0, 0, time.UTC) // exactly 03:00
	got, _ := c.Next(now)
	want := time.Date(2025, 5, 8, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next from exact-match should advance one day: got %s, want %s", got, want)
	}
}

func TestNext_secondsResolution(t *testing.T) {
	c, _ := ParseCron("*/30 * * * * *") // every 30 sec
	now := time.Date(2025, 5, 7, 12, 0, 5, 0, time.UTC)
	got, _ := c.Next(now)
	want := time.Date(2025, 5, 7, 12, 0, 30, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

func TestNext_monthSkip(t *testing.T) {
	// Day-of-month=29, only February — must skip to the next leap year.
	c, _ := ParseCron("0 0 29 2 *")
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := c.Next(now)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next leap = %s, want %s", got, want)
	}
}

func TestNext_impossibleScheduleFails(t *testing.T) {
	// February 30th never exists.
	c, _ := ParseCron("0 0 30 2 *")
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := c.Next(now)
	if err == nil {
		t.Fatal("Next should fail for impossible date combo (Feb 30)")
	}
	if !strings.Contains(err.Error(), "no match") {
		t.Errorf("error = %v, want 'no match' substring", err)
	}
}

func TestNext_weekdayOnly(t *testing.T) {
	c, _ := ParseCron("0 9 * * 1") // every Monday at 09:00
	// Wednesday May 7, 2025
	now := time.Date(2025, 5, 7, 12, 0, 0, 0, time.UTC)
	got, _ := c.Next(now)
	// Next Monday is May 12, 2025
	want := time.Date(2025, 5, 12, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next weekday = %s, want %s", got, want)
	}
	if got.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", got.Weekday())
	}
}

// ----------------------------------------------------------------------------
// fieldMatcher
// ----------------------------------------------------------------------------

func TestFieldMatcher_outOfRange(t *testing.T) {
	var m fieldMatcher = 0xFFFF
	if m.match(-1) {
		t.Error("match(-1) should be false")
	}
	if m.match(64) {
		t.Error("match(64) should be false (mask is uint64, bits 0..63 only)")
	}
}
