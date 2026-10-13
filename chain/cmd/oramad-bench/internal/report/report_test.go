package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/apphash"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

var t0 = time.Unix(1_700_000_000, 0)

func at(sec float64) time.Time { return t0.Add(time.Duration(sec * float64(time.Second))) }

func included(sent, done float64) watch.Record {
	return watch.Record{SentAt: at(sent), DoneAt: at(done), Outcome: watch.Included, Height: 1}
}

func TestReasonKey_groupsTheSameFailure(t *testing.T) {
	a := ReasonKey("account sequence mismatch, expected 12, got 14: incorrect account sequence")
	b := ReasonKey("account sequence mismatch, expected 7, got 9: incorrect account sequence")
	if a != b || strings.ContainsAny(a, "0123456789") {
		t.Fatalf("keys differ or keep numbers: %q vs %q", a, b)
	}
	if ReasonKey("") != "(no log)" || ReasonKey("   ") != "(no log)" {
		t.Fatal("an empty log must have a name")
	}
	if got := ReasonKey(strings.Repeat("x", 500)); len(got) > maxReasonLen+3 {
		t.Fatalf("key of %d bytes", len(got))
	}
}

func TestBuildStep_countsOutcomesAndMeasuresLatency(t *testing.T) {
	recs := map[string]watch.Record{
		"a": included(0, 1), "b": included(0.5, 2.5), "c": included(1, 4),
		"d": {SentAt: at(1), DoneAt: at(3), Outcome: watch.FailedInBlock, Code: 11, Log: "out of gas in location: gas 150000"},
		"e": {SentAt: at(2), DoneAt: at(2), Outcome: watch.Rejected, Code: 19, Log: "insufficient fee; got: 5 required: 40"},
		"f": {SentAt: at(2), DoneAt: at(2), Outcome: watch.NotSent},
		"g": {SentAt: at(2)},
	}
	s, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 4, Start: t0, SendEnd: at(4), Records: recs})
	if err != nil {
		t.Fatal(err)
	}
	if s.Offered != 7 || s.Included != 3 || s.FailedInBlock != 1 || s.Rejected != 1 || s.NotSent != 1 || s.Unresolved != 1 {
		t.Fatalf("counts %+v", s)
	}
	if s.Latency.Count != 4 || s.Latency.Min != 1 || s.Latency.Max != 3 {
		t.Fatalf("latency %+v: the failed-in-block transaction reached a block and counts", s.Latency)
	}
	if len(s.FailureReasons) != 1 || len(s.RejectionReasons) != 1 {
		t.Fatalf("reasons %v %v", s.FailureReasons, s.RejectionReasons)
	}
}

func TestBuildStep_achievedRateShiftsTheWindowByTheMedianLatency(t *testing.T) {
	// A steady 2 tx/s for 10s with a constant 2s latency: sent at 0, 0.5, ... 9.5; included 2s later.
	recs := map[string]watch.Record{}
	for i := 0; i < 20; i++ {
		sent := float64(i) * 0.5
		recs[fmt.Sprint(i)] = included(sent, sent+2)
	}
	s, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 2, Start: t0, SendEnd: at(10), Records: recs})
	if err != nil {
		t.Fatal(err)
	}
	if s.AchievedRate != 2 || s.IncludedInWindow != 20 {
		t.Fatalf("achieved %.2f (%d): an exactly-kept-up step must read as its offered rate", s.AchievedRate, s.IncludedInWindow)
	}
	if s.Backlog != 3 {
		t.Fatalf("backlog %d: three transactions were still waiting when the sending stopped", s.Backlog)
	}
	if !s.Sustained() {
		t.Fatal("a kept-up step was not sustained")
	}
}

