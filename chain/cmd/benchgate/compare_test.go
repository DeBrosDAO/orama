package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// comparisonCSV is the output of `benchstat -format csv old.txt new.txt` (golang.org/x/perf at the
// version the Makefile pins) for six runs of each of: Fast (+60%), Same (noise), Gone (only in the
// baseline), Slow (+10%) and Added (only in the new run), with -benchmem so it also has the B/op
// and allocs/op tables the gate must ignore.
const comparisonCSV = `goos: linux
goarch: amd64
pkg: example.com/x
cpu: test
,old.txt,,new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
Fast-8,9.9e-08,1%,1.59e-07,1%,+60.61%,p=0.002 n=6
Same-8,1.995e-07,1%,2.0000000000000002e-07,1%,~,p=0.511 n=6
Gone-8,3e-07,1%
Slow-8,9.985000000000003e-07,1%,1.1040000000000001e-06,1%,+10.57%,p=0.002 n=6
Added-8,,,9.5e-09,5%
geomean,1.9608695374708278e-07,,1.2444818492055478e-07,,+34.28%,

,old.txt,,new.txt,,,
,B/op,CI,B/op,CI,vs base,P
Fast-8,1024,0%,99999,0%,+9000.00%,p=0.002 n=6
geomean,1024,,1024,,+0.00%,
`

func parse(t *testing.T, csv string) []Row {
	t.Helper()
	rows, err := ParseBenchstatCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseBenchstatCSV: %v", err)
	}
	return rows
}

func TestParseBenchstatCSV_readsOnlyTimeRows(t *testing.T) {
	rows := parse(t, comparisonCSV)
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	want := []string{"Fast-8", "Same-8", "Gone-8", "Slow-8", "Added-8"}
	if !slices.Equal(names, want) {
		t.Fatalf("rows %v, want %v (no geomean, none of the B/op table)", names, want)
	}
	if fast := rows[0]; !fast.Significant || fast.Samples != 6 || fast.Old != 9.9e-08 {
		t.Errorf("Fast = %+v", fast)
	}
	if same := rows[1]; same.Significant {
		t.Errorf("a ~ row is not significant: %+v", same)
	}
	if gone := rows[2]; gone.New != 0 || gone.Old == 0 {
		t.Errorf("a baseline-only row has no new value: %+v", gone)
	}
}

func TestParseBenchstatCSV_unequalSampleCountsTakeTheSmaller(t *testing.T) {
	rows := parse(t, ",a,,b,,,\n,sec/op,CI,sec/op,CI,vs base,P\nX-8,1,1%,2,1%,+100.00%,p=0.004 n=7+5\n")
	if rows[0].Samples != 5 {
		t.Fatalf("Samples = %d, want 5", rows[0].Samples)
	}
}

func TestParseBenchstatCSV_emptyAndUnusableInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":           "",
		"header only":     ",a,,b,,,\n,sec/op,CI,sec/op,CI,vs base,P\n",
		"only bytes/op":   ",a,,b,,,\n,B/op,CI,B/op,CI,vs base,P\nX-8,1,0%,1,0%,~,p=1.000 n=6\n",
		"only the config": "goos: linux\npkg: x\n",
	} {
		if _, err := ParseBenchstatCSV(strings.NewReader(in)); !errors.Is(err, ErrNoBenchmarks) {
			t.Errorf("%s: err = %v, want ErrNoBenchmarks", name, err)
		}
	}
	if _, err := ParseBenchstatCSV(strings.NewReader(",a,,b,,,\n,sec/op,CI,sec/op,CI,vs base,P\nX-8,fast,1%,2,1%,~,p=1 n=6\n")); err == nil {
		t.Error("a value that is not a number was accepted")
	}
}

func TestJudge_passesWithinTheThresholdAndListsNewBenchmarks(t *testing.T) {
	rows := []Row{
		{Name: "Same-8", Old: 2e-7, New: 2.01e-7, Samples: 6},
		{Name: "Slow-8", Old: 1e-6, New: 1.1e-6, Significant: true, Samples: 6},
		{Name: "Faster-8", Old: 1e-6, New: 5e-7, Significant: true, Samples: 6},
		{Name: "Added-8", New: 1e-8, Samples: 6},
	}
	v, err := Judge(rows, DefaultRegressionPercent)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() || v.Compared != 3 || !slices.Equal(v.New, []string{"Added-8"}) {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestJudge_failsOnARegressionOverTheThreshold(t *testing.T) {
	v, err := Judge(parse(t, comparisonCSV), DefaultRegressionPercent)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK() {
		t.Fatal("a +60% time/op regression passed")
	}
	if len(v.Regressions) != 1 || v.Regressions[0].Name != "Fast-8" {
		t.Fatalf("regressions = %+v, want only Fast-8 (Slow-8 is +10%%, Same-8 is noise)", v.Regressions)
	}
}

func TestJudge_thresholdIsExclusive(t *testing.T) {
	at := []Row{{Name: "X-8", Old: 1, New: 1.25, Significant: true, Samples: 6}}
	over := []Row{{Name: "X-8", Old: 1, New: 1.26, Significant: true, Samples: 6}}
	if v, _ := Judge(at, DefaultRegressionPercent); !v.OK() {
		t.Error("exactly the threshold failed: only more than it is a regression")
	}
	if v, _ := Judge(over, DefaultRegressionPercent); v.OK() {
		t.Error("26% passed a 25% gate")
	}
	if v, _ := Judge(over, 50); !v.OK() {
		t.Error("26% failed a 50% gate")
	}
}

func TestJudge_aSlowdownBenchstatCallsNoiseDoesNotFail(t *testing.T) {
	v, err := Judge([]Row{{Name: "Noisy-8", Old: 1, New: 2, Significant: false, Samples: 6}}, DefaultRegressionPercent)
	if err != nil || !v.OK() {
		t.Fatalf("verdict = %+v, err = %v", v, err)
	}
}

func TestJudge_aMissingBenchmarkFails(t *testing.T) {
	v, err := Judge(parse(t, comparisonCSV)[2:3], DefaultRegressionPercent)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK() || !slices.Equal(v.Missing, []string{"Gone-8"}) {
		t.Fatalf("verdict = %+v, want Gone-8 missing", v)
	}
}

func TestJudge_tooFewSamplesRefusesToJudge(t *testing.T) {
	_, err := Judge([]Row{{Name: "Few-8", Old: 1, New: 3, Samples: MinSamples - 1}}, DefaultRegressionPercent)
	if !errors.Is(err, ErrTooFewSamples) {
		t.Fatalf("err = %v, want ErrTooFewSamples", err)
	}
}

func TestJudge_noRowsIsAnEmptyVerdict(t *testing.T) {
	v, err := Judge(nil, DefaultRegressionPercent)
	if err != nil || v.Compared != 0 {
		t.Fatalf("verdict = %+v, err = %v", v, err)
	}
}
