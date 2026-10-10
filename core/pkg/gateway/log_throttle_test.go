package gateway

import (
	"fmt"
	"testing"
	"time"
)

func TestLogThrottle_oneEventPerKeyPerInterval(t *testing.T) {
	var th logThrottle
	t0 := time.Unix(1000, 0)

	if ok, held := th.allow("a", t0, time.Minute); !ok || held != 0 {
		t.Fatalf("first event: ok=%v held=%d", ok, held)
	}
	for i := 0; i < 3; i++ {
		if ok, _ := th.allow("a", t0.Add(time.Duration(i+1)*time.Second), time.Minute); ok {
			t.Fatalf("event %d inside the interval was let through", i+1)
		}
	}
	if ok, held := th.allow("b", t0.Add(time.Second), time.Minute); !ok || held != 0 {
		t.Fatalf("another key was throttled by the first: ok=%v held=%d", ok, held)
	}
	if ok, held := th.allow("a", t0.Add(time.Minute), time.Minute); !ok || held != 3 {
		t.Fatalf("after the interval: ok=%v held=%d, want true and the 3 held back", ok, held)
	}
	if ok, held := th.allow("a", t0.Add(time.Minute+time.Second), time.Minute); ok || held != 0 {
		t.Fatalf("the window did not restart: ok=%v held=%d", ok, held)
	}
}

// The cap is a real cap: a flood of distinct keys never holds more than
// logThrottleMaxKeys, and the key let through longest ago is the one forgotten.
func TestLogThrottle_capIsAHardCapAndForgetsTheOldest(t *testing.T) {
	var th logThrottle
	t0 := time.Unix(1000, 0)
	for i := 0; i < 3*logThrottleMaxKeys; i++ {
		th.allow(fmt.Sprint("key", i), t0.Add(time.Duration(i)*time.Millisecond), time.Hour)
		if n := len(th.entries); n > logThrottleMaxKeys || th.order.Len() != n {
			t.Fatalf("after %d keys: %d entries, %d in order, cap %d", i+1, n, th.order.Len(), logThrottleMaxKeys)
		}
	}
	now := t0.Add(time.Minute)
	if ok, _ := th.allow("key0", now, time.Hour); !ok {
		t.Error("the oldest key was remembered past the cap")
	}
	if ok, _ := th.allow(fmt.Sprint("key", 3*logThrottleMaxKeys-1), now, time.Hour); ok {
		t.Error("the newest key was forgotten")
	}
}

// A key whose interval ended while the table is full still reports how many
// events were held back.
func TestLogThrottle_heldBackSurvivesAFullTable(t *testing.T) {
	var th logThrottle
	t0 := time.Unix(1000, 0)
	th.allow("busy", t0, time.Minute)
	for i := 0; i < 5; i++ {
		th.allow("busy", t0.Add(time.Second), time.Minute)
	}
	for i := 0; i < logThrottleMaxKeys-1; i++ {
		th.allow(fmt.Sprint("other", i), t0.Add(2*time.Second), time.Minute)
	}
	if len(th.entries) != logThrottleMaxKeys {
		t.Fatalf("table has %d entries, want it full", len(th.entries))
	}
	if ok, held := th.allow("busy", t0.Add(time.Minute), time.Minute); !ok || held != 5 {
		t.Fatalf("ok=%v held=%d, want true and the 5 held back", ok, held)
	}
}
