package gateway

import (
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

func TestLogThrottle_forgetsExpiredKeysAtTheCap(t *testing.T) {
	var th logThrottle
	t0 := time.Unix(1000, 0)
	for i := 0; i < logThrottleMaxKeys; i++ {
		th.allow(string(rune('a'+i%26))+time.Duration(i).String(), t0, time.Minute)
	}
	th.allow("fresh", t0.Add(time.Hour), time.Minute)
	if n := len(th.last); n != 1 {
		t.Fatalf("%d keys kept after the cap was reached with every interval over, want 1", n)
	}
}
