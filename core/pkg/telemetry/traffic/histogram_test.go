package traffic

import (
	"math"
	"testing"
	"time"
)

const floatTolerance = 1e-9

func assertMs(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > floatTolerance {
		t.Errorf("%s = %v ms, want %v ms", name, got, want)
	}
}

func TestBucketFor_boundaries(t *testing.T) {
	last := len(latencyBoundsMs) - 1
	cases := []struct {
		name string
		d    time.Duration
		want int
	}{
		{"zero", 0, 0},
		{"exactly first bound", time.Millisecond, 0},
		{"just above first bound", time.Millisecond + time.Microsecond, 1},
		{"exactly last bound", 30 * time.Second, last},
		{"above last bound", 31 * time.Second, last + 1},
		{"negative", -time.Second, 0},
	}
	for _, tc := range cases {
		if got := bucketFor(tc.d); got != tc.want {
			t.Errorf("%s: bucketFor(%v) = %d, want %d", tc.name, tc.d, got, tc.want)
		}
	}
}

func TestQuantile_empty(t *testing.T) {
	var h histogram
	assertMs(t, "p50", h.quantile(p50), 0)
	assertMs(t, "p99", h.quantile(p99), 0)
}

func TestQuantile_singleBucketInterpolates(t *testing.T) {
	var h histogram
	for range 100 {
		h.observe(50 * time.Millisecond) // bucket (40, 60]
	}
	assertMs(t, "p50", h.quantile(p50), 50)
	assertMs(t, "p95", h.quantile(p95), 59)
}

func TestQuantile_bimodal(t *testing.T) {
	var h histogram
	for range 90 {
		h.observe(4 * time.Millisecond) // bucket (3, 5]
	}
	for range 10 {
		h.observe(800 * time.Millisecond) // bucket (600, 1000]
	}
	assertMs(t, "p50", h.quantile(p50), 3+(50.0/90.0)*2)
	assertMs(t, "p95", h.quantile(p95), 800)
	assertMs(t, "p99", h.quantile(p99), 960)
}

func TestQuantile_overflowReportsLastBound(t *testing.T) {
	var h histogram
	h.observe(5 * time.Minute)
	assertMs(t, "p50", h.quantile(p50), 30000)
}

func TestMerge_addsCounts(t *testing.T) {
	var a, b histogram
	a.observe(time.Millisecond)
	b.observe(time.Millisecond)
	b.observe(time.Hour)
	a.merge(&b)
	if a.count() != 3 || a[0] != 2 || a[numLatencyBuckets-1] != 1 {
		t.Fatalf("merged histogram = %v, want 2 in the first bucket and 1 in overflow", a)
	}
}
