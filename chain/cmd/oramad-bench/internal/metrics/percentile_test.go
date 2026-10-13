package metrics

import (
	"errors"
	"math"
	"testing"
)

func TestPercentile_nearestRank(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		p    float64
		want float64
	}{
		{10, 1}, {50, 5}, {51, 6}, {90, 9}, {99, 10}, {100, 10},
	}
	for _, c := range cases {
		got, err := Percentile(sorted, c.p)
		if err != nil {
			t.Fatalf("p%v: %v", c.p, err)
		}
		if got != c.want {
			t.Errorf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestPercentile_singleSampleIsEveryPercentile(t *testing.T) {
	for _, p := range []float64{0.001, 50, 99, 100} {
		got, err := Percentile([]float64{7}, p)
		if err != nil || got != 7 {
			t.Fatalf("p%v = %v, %v", p, got, err)
		}
	}
}

func TestPercentile_errors(t *testing.T) {
	if _, err := Percentile(nil, 50); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("empty: %v", err)
	}
	for _, p := range []float64{0, -1, 100.5, math.NaN()} {
		if _, err := Percentile([]float64{1}, p); err == nil {
			t.Errorf("p%v accepted", p)
		}
	}
}

func TestSummarize_distribution(t *testing.T) {
	samples := make([]float64, 100)
	for i := range samples {
		samples[len(samples)-1-i] = float64(i + 1) // unsorted on purpose
	}
	s, err := Summarize(samples)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 100 || s.Min != 1 || s.Max != 100 || s.P50 != 50 || s.P90 != 90 || s.P99 != 99 {
		t.Fatalf("summary %+v", s)
	}
	if math.Abs(s.Mean-50.5) > 1e-9 {
		t.Fatalf("mean %v", s.Mean)
	}
	if samples[0] != 100 {
		t.Fatal("Summarize sorted the caller's slice")
	}
}

func TestSummarize_emptyAndNaN(t *testing.T) {
	s, err := Summarize(nil)
	if err != nil || s != (Summary{}) {
		t.Fatalf("empty: %+v, %v", s, err)
	}
	if _, err := Summarize([]float64{1, math.NaN()}); err == nil {
		t.Fatal("NaN accepted")
	}
}