func TestBuildStep_aSaturatedChainAchievesLessThanOffered(t *testing.T) {
	// 10 tx/s offered for 10s; the chain includes 4 tx/s, in a burst per second.
	recs := map[string]watch.Record{}
	for i := 0; i < 100; i++ {
		done := 1 + float64(i/4)
		recs[fmt.Sprint(i)] = included(float64(i)*0.1, done)
	}
	s, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 10, Start: t0, SendEnd: at(10), Records: recs})
	if err != nil {
		t.Fatal(err)
	}
	if s.AchievedRate > 5 || s.Sustained() {
		t.Fatalf("achieved %.1f of 10 offered but the step reads as sustained=%v", s.AchievedRate, s.Sustained())
	}
	if s.Backlog == 0 {
		t.Fatal("a saturated step left no backlog")
	}
}

func TestBuildStep_emptyStep(t *testing.T) {
	s, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 1, Start: t0, SendEnd: t0, Records: nil})
	if err != nil {
		t.Fatal(err)
	}
	if s.Offered != 0 || s.AchievedRate != 0 || s.Latency.Count != 0 || s.Sustained() {
		t.Fatalf("empty step %+v", s)
	}
}

func TestBuildStep_blocksFeesAndMempool(t *testing.T) {
	blocks := []watch.BlockStat{
		{Height: 1, Time: at(0), Txs: 10, GasUsed: 100, GasWanted: 200},
		{Height: 2, Time: at(2), Txs: 30, GasUsed: 300, GasWanted: 600},
	}
	fees := []watch.FeeSample{
		{Height: 1, At: at(0), BaseFee: "9"}, {Height: 2, At: at(2), BaseFee: "100"}, {Height: 3, At: at(4), BaseFee: "12"},
	}
	mem := []watch.MempoolSample{{T: at(0), Node: 0, Txs: 5}, {T: at(1), Node: 1, Txs: 80}}
	s, err := BuildStep(StepInput{Mode: ModeClosed, Window: 8, Start: t0, SendEnd: at(4), Blocks: blocks, Fees: fees, Mempool: mem, BlockMaxGas: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if s.Blocks.Count != 2 || s.Blocks.AvgTxs != 20 || s.Blocks.MaxTxs != 30 || s.Blocks.IntervalSeconds != 2 || s.Blocks.GasWantedOfLimit != 0.4 {
		t.Fatalf("blocks %+v", s.Blocks)
	}
	if s.BaseFee.Start != "9" || s.BaseFee.End != "12" || s.BaseFee.Max != "100" {
		t.Fatalf("a base fee of 100 is larger than 12 and 9 even though it sorts first as text: %+v", s.BaseFee)
	}
	if s.MempoolPeak != 80 {
		t.Fatalf("mempool peak %d", s.MempoolPeak)
	}
}

func TestDownsample_keepsEveryNodesSeries(t *testing.T) {
	var in []watch.MempoolSample
	for i := 0; i < 600; i++ {
		for node := 0; node < 4; node++ {
			in = append(in, watch.MempoolSample{T: at(float64(i)), Node: node, Txs: i})
		}
	}
	original := append([]watch.MempoolSample(nil), in...)
	out := downsample(in, 240)
	if len(out) > 240 {
		t.Fatalf("%d points", len(out))
	}
	first, last := map[int]int{}, map[int]int{}
	for node := 0; node < 4; node++ {
		first[node], last[node] = -1, -1
	}
	for _, m := range out {
		if first[m.Node] < 0 {
			first[m.Node] = m.Txs
		}
		last[m.Node] = m.Txs
	}
	for node := 0; node < 4; node++ {
		if first[node] != 0 || last[node] != 599 {
			t.Errorf("node %d: kept samples %d to %d, want 0 to 599: a stride that is a multiple of the node count must not drop a node", node, first[node], last[node])
		}
	}
	for i := 1; i < len(out); i++ {
		if out[i].T.Before(out[i-1].T) {
			t.Fatal("the downsampled series is not in time order")
		}
	}
	for i := range in {
		if in[i] != original[i] {
			t.Fatal("downsample changed its argument")
		}
	}
}

func TestDownsample_keepsTheEnds(t *testing.T) {
	var in []watch.MempoolSample
	for i := 0; i < 1000; i++ {
		in = append(in, watch.MempoolSample{T: at(float64(i)), Txs: i})
	}
	out := downsample(in, 100)
	if len(out) > 101 || out[0].Txs != 0 || out[len(out)-1].Txs != 999 {
		t.Fatalf("%d points, first %d, last %d", len(out), out[0].Txs, out[len(out)-1].Txs)
	}
	if got := downsample(in[:10], 100); len(got) != 10 {
		t.Fatalf("a short series changed: %d", len(got))
	}
}

func openStep(rate, achieved float64, offered, refused int) Step {
	return Step{Mode: ModeOpen, OfferedRate: rate, AchievedRate: achieved, Offered: offered, Rejected: refused, DurationSeconds: 30, Blocks: BlockSummary{Count: 30, IntervalSeconds: 1}}
}

func TestAnalyze_findsTheSaturationPoint(t *testing.T) {
	steps := []Step{
		openStep(200, 80, 2000, 0),
		openStep(10, 10, 100, 0),
		openStep(50, 49, 500, 0),
		openStep(100, 96, 1000, 0),
		{Mode: ModeClosed, Window: 8, AchievedRate: 40, DurationSeconds: 30, Blocks: BlockSummary{Count: 30, IntervalSeconds: 1}},
		{Mode: ModeClosed, Window: 64, AchievedRate: 97, DurationSeconds: 30, Blocks: BlockSummary{Count: 30, IntervalSeconds: 1}},
		{Mode: ModeClosed, Window: 512, AchievedRate: 90, DurationSeconds: 30, Blocks: BlockSummary{Count: 30, IntervalSeconds: 1}},
	}
	sat := Analyze(steps)
	if sat.OpenMaxSustained != 100 || sat.OpenSaturatedAt != 200 {
		t.Fatalf("open: sustained %v, saturated at %v", sat.OpenMaxSustained, sat.OpenSaturatedAt)
	}
	if sat.ClosedMaxRate != 97 || sat.ClosedBestWindow != 64 {
		t.Fatalf("closed: %v at %d", sat.ClosedMaxRate, sat.ClosedBestWindow)
	}
}

func TestAnalyze_refusalsBreakSustainedEvenAtFullThroughput(t *testing.T) {
	sat := Analyze([]Step{openStep(10, 10, 100, 0), openStep(20, 19, 200, 40)})
	if sat.OpenMaxSustained != 10 || sat.OpenSaturatedAt != 20 {
		t.Fatalf("got %+v: a step that refuses 20%% of its transactions is not sustained", sat)
	}
}

func TestAnalyze_aWindowOfFewBlockIntervalsIsNotJudged(t *testing.T) {
	short := openStep(50, 5, 500, 0)
	short.DurationSeconds = MinIntervalsToJudge - 1 // with 1 s blocks
	sat := Analyze([]Step{openStep(10, 10, 100, 0), short})
	if sat.OpenSaturatedAt != 0 || sat.OpenMaxSustained != 10 || sat.Inconclusive != 1 {
		t.Fatalf("got %+v: a window of %d block intervals says nothing about saturation", sat, MinIntervalsToJudge-1)
	}
	edge := openStep(50, 5, 500, 0)
	edge.DurationSeconds = MinIntervalsToJudge
	if Analyze([]Step{edge}).OpenSaturatedAt != 50 {
		t.Fatal("a window of exactly the minimum was not judged")
	}
	none := openStep(50, 5, 500, 0)
	none.Blocks = BlockSummary{}
	if Analyze([]Step{none}).Inconclusive != 1 {
		t.Fatal("a step that saw no block interval was judged")
	}
}

func TestAnalyze_aStepWhereTheSenderFellBehindIsNotJudged(t *testing.T) {
	behind := openStep(50, 5, 500, 0)
	behind.SenderBehind = true
	sat := Analyze([]Step{openStep(10, 10, 100, 0), behind})
	if sat.OpenSaturatedAt != 0 || sat.Inconclusive != 1 {
		t.Fatalf("got %+v: a step that measured the load generator says nothing about the chain", sat)
	}
}

func TestBuildStep_flagsASenderThatFellBehind(t *testing.T) {
	ok, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 1, Start: t0, SendEnd: at(1), QueueMax: SenderBehindDepth - 1})
	if err != nil || ok.SenderBehind {
		t.Fatalf("a queue of %d is normal: %+v, %v", SenderBehindDepth-1, ok.SenderBehind, err)
	}
	slow, err := BuildStep(StepInput{Mode: ModeOpen, Rate: 1, Start: t0, SendEnd: at(1), QueueMax: SenderBehindDepth})
	if err != nil || !slow.SenderBehind {
		t.Fatalf("a queue of %d must be flagged: %v", SenderBehindDepth, err)
	}
}

