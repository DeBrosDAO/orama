package chainsoak

import (
	"fmt"
	"testing"
	"time"
)

const (
	interval = 5 * time.Minute
	unit     = "orama-global-chain.service"
)

var t0 = time.Unix(1_800_000_000, 0).UTC()

// build makes a timeline of n samples, three validators each, that a healthy chain would leave:
// blocks every 5 s, the same hashes everywhere, a flat 300 MiB service, an indexer a block behind.
// tweak edits each sample.
func build(n int, tweak func(i int, s *Sample)) Timeline {
	tl := Timeline{Started: t0, Duration: time.Duration(n) * interval, Interval: interval}
	for i := 0; i < n; i++ {
		at := t0.Add(time.Duration(i) * interval)
		height := int64(1000 + i*60)
		s := Sample{At: at, Elapsed: at.Sub(t0), AgreedHeight: height - 1, Sent: i * 30}
		for _, name := range []string{"node-1", "node-2", "node-3"} {
			s.Nodes = append(s.Nodes, Node{
				Name: name, Height: height, BlockHash: fmt.Sprintf("B%d", height-1), AppHash: fmt.Sprintf("A%d", height-1),
				IndexerCursor: height - 1, IndexerLag: 1,
				Services: map[string]Service{unit: {AnonMB: 300, DataBytes: 1 << 30}},
			})
		}
		if tweak != nil {
			tweak(i, &s)
		}
		tl.Samples = append(tl.Samples, s)
	}
	return tl
}

func kinds(fs []Finding) map[Kind]int {
	out := map[Kind]int{}
	for _, f := range fs {
		out[f.Kind]++
	}
	return out
}

func TestAnalyze_healthyRunHasNoFinding(t *testing.T) {
	if got := Analyze(build(40, nil), DefaultLimits()); len(got) != 0 {
		t.Fatalf("a healthy timeline has findings: %+v", got)
	}
}

func TestAnalyze_noSamples(t *testing.T) {
	got := Analyze(Timeline{}, DefaultLimits())
	if len(got) != 1 || got[0].Kind != NoSamples || !got[0].Fatal() {
		t.Fatalf("an empty timeline: %+v", got)
	}
}

func TestAnalyze_oneSampleIsNotAFailure(t *testing.T) {
	if got := Analyze(build(1, nil), DefaultLimits()); len(got) != 0 {
		t.Fatalf("one sample has nothing to compare over time: %+v", got)
	}
}

func TestAnalyze_divergence(t *testing.T) {
	tl := build(10, func(i int, s *Sample) {
		if i == 6 {
			s.Nodes[2].AppHash = "FORKED"
		}
	})
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[Divergence] != 1 || got[0].Node != "node-3" || !got[0].Fatal() {
		t.Fatalf("want one divergence on node-3: %+v", got)
	}
}

func TestAnalyze_divergenceOfTheBlockHashAlone(t *testing.T) {
	tl := build(3, func(i int, s *Sample) {
		if i == 1 {
			s.Nodes[1].BlockHash = "OTHER"
		}
	})
	if kinds(Analyze(tl, DefaultLimits()))[Divergence] != 1 {
		t.Fatal("a different block hash at one height is a fork")
	}
}

func TestAnalyze_aNodeWithoutAReadingIsNotComparedAndNotDivergent(t *testing.T) {
	tl := build(5, func(i int, s *Sample) {
		s.Nodes[1].AppHash, s.Nodes[1].BlockHash = "", ""
	})
	if kinds(Analyze(tl, DefaultLimits()))[Divergence] != 0 {
		t.Fatal("an empty hash is no reading, not a different hash")
	}
}

func TestAnalyze_stallWhenTheHeadStops(t *testing.T) {
	tl := build(12, func(i int, s *Sample) {
		if i >= 4 { // the head freezes at the height of sample 3
			for j := range s.Nodes {
				s.Nodes[j].Height = int64(1000 + 3*60)
			}
		}
	})
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[Stall] != 1 || !got[0].Fatal() {
		t.Fatalf("want one stall: %+v", got)
	}
}

func TestAnalyze_aShortPauseIsNoStall(t *testing.T) {
	tl := build(12, func(i int, s *Sample) {
		if i == 5 || i == 6 { // two samples (10 min) without progress, then it moves again
			for j := range s.Nodes {
				s.Nodes[j].Height = int64(1000 + 4*60)
			}
		}
	})
	if kinds(Analyze(tl, DefaultLimits()))[Stall] != 0 {
		t.Fatal("a pause shorter than StallAfter that ends is not a halt")
	}
}

func TestAnalyze_oneNodeBehind(t *testing.T) {
	tl := build(10, func(i int, s *Sample) {
		if i >= 5 {
			s.Nodes[0].Height -= 500
		}
	})
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[NodeBehind] != 1 || got[0].Node != "node-1" {
		t.Fatalf("want node-1 behind once: %+v", got)
	}
}

func TestAnalyze_aNodeCatchingUpForSeveralSamples(t *testing.T) {
	tl := build(10, func(i int, s *Sample) {
		if i >= 2 && i <= 4 {
			s.Nodes[1].CatchingUp = true
		}
	})
	if kinds(Analyze(tl, DefaultLimits()))[NodeBehind] != 1 {
		t.Fatal("three samples in a row of catching up is a node that cannot keep up")
	}
	two := build(10, func(i int, s *Sample) {
		if i == 2 || i == 3 {
			s.Nodes[1].CatchingUp = true
		}
	})
	if kinds(Analyze(two, DefaultLimits()))[NodeBehind] != 0 {
		t.Fatal("two samples is a hiccup")
	}
}

func TestAnalyze_indexerLag(t *testing.T) {
	tl := build(12, func(i int, s *Sample) {
		if i >= 6 {
			s.Nodes[2].IndexerLag = 900
		}
	})
	got := Analyze(tl, DefaultLimits())
	if kinds(got)[IndexerLag] != 1 || got[0].Node != "node-3" {
		t.Fatalf("want node-3's indexer behind: %+v", got)
	}
	brief := build(12, func(i int, s *Sample) {
		if i == 6 || i == 7 {
			s.Nodes[2].IndexerLag = 900
		}
	})
	if kinds(Analyze(brief, DefaultLimits()))[IndexerLag] != 0 {
		t.Fatal("two samples of lag is a catch-up after a burst")
	}
}

func TestAnalyze_mempoolThatDoesNotDrain(t *testing.T) {
	tl := build(12, func(i int, s *Sample) {
		if i >= 3 {
			s.Nodes[0].MempoolTxs = 4000
		}
	})
	if kinds(Analyze(tl, DefaultLimits()))[MempoolStuck] != 1 {
		t.Fatal("a mempool full for six samples in a row is stuck")
	}
}

func TestFinding_fatalKinds(t *testing.T) {
	for kind, want := range map[Kind]bool{Divergence: true, Stall: true, NoSamples: true, MemoryGrowth: false, Restart: false, NodeBehind: false} {
		if got := (Finding{Kind: kind}).Fatal(); got != want {
			t.Errorf("%s fatal = %v, want %v", kind, got, want)
		}
	}
}
