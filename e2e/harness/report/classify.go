package report

import "github.com/DeBrosOfficial/network/e2e/harness/gotest"

// Flakiness labels of a failure.
const (
	// FlakinessDeterministic failed again when re-run alone.
	FlakinessDeterministic = "deterministic"
	// FlakinessFlaky passed when re-run alone. It is still a failure: the
	// verdict is computed from the original run only.
	FlakinessFlaky = "flaky"
	// FlakinessUnknown was not re-run (a package-level failure, or no re-run).
	FlakinessUnknown = "unknown"
)

// Classify labels each failed test by the outcome of its targeted re-run,
// keyed by gotest.Result.Key. It only labels: nothing here can turn a failure
// into a pass.
func Classify(failed, rerun []gotest.Result) map[string]string {
	again := map[string]string{}
	for _, r := range rerun {
		if r.Test != "" {
			again[r.Key()] = r.Action
		}
	}
	labels := make(map[string]string, len(failed))
	for _, f := range failed {
		switch again[f.Key()] {
		case gotest.ActionFail:
			labels[f.Key()] = FlakinessDeterministic
		case gotest.ActionPass:
			labels[f.Key()] = FlakinessFlaky
		default:
			labels[f.Key()] = FlakinessUnknown
		}
	}
	return labels
}
