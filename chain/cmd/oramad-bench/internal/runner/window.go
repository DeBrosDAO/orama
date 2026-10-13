package runner

import (
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

// sendEnd is when a step's load actually stopped. A step normally sends for its whole duration,
// but a type can run out of transactions first (a finite stock of listings or bundles) and a
// closed loop stops when its signed transactions are gone. Throughput is measured over the window
// the load was on, so a step that stopped early must not be divided by the time it was meant to run.
//
// An open-loop step that offered n transactions at a rate offered them for n/rate seconds. A
// closed-loop step ended when its last transaction was offered.
func sendEnd(spec profile.Step, start time.Time, offered int, driveEnd time.Time) time.Time {
	nominal := start.Add(spec.Duration)
	if spec.Mode == report.ModeOpen {
		if spec.Rate <= 0 {
			return nominal
		}
		end := start.Add(time.Duration(float64(offered) / spec.Rate * float64(time.Second)))
		if end.After(nominal) {
			return nominal
		}
		return end
	}
	if driveEnd.Before(nominal) {
		return driveEnd
	}
	return nominal
}
