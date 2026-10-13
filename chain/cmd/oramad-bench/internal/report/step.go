package report

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/metrics"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

const (
	// SenderBehindDepth is how many transactions waiting for one worker mean the sender, not the
	// chain, is the limit: a worker that keeps up has one or two.
	SenderBehindDepth = 64
	// maxReasonLen cuts a chain log to a key that groups the same failure.
	maxReasonLen = 96
	// maxMempoolPoints bounds the mempool series kept per step in the report.
	maxMempoolPoints = 240
)

var digits = regexp.MustCompile(`[0-9]+`)

// ReasonKey turns a CheckTx or execution log into a key that groups the same failure: numbers
// (sequences, amounts, gas) are replaced, and the text is cut.
func ReasonKey(log string) string {
	key := strings.TrimSpace(digits.ReplaceAllString(log, "#"))
	if key == "" {
		return "(no log)"
	}
	if len(key) > maxReasonLen {
		key = key[:maxReasonLen] + "..."
	}
	return key
}

// StepInput is everything a step's numbers are computed from.
type StepInput struct {
	Mode   string
	Rate   float64
	Window int
	// Start is when the first transaction was due; SendEnd is when the send window closed.
	Start, SendEnd time.Time
	// Records are the outcomes of every transaction the step offered.
	Records     map[string]watch.Record
	Blocks      []watch.BlockStat
	Mempool     []watch.MempoolSample
	Fees        []watch.FeeSample
	QueueMax    int
	BlockMaxGas int64
}

// BuildStep computes the report of one step.
func BuildStep(in StepInput) (Step, error) {
	dur := in.SendEnd.Sub(in.Start).Seconds()
	s := Step{
		Mode: in.Mode, OfferedRate: in.Rate, Window: in.Window, DurationSeconds: dur,
		Offered: len(in.Records), SenderQueueMax: in.QueueMax,
		RejectionReasons: map[string]int{}, FailureReasons: map[string]int{},
	}
	var latencies []float64
	for _, r := range in.Records {
		count(&s, r)
		if r.Outcome == watch.Included || r.Outcome == watch.FailedInBlock {
			latencies = append(latencies, r.Latency().Seconds())
		}
	}
	if len(s.RejectionReasons) == 0 {
		s.RejectionReasons = nil
	}
	if len(s.FailureReasons) == 0 {
		s.FailureReasons = nil
	}
	lat, err := metrics.Summarize(latencies)
	if err != nil {
		return Step{}, err
	}
	s.Latency = lat
	s.IncludedInWindow, s.AchievedRate = achieved(in, lat.P50)
	s.Backlog = backlog(in)
	s.Blocks = summarizeBlocks(in.Blocks, in.BlockMaxGas)
	s.Mempool = downsample(in.Mempool, maxMempoolPoints)
	s.MempoolPeak = watch.PeakTxs(in.Mempool)
	s.SenderBehind = in.QueueMax >= SenderBehindDepth
	s.BaseFee = summarizeFee(in.Fees)
	return s, nil
}

func count(s *Step, r watch.Record) {
	switch r.Outcome {
	case watch.Included:
		s.Included++
	case watch.FailedInBlock:
		s.FailedInBlock++
		s.FailureReasons[ReasonKey(r.Log)]++
	case watch.Rejected:
		s.Rejected++
		s.RejectionReasons[ReasonKey(r.Log)]++
	case watch.NotSent:
		s.NotSent++
	default:
		s.Unresolved++
	}
}

// achieved counts the transactions that executed while the load was on and divides by its length.
// The window is shifted by the median latency: a transaction sent at the start of the step cannot
// be included before one latency has passed, so counting from the start would blame the pipeline's
// depth on the chain. At a rate the chain keeps up with, the shifted window holds as many
// inclusions as the offered load; above it, only as many as the chain can take.
func achieved(in StepInput, shiftSeconds float64) (int, float64) {
	dur := in.SendEnd.Sub(in.Start).Seconds()
	if dur <= 0 {
		return 0, 0
	}
	shift := time.Duration(shiftSeconds * float64(time.Second))
	from, to := in.Start.Add(shift), in.SendEnd.Add(shift)
	n := 0
	for _, r := range in.Records {
		if r.Outcome == watch.Included && !r.DoneAt.Before(from) && !r.DoneAt.After(to) {
			n++
		}
	}
	return n, float64(n) / dur
}

// backlog is how many transactions were sent and had no block yet when the send window closed.
func backlog(in StepInput) int {
	n := 0
	for _, r := range in.Records {
		switch r.Outcome {
		case watch.Included, watch.FailedInBlock:
			if r.DoneAt.After(in.SendEnd) {
				n++
			}
		case watch.Pending:
			n++
		}
	}
	return n
}

// BuildBlocks summarizes blocks for the report; maxGas is the chain's block gas limit.
func BuildBlocks(blocks []watch.BlockStat, maxGas int64) BlockSummary {
	return summarizeBlocks(blocks, maxGas)
}

func summarizeBlocks(blocks []watch.BlockStat, maxGas int64) BlockSummary {
	var b BlockSummary
	b.Count = len(blocks)
	if b.Count == 0 {
		return b
	}
	var txs, used, wanted int64
	for _, blk := range blocks {
		txs += int64(blk.Txs)
		used += blk.GasUsed
		wanted += blk.GasWanted
		b.MaxTxs = max(b.MaxTxs, blk.Txs)
		b.MaxGasWanted = max(b.MaxGasWanted, blk.GasWanted)
	}
	n := float64(len(blocks))
	b.AvgTxs, b.AvgGasUsed, b.AvgGasWanted = float64(txs)/n, float64(used)/n, float64(wanted)/n
	if maxGas > 0 {
		b.GasWantedOfLimit = b.AvgGasWanted / float64(maxGas)
	}
	if len(blocks) > 1 {
		span := blocks[len(blocks)-1].Time.Sub(blocks[0].Time).Seconds()
		b.IntervalSeconds = span / float64(len(blocks)-1)
	}
	return b
}

func summarizeFee(fees []watch.FeeSample) BaseFee {
	if len(fees) == 0 {
		return BaseFee{}
	}
	out := BaseFee{Start: fees[0].BaseFee, End: fees[len(fees)-1].BaseFee, Max: fees[0].BaseFee, Samples: fees}
	for _, f := range fees {
		if feeGreater(f.BaseFee, out.Max) {
			out.Max = f.BaseFee
		}
	}
	return out
}

// feeGreater compares two decimal integers given as strings, without losing precision.
func feeGreater(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

// downsample keeps at most limit points, evenly from every node's own series and always the first
// and last of each, in time order. It does not change its argument.
func downsample(samples []watch.MempoolSample, limit int) []watch.MempoolSample {
	if len(samples) <= limit {
		return samples
	}
	byNode := map[int][]watch.MempoolSample{}
	for _, m := range samples {
		byNode[m.Node] = append(byNode[m.Node], m)
	}
	perNode := max(limit/len(byNode), 2)
	var out []watch.MempoolSample
	for _, series := range byNode {
		out = append(out, thin(series, perNode)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].T.Equal(out[j].T) {
			return out[i].T.Before(out[j].T)
		}
		return out[i].Node < out[j].Node
	})
	return out
}

// thin keeps at most n of the series, evenly, including its first and last.
func thin(series []watch.MempoolSample, n int) []watch.MempoolSample {
	if len(series) <= n {
		return series
	}
	out := make([]watch.MempoolSample, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, series[i*(len(series)-1)/(n-1)])
	}
	return out
}
