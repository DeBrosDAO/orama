package report

import "sort"

const (
	// SustainedFraction is how much of the offered rate must be included while the load is on for
	// the chain to count as keeping up. It is below 1 because blocks are discrete: a short step
	// counts whole blocks' worth of transactions, so a few percent is noise.
	SustainedFraction = 0.9
	// MaxFailedFraction is how many of a step's transactions may be refused or fail before the
	// step no longer counts as sustained, whatever its throughput.
	MaxFailedFraction = 0.05
)

// MinIntervalsToJudge is how many block intervals a step's send window must hold for its
// throughput to say anything. Blocks are discrete, so the inclusions counted in a window are off by
// up to one block's worth: over 20 intervals that is 5%, inside the margin SustainedFraction leaves.
const MinIntervalsToJudge = 20

// Conclusive reports whether the step can be judged: its send window held enough block intervals,
// and the load generator kept up with the rate it was asked for. A step where the sender fell
// behind measured the sender.
func (s Step) Conclusive() bool {
	if s.SenderBehind || s.Blocks.IntervalSeconds <= 0 {
		return false
	}
	return s.DurationSeconds/s.Blocks.IntervalSeconds >= MinIntervalsToJudge
}

// Sustained reports whether an open-loop step was kept up with.
func (s Step) Sustained() bool {
	if s.Mode != ModeOpen || s.Offered == 0 || s.OfferedRate <= 0 {
		return false
	}
	bad := float64(s.Rejected+s.FailedInBlock+s.NotSent) / float64(s.Offered)
	return s.AchievedRate >= SustainedFraction*s.OfferedRate && bad <= MaxFailedFraction
}

// Analyze finds where a type stopped scaling from its steps. The open-loop saturation point is the
// lowest offered rate that was not sustained; the highest sustained rate below it is the
// open-loop maximum. The closed-loop maximum is the best throughput any window reached.
func Analyze(steps []Step) Saturation {
	var sat Saturation
	var open []Step
	for _, s := range steps {
		if !s.Conclusive() {
			sat.Inconclusive++
			continue
		}
		switch s.Mode {
		case ModeOpen:
			open = append(open, s)
		case ModeClosed:
			if s.AchievedRate > sat.ClosedMaxRate {
				sat.ClosedMaxRate, sat.ClosedBestWindow = s.AchievedRate, s.Window
			}
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].OfferedRate < open[j].OfferedRate })
	for _, s := range open {
		if !s.Sustained() {
			sat.OpenSaturatedAt = s.OfferedRate
			break
		}
		sat.OpenMaxSustained = s.OfferedRate
	}
	return sat
}