func TestAnalyze_neverSaturatedAndNoSteps(t *testing.T) {
	sat := Analyze([]Step{openStep(10, 10, 100, 0), openStep(20, 20, 200, 0)})
	if sat.OpenMaxSustained != 20 || sat.OpenSaturatedAt != 0 {
		t.Fatalf("got %+v", sat)
	}
	if empty := Analyze(nil); empty != (Saturation{}) {
		t.Fatalf("no steps gave %+v", empty)
	}
}

func sampleReport(agreed bool) Report {
	r := Report{
		Tool: Tool, Profile: "smoke", AppHashAgreed: agreed,
		Environment: Environment{GoVersion: "go1.27", GOOS: "darwin", GOARCH: "arm64", CPUs: 8, Note: "laptop localnet"},
		Chain:       Chain{ChainID: "orama-localnet-1", Validators: 4, StartHeight: 10, EndHeight: 50, BroadcastMode: "sync", FeeMultiple: 32, Accounts: 16, BlockMaxGas: 100000000, BlockIntervalSeconds: 1.1},
		AppHash:     apphash.Result{From: 10, To: 50, Nodes: []string{"n0", "n1", "n2", "n3"}, HeightsChecked: 41},
		Types: []Type{
			{Name: "bank-send", Description: "bank", GasLimit: 90000, GasPrice: "32", Steps: []Step{
				{Mode: ModeOpen, OfferedRate: 20, DurationSeconds: 10, Offered: 200, Included: 198, Rejected: 2, AchievedRate: 19.5, RejectionReasons: map[string]int{"mempool is full": 2}, BaseFee: BaseFee{Start: "1", End: "2", Max: "2"}},
			}, Saturation: Saturation{OpenMaxSustained: 20}},
			{Name: "wasm-call", Skipped: true, Reason: "the node does not serve CosmWasm"},
		},
	}
	if !agreed {
		r.AppHash.Mismatches = []apphash.Mismatch{{Height: 33, Field: apphash.FieldAppHash, Values: map[string]string{"n0": "aa", "n1": "bb"}}}
	}
	return r
}

