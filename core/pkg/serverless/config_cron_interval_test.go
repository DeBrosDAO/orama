package serverless

import (
	"strings"
	"testing"
	"time"
)

// TestConfig_Validate_CronPollIntervalFloor is the regression guard for
// the bugboard #109 floor. The original ask was sub-second cron polling
// for typing/presence prune workloads. We allow sub-second down to the
// MinCronPollInterval floor (100ms), and reject anything below it
// because the per-tick rqlite cost would queue ticks indefinitely and
// starve the namespace gateway.
func TestConfig_Validate_CronPollIntervalFloor(t *testing.T) {
	cases := []struct {
		name       string
		interval   time.Duration
		wantReject bool
	}{
		{"zero means use default (no error)", 0, false},
		{"1 minute (legacy default) — fine", time.Minute, false},
		{"1 second — sub-second OK", time.Second, false},
		{"500ms — sub-second OK", 500 * time.Millisecond, false},
		{"exactly the floor (100ms) — OK", MinCronPollInterval, false},
		{"50ms — below floor, REJECT", 50 * time.Millisecond, true},
		{"1ms — well below floor, REJECT", 1 * time.Millisecond, true},
		{"-1s (operator typo) — REJECT", -time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.CronPollInterval = tc.interval

			errs := c.Validate()
			gotReject := false
			for _, err := range errs {
				if ce, ok := err.(*ConfigError); ok && ce.Field == "CronPollInterval" {
					gotReject = true
				}
			}
			if gotReject != tc.wantReject {
				t.Errorf("interval=%v: reject=%v; want reject=%v (errs=%v)",
					tc.interval, gotReject, tc.wantReject, errs)
			}
		})
	}
}

// TestConfig_Validate_CronPollIntervalErrorMessage verifies the
// rejection error carries the operator-facing detail (current value,
// min value, bugboard reference). Without this, an operator misconfiguring
// `cron_poll_interval: 10ms` gets an opaque "invalid config" error and
// has to grep code to figure out why.
func TestConfig_Validate_CronPollIntervalErrorMessage(t *testing.T) {
	c := DefaultConfig()
	c.CronPollInterval = 10 * time.Millisecond

	errs := c.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for sub-floor CronPollInterval")
	}
	var found *ConfigError
	for _, err := range errs {
		if ce, ok := err.(*ConfigError); ok && ce.Field == "CronPollInterval" {
			found = ce
			break
		}
	}
	if found == nil {
		t.Fatalf("no CronPollInterval ConfigError in %v", errs)
	}
	for _, want := range []string{
		MinCronPollInterval.String(), // floor
		"10ms",                       // current value
		"#109",                       // bugboard reference
	} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("error message missing %q: %s", want, found.Message)
		}
	}
}

// TestConfig_ApplyDefaults_FillsInCronPollInterval verifies the default
// is applied when the field is zero. Regression guard against a future
// refactor that accidentally drops the zero-check.
func TestConfig_ApplyDefaults_FillsInCronPollInterval(t *testing.T) {
	c := &Config{}
	c.ApplyDefaults()
	if c.CronPollInterval != time.Minute {
		t.Errorf("ApplyDefaults: CronPollInterval = %v; want %v",
			c.CronPollInterval, time.Minute)
	}
}

// TestMinCronPollInterval_Reasonable is a guard rail on the constant
// itself. If a future contributor sets it too high (blocks legit
// typing/presence workloads) or too low (lets DoS through), this
// catches it.
func TestMinCronPollInterval_Reasonable(t *testing.T) {
	if MinCronPollInterval > time.Second {
		t.Errorf("MinCronPollInterval=%v is too high — blocks legit sub-second prune workloads (bugboard #109)",
			MinCronPollInterval)
	}
	if MinCronPollInterval < time.Millisecond {
		t.Errorf("MinCronPollInterval=%v is too low — opens scheduler DoS surface",
			MinCronPollInterval)
	}
}
