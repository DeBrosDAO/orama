package triggers

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpression is a parsed cron expression that can compute the next
// occurrence after an arbitrary instant.
//
// Two layouts are accepted:
//
//	5 fields:  minute hour day-of-month month day-of-week
//	6 fields:  second minute hour day-of-month month day-of-week
//
// Each field supports `*`, single integers, comma-separated lists,
// `a-b` ranges, and `*/n` step expressions. Day-of-week values are
// 0-6 with 0 = Sunday; 7 is normalised to 0. Month values are 1-12.
//
// Schedules are treated as wall-clock UTC; that matches the gateway's
// time.Now() — no per-namespace timezone configuration today.
type CronExpression struct {
	hasSeconds bool
	seconds    fieldMatcher // 0-59 (only when hasSeconds)
	minutes    fieldMatcher // 0-59
	hours      fieldMatcher // 0-23
	dom        fieldMatcher // 1-31
	month      fieldMatcher // 1-12
	dow        fieldMatcher // 0-6 (Sunday = 0)
	expr       string
}

// fieldMatcher is a bitmask over the legal value range for a cron field.
// Up to 64 entries — the largest range we model is seconds/minutes
// (0..59), comfortably below 64.
type fieldMatcher uint64

func (f fieldMatcher) match(v int) bool {
	if v < 0 || v >= 64 {
		return false
	}
	return f&(1<<uint(v)) != 0
}

// ParseCron parses a 5- or 6-field cron expression. Returns a non-nil
// error on any malformed field; otherwise returns a CronExpression
// usable across goroutines (immutable after construction).
func ParseCron(expr string) (*CronExpression, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron: empty expression")
	}
	parts := strings.Fields(expr)
	c := &CronExpression{expr: expr}
	switch len(parts) {
	case 5:
		c.hasSeconds = false
	case 6:
		c.hasSeconds = true
	default:
		return nil, fmt.Errorf("cron: expected 5 or 6 fields, got %d in %q", len(parts), expr)
	}

	idx := 0
	if c.hasSeconds {
		s, err := parseField(parts[idx], 0, 59)
		if err != nil {
			return nil, fmt.Errorf("cron seconds: %w", err)
		}
		c.seconds = s
		idx++
	}
	min, err := parseField(parts[idx], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("cron minutes: %w", err)
	}
	c.minutes = min
	idx++

	hr, err := parseField(parts[idx], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("cron hours: %w", err)
	}
	c.hours = hr
	idx++

	dom, err := parseField(parts[idx], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	c.dom = dom
	idx++

	mo, err := parseField(parts[idx], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	c.month = mo
	idx++

	dow, err := parseField(parts[idx], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}
	// Normalise Sunday: cron permits 0 OR 7.
	if dow.match(7) {
		dow |= 1
	}
	dow &= 0x7F // clamp to 0..6 bits
	c.dow = dow

	return c, nil
}

// parseField builds a bitmask over [lo, hi] inclusive from a single cron
// field. Accepts: "*", "n", "a,b,c", "a-b", "a-b/n", "*/n".
func parseField(s string, lo, hi int) (fieldMatcher, error) {
	if s == "" {
		return 0, fmt.Errorf("empty field")
	}
	var mask fieldMatcher
	for _, segment := range strings.Split(s, ",") {
		seg := strings.TrimSpace(segment)
		step := 1
		if i := strings.Index(seg, "/"); i >= 0 {
			n, err := strconv.Atoi(seg[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step in %q", seg)
			}
			step = n
			seg = seg[:i]
		}
		var rangeLo, rangeHi int
		switch {
		case seg == "*":
			rangeLo, rangeHi = lo, hi
		case strings.Contains(seg, "-"):
			parts := strings.SplitN(seg, "-", 2)
			a, err1 := strconv.Atoi(parts[0])
			b, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("bad range %q", seg)
			}
			rangeLo, rangeHi = a, b
		default:
			n, err := strconv.Atoi(seg)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", seg)
			}
			rangeLo, rangeHi = n, n
		}
		if rangeLo < lo || rangeHi > hi || rangeLo > rangeHi {
			return 0, fmt.Errorf("value %d-%d out of range [%d,%d]", rangeLo, rangeHi, lo, hi)
		}
		for v := rangeLo; v <= rangeHi; v += step {
			if v >= 64 {
				continue
			}
			mask |= 1 << uint(v)
		}
	}
	return mask, nil
}

// Next returns the smallest time strictly after `after` that matches the
// expression. The search is bounded to a few years out — schedules that
// can never match (e.g. day-of-month=31 with month=Feb) return a non-nil
// error rather than looping forever.
func (c *CronExpression) Next(after time.Time) (time.Time, error) {
	t := after.UTC()
	if c.hasSeconds {
		t = t.Add(time.Second).Truncate(time.Second)
	} else {
		t = t.Add(time.Minute).Truncate(time.Minute)
	}

	// Search horizon: 5 years. A valid expression matches well within
	// this window; pathological ones (impossible date combos) are caught
	// by this bound.
	deadline := after.Add(5 * 365 * 24 * time.Hour)
	for t.Before(deadline) {
		if !c.month.match(int(t.Month())) {
			// Jump to the first of the next month.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.dom.match(t.Day()) || !c.dow.match(int(t.Weekday())) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.hours.match(t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if !c.minutes.match(t.Minute()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute()+1, 0, 0, time.UTC)
			continue
		}
		if c.hasSeconds && !c.seconds.match(t.Second()) {
			t = t.Add(time.Second)
			continue
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cron: no match within 5 years for %q", c.expr)
}

// String returns the original expression as parsed.
func (c *CronExpression) String() string { return c.expr }