func TestWriteSummary_showsNumbersSkipsAndRefusalReasons(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleReport(true).WriteSummary(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"all 4 validators agree at all 41 heights", "bank-send", "open 20/s", "19.5", "lost", "SKIPPED: the node does not serve CosmWasm", "2 x mempool is full", "sustained up to 20 tx/s", "laptop localnet", "1>2"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

func TestWriteSummary_anAppHashMismatchComesFirstAndLoud(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleReport(false).WriteSummary(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	i := strings.Index(out, "APP HASH MISMATCH")
	j := strings.Index(out, "bank-send")
	if i < 0 || j < 0 || i > j || !strings.Contains(out, "height 33 app_hash") {
		t.Fatalf("mismatch banner missing or after the numbers:\n%s", out)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("disk full") }

func TestWriteSummary_reportsAWriteError(t *testing.T) {
	if err := sampleReport(true).WriteSummary(failingWriter{}); err == nil {
		t.Fatal("a failed write was not reported")
	}
}

func TestWriteJSON_roundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out", "report.json")
	want := sampleReport(true)
	if err := want.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Types[0].Steps[0].AchievedRate != 19.5 || !got.Types[1].Skipped || !got.AppHashAgreed || got.Chain.ChainID != want.Chain.ChainID {
		t.Fatalf("round trip lost data: %+v", got)
	}
}
