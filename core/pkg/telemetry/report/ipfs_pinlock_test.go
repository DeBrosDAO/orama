package report

import (
	"testing"
	"time"
)

// diag/cmds modelled on poseidon (Kubo 0.38.2) during bug 2722: the
// cluster's pin/add of a CID no peer had, and the GC oneshot's repo/gc queued
// behind it. The repo/gc entry's options carry the CLI's bearer.
const diagCmdsStuck = `[
 {"StartTime":"2026-09-27T23:31:49Z","EndTime":"0001-01-01T00:00:00Z","Active":true,"Command":"pin/add","Options":{"encoding":"json","progress":true,"recursive":true},"Args":["Qme19HCi9sDZorn3eHxsoEibrRnzG5kBZDQfbXgRW1ysEG"],"ID":8},
 {"StartTime":"2026-09-28T00:02:47Z","EndTime":"0001-01-01T00:00:00Z","Active":true,"Command":"repo/gc","Options":{"api-auth":"bearer:0000"},"Args":[],"ID":3847},
 {"StartTime":"2026-09-27T20:00:00Z","EndTime":"2026-09-27T20:00:01Z","Active":false,"Command":"pin/add","Options":{},"Args":["QmDone"],"ID":2},
 {"StartTime":"2026-09-27T19:00:00Z","EndTime":"0001-01-01T00:00:00Z","Active":true,"Command":"pubsub/sub","Options":{},"Args":["t"],"ID":1},
 {"StartTime":"2026-09-28T05:19:01Z","EndTime":"0001-01-01T00:00:00Z","Active":true,"Command":"diag/cmds","Options":{},"Args":[],"ID":42164}
]`

func TestOldestPinLockCommand_findsTheStuckPin(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 19, 1, 0, time.UTC)
	cmd, age, err := oldestPinLockCommand([]byte(diagCmdsStuck), now)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "pin/add" {
		t.Errorf("command = %q, want the pin/add the GC is queued behind", cmd)
	}
	if want := 5*time.Hour + 47*time.Minute + 12*time.Second; age != want {
		t.Errorf("age = %s, want %s", age, want)
	}
}

// Finished requests and long-lived streams that do not take the pin lock
// (pubsub/sub) are not a stalled pin.
func TestOldestPinLockCommand_ignoresInactiveAndUnrelated(t *testing.T) {
	body := `[
 {"StartTime":"2026-09-27T20:00:00Z","Active":false,"Command":"pin/add"},
 {"StartTime":"2026-09-27T19:00:00Z","Active":true,"Command":"pubsub/sub"},
 {"StartTime":"2026-09-27T19:00:00Z","Active":true,"Command":"repo/stat"}
]`
	cmd, age, err := oldestPinLockCommand([]byte(body), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "" || age != 0 {
		t.Errorf("got %q %s, want nothing active", cmd, age)
	}
}

func TestOldestPinLockCommand_edgeCases(t *testing.T) {
	for _, body := range []string{`[]`, `null`} {
		cmd, _, err := oldestPinLockCommand([]byte(body), time.Now())
		if err != nil || cmd != "" {
			t.Errorf("%s: got %q, %v; want nothing active", body, cmd, err)
		}
	}
	if _, _, err := oldestPinLockCommand([]byte(`{"Message":"401"}`), time.Now()); err == nil {
		t.Error("a non-list answer was read as no active requests")
	}
	// A start time after now (clock step) is not a negative age.
	future := `[{"StartTime":"2030-01-01T00:00:00Z","Active":true,"Command":"repo/gc"}]`
	_, age, err := oldestPinLockCommand([]byte(future), time.Now())
	if err != nil || age != 0 {
		t.Errorf("future start: got %s, %v; want 0", age, err)
	}
	// An entry without a start time cannot be aged and is skipped.
	cmd, _, err := oldestPinLockCommand([]byte(`[{"Active":true,"Command":"pin/add"}]`), time.Now())
	if err != nil || cmd != "" {
		t.Errorf("zero start: got %q, %v", cmd, err)
	}
}
