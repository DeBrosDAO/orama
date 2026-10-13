package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

const tabPadding = 2

// WriteSummary prints the report for a person. It states the machine the numbers come from first,
// and a disagreement between validators before anything else.
func (r Report) WriteSummary(w io.Writer) error {
	ew := &errWriter{w: w}
	ew.printf("oramad-bench %s profile on %s\n", r.Profile, r.Chain.ChainID)
	ew.printf("measured on: %s/%s, %d CPUs, Go %s. %s\n", r.Environment.GOOS, r.Environment.GOARCH, r.Environment.CPUs, r.Environment.GoVersion, r.Environment.Note)
	ew.printf("chain: %d validators, blocks %d to %d (about %.2fs apart), vote extensions %s, block max gas %d\n",
		r.Chain.Validators, r.Chain.StartHeight, r.Chain.EndHeight, r.Chain.BlockIntervalSeconds, onOff(r.Chain.VoteExtensionsOn), r.Chain.BlockMaxGas)
	ew.printf("load: %d accounts, broadcast_tx_%s, fee %dx the base fee\n\n", r.Chain.Accounts, r.Chain.BroadcastMode, r.Chain.FeeMultiple)
	writeAppHash(ew, r)
	for _, t := range r.Types {
		writeType(ew, t)
	}
	for _, warn := range r.Warnings {
		ew.printf("warning: %s\n", warn)
	}
	return ew.err
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func writeAppHash(ew *errWriter, r Report) {
	if r.AppHashAgreed {
		ew.printf("app hash: all %d validators agree at all %d heights (%d to %d)\n\n", len(r.AppHash.Nodes), r.AppHash.HeightsChecked, r.AppHash.From, r.AppHash.To)
		return
	}
	ew.printf("!!! APP HASH MISMATCH !!! the validators disagree on state; the numbers below mean nothing.\n")
	for i, m := range r.AppHash.Mismatches {
		if i == 5 {
			ew.printf("    ... and %d more (see the JSON)\n", len(r.AppHash.Mismatches)-5)
			break
		}
		ew.printf("    height %d %s: %v\n", m.Height, m.Field, m.Values)
	}
	ew.printf("\n")
}

func writeType(ew *errWriter, t Type) {
	ew.printf("== %s: %s\n", t.Name, t.Description)
	if t.Skipped {
		ew.printf("   SKIPPED: %s\n\n", t.Reason)
		return
	}
	ew.printf("   gas limit %d per transaction, gas price offered %s norama\n", t.GasLimit, t.GasPrice)
	tw := tabwriter.NewWriter(ew, 3, 0, tabPadding, ' ', 0)
	fmt.Fprintln(tw, "   load\toffered\tincluded/s\tok\trefused\tfailed\tlost\tp50\tp90\tp99\tbacklog\tmempool\tbase fee")
	for _, s := range t.Steps {
		fmt.Fprintf(tw, "   %s\t%d\t%.1f\t%d\t%d\t%d\t%d\t%.2fs\t%.2fs\t%.2fs\t%d\t%d\t%s\n",
			stepLabel(s), s.Offered, s.AchievedRate, s.Included, s.Rejected+s.NotSent, s.FailedInBlock, s.Unresolved,
			s.Latency.P50, s.Latency.P90, s.Latency.P99, s.Backlog, s.MempoolPeak, feeLabel(s.BaseFee))
	}
	tw.Flush()
	writeStepNotes(ew, t.Steps)
	writeSaturation(ew, t.Saturation)
	writeReasons(ew, t)
}

// writeStepNotes explains the marks a reader of the table needs: percentiles cover only the
// transactions that reached a block, and a sender that fell behind measured itself.
func writeStepNotes(ew *errWriter, steps []Step) {
	lost, behind := false, []string{}
	for _, s := range steps {
		lost = lost || s.Unresolved > 0
		if s.SenderBehind {
			behind = append(behind, stepLabel(s))
		}
	}
	if lost {
		ew.printf("   lost: transactions that reached no block before the drain time ran out; the percentiles cover only those that did\n")
	}
	if len(behind) > 0 {
		ew.printf("   sender behind (%s): the load generator's own queue grew, so the chain was not the only limit; these steps are not judged\n", strings.Join(behind, ", "))
	}
}

func stepLabel(s Step) string {
	if s.Mode == ModeClosed {
		return fmt.Sprintf("closed w=%d", s.Window)
	}
	return fmt.Sprintf("open %g/s", s.OfferedRate)
}

func feeLabel(f BaseFee) string {
	if f.Start == "" {
		return "-"
	}
	if f.Start == f.Max && f.Start == f.End {
		return f.Start
	}
	return fmt.Sprintf("%s>%s (max %s)", f.Start, f.End, f.Max)
}

func writeSaturation(ew *errWriter, s Saturation) {
	var parts []string
	if s.OpenMaxSustained > 0 || s.OpenSaturatedAt > 0 {
		sat := "no offered rate saturated it"
		if s.OpenSaturatedAt > 0 {
			sat = fmt.Sprintf("saturated at %g tx/s", s.OpenSaturatedAt)
		}
		parts = append(parts, fmt.Sprintf("open-loop: sustained up to %g tx/s, %s", s.OpenMaxSustained, sat))
	}
	if s.ClosedMaxRate > 0 {
		parts = append(parts, fmt.Sprintf("closed-loop: best %.1f tx/s at window %d", s.ClosedMaxRate, s.ClosedBestWindow))
	}
	if s.Inconclusive > 0 {
		parts = append(parts, fmt.Sprintf("%d step(s) were not judged (the send window held fewer than %d block intervals, or the sender fell behind)", s.Inconclusive, MinIntervalsToJudge))
	}
	if len(parts) == 0 {
		parts = append(parts, "no saturation analysis (no step ran)")
	}
	ew.printf("   saturation: %s\n", strings.Join(parts, "; "))
}

// writeReasons lists why transactions were refused or failed, most common first.
func writeReasons(ew *errWriter, t Type) {
	refused, failed := map[string]int{}, map[string]int{}
	for _, s := range t.Steps {
		for k, v := range s.RejectionReasons {
			refused[k] += v
		}
		for k, v := range s.FailureReasons {
			failed[k] += v
		}
	}
	for _, group := range []struct {
		title string
		m     map[string]int
	}{{"refused by CheckTx", refused}, {"failed in a block", failed}} {
		keys := make([]string, 0, len(group.m))
		for k := range group.m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return group.m[keys[i]] > group.m[keys[j]] })
		for _, k := range keys {
			ew.printf("   %s: %d x %s\n", group.title, group.m[k], k)
		}
	}
	ew.printf("\n")
}

// errWriter keeps the first write error so a summary needs one check at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	e.err = err
	return n, err
}

func (e *errWriter) printf(format string, args ...any) {
	fmt.Fprintf(e, format, args...)
}
