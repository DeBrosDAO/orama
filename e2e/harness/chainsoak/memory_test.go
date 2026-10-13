package chainsoak

import (
	"encoding/json"
	"testing"
)

func memoryTimeline(n int, mb func(i int) int, restarts func(i int) int) Timeline {
	return build(n, func(i int, s *Sample) {
		svc := Service{AnonMB: mb(i), DataBytes: 1 << 30}
		if restarts != nil {
			svc.Restarts = restarts(i)
		}
		s.Nodes[0].Services = map[string]Service{unit: svc}
	})
}

func TestAnalyze_monotonicMemoryGrowthFails(t *testing.T) {
	tl := memoryTimeline(60, func(i int) int { return 300 + 4*i }, nil) // +236 MiB over 5 hours, never falling
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[MemoryGrowth] != 1 || got[0].Node != "node-1/"+unit {
		t.Fatalf("want one memory-growth finding for node-1: %+v", got)
	}
}

func TestAnalyze_aSawtoothThatDoesNotGrowPasses(t *testing.T) {
	tl := memoryTimeline(60, func(i int) int { return 300 + 80*(i%5)/4 }, nil) // collector sawtooth, 300..380
	if kinds(Analyze(tl, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("a sawtooth around a flat level is not a leak")
	}
}

func TestAnalyze_noisyGrowthThatKeepsFallingPasses(t *testing.T) {
	tl := memoryTimeline(60, func(i int) int { return 300 + 3*i + 60*(i%2) }, nil) // rises, but every other step falls 57 MiB
	if kinds(Analyze(tl, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("a series that falls half the time is not monotonic growth")
	}
}

func TestAnalyze_growthBelowTheFloorOrPercentPasses(t *testing.T) {
	small := memoryTimeline(60, func(i int) int { return 300 + i/3 }, nil) // +20 MiB: under the floor
	if kinds(Analyze(small, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("20 MiB of growth is under the floor")
	}
	big := memoryTimeline(60, func(i int) int { return 4000 + 2*i }, nil) // +118 MiB but only 3%
	if kinds(Analyze(big, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("3% growth of a large daemon is under the percentage")
	}
}

func TestAnalyze_warmupGrowthIsIgnored(t *testing.T) {
	tl := memoryTimeline(100, func(i int) int { return min(300+40*i, 700) }, nil) // fills in the first 10 samples, then flat
	if kinds(Analyze(tl, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("the cache filling in the first tenth of the run is not a leak")
	}
}

func TestAnalyze_tooFewSamplesAreNotJudged(t *testing.T) {
	tl := memoryTimeline(10, func(i int) int { return 300 + 50*i }, nil)
	if kinds(Analyze(tl, DefaultLimits()))[MemoryGrowth] != 0 {
		t.Fatal("ten samples cannot tell a leak from warm-up")
	}
}

func TestAnalyze_aRestartIsAFindingAndSplitsTheSeries(t *testing.T) {
	// Memory climbs 300 -> 520 in the first half, the daemon restarts, and it climbs again in the
	// second half: each half alone is judged, and the restart is reported.
	tl := memoryTimeline(120, func(i int) int { return 300 + 4*(i%60) }, func(i int) int {
		if i >= 60 {
			return 1
		}
		return 0
	})
	got := kinds(Analyze(tl, DefaultLimits()))
	if got[Restart] != 1 {
		t.Fatalf("want one restart finding: %+v", got)
	}
}

func TestAnalyze_aServiceKeepsItsOwnSeries(t *testing.T) {
	tl := build(60, func(i int, s *Sample) {
		s.Nodes[0].Services = map[string]Service{
			unit:                           {AnonMB: 300},
			"orama-global-indexer.service": {AnonMB: 100 + 3*i},
		}
	})
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[MemoryGrowth] != 1 || got[0].Node != "node-1/orama-global-indexer.service" {
		t.Fatalf("only the indexer grows: %+v", got)
	}
}

func TestSummarize_numbersOfAHealthyRun(t *testing.T) {
	tl := build(13, func(i int, s *Sample) {
		s.Nodes[0].Services = map[string]Service{unit: {AnonMB: 300 + i, DataBytes: int64(1<<30) + int64(i)*(1<<20)}}
		s.Nodes[0].MempoolTxs = i
	})
	sum := Summarize(tl, nil)
	if sum.Samples != 13 || sum.Blocks != 12*60 || sum.TransfersSent != 12*30 || sum.MaxMempoolTxs != 12 || sum.MaxIndexerLag != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if got := sum.BlockIntervalS; got < 4.9 || got > 5.1 {
		t.Fatalf("mean block interval %.2f s, want 5", got)
	}
	svc := sum.Services["node-1/"+unit]
	if svc.FirstMB != 300 || svc.LastMB != 312 || svc.MinMB != 300 || svc.MaxMB != 312 {
		t.Fatalf("service summary: %+v", svc)
	}
	if svc.SlopeMBPerHour < 11.9 || svc.SlopeMBPerHour > 12.1 {
		t.Fatalf("slope %.2f MiB/h, want 12 (one MiB per 5 minutes)", svc.SlopeMBPerHour)
	}
	if want := int64(12*(1<<20)) * 24; svc.DataGrowthBytesPerDay != want {
		t.Fatalf("data growth %d B/day, want %d", svc.DataGrowthBytesPerDay, want)
	}
}

func TestSummarize_emptyAndSingle(t *testing.T) {
	if s := Summarize(Timeline{}, nil); s.Samples != 0 {
		t.Fatalf("%+v", s)
	}
	s := Summarize(build(1, nil), []Finding{{Kind: Restart}, {Kind: Restart}})
	if s.Blocks != 0 || s.BlockIntervalS != 0 || s.FindingsPerKind[Restart] != 2 || s.Services["node-1/"+unit].SlopeMBPerHour != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestTimeline_roundTripsThroughJSON(t *testing.T) {
	tl := build(3, nil)
	raw, err := json.Marshal(tl)
	if err != nil {
		t.Fatal(err)
	}
	var back Timeline
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Samples) != 3 || back.Samples[2].Nodes[1].Services[unit].AnonMB != 300 || back.Samples[1].Elapsed != interval {
		t.Fatalf("round trip lost data: %+v", back.Samples[1])
	}
}

func TestGrowing_aSeriesOfOneSampleIsNotJudged(t *testing.T) {
	if _, _, _, ok := growing([]int{100}, Limits{}); ok {
		t.Fatal("one sample was judged growing")
	}
	if _, _, _, ok := growing(nil, Limits{}); ok {
		t.Fatal("no sample was judged growing")
	}
}
