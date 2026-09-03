package inspector

import (
	"time"
)

// Severity levels for check results.
type Severity int

const (
	Low Severity = iota
	Medium
	High
	Critical
)

func (s Severity) String() string {
	switch s {
	case Low:
		return "LOW"
	case Medium:
		return "MEDIUM"
	case High:
		return "HIGH"
	case Critical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// Status represents the outcome of a check.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusSkip Status = "skip"
)

// CheckResult holds the outcome of a single health check.
type CheckResult struct {
	ID        string   `json:"id"`        // e.g. "rqlite.leader_exists"
	Name      string   `json:"name"`      // "Cluster has exactly one leader"
	Subsystem string   `json:"subsystem"` // "rqlite"
	Severity  Severity `json:"severity"`
	Status    Status   `json:"status"`
	Message   string   `json:"message"`        // human-readable detail
	Node      string   `json:"node,omitempty"` // which node (empty for cluster-wide)
}

// Results holds all check outcomes.
type Results struct {
	Checks   []CheckResult `json:"checks"`
	Duration time.Duration `json:"duration"`
}

// Summary returns counts by status.
func (r *Results) Summary() (passed, failed, warned, skipped int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			passed++
		case StatusFail:
			failed++
		case StatusWarn:
			warned++
		case StatusSkip:
			skipped++
		}
	}
	return
}

// Failures returns only failed checks.
func (r *Results) Failures() []CheckResult {
	var out []CheckResult
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			out = append(out, c)
		}
	}
	return out
}

// FailuresAndWarnings returns failed and warning checks.
func (r *Results) FailuresAndWarnings() []CheckResult {
	var out []CheckResult
	for _, c := range r.Checks {
		if c.Status == StatusFail || c.Status == StatusWarn {
			out = append(out, c)
		}
	}
	return out
}

// CheckFunc is the signature for a subsystem check function.
type CheckFunc func(data *ClusterData) []CheckResult

// SubsystemCheckers maps subsystem names to their check functions.
// Populated by checks/ package init or by explicit registration.
var SubsystemCheckers = map[string]CheckFunc{}

// RegisterChecker registers a check function for a subsystem.
func RegisterChecker(subsystem string, fn CheckFunc) {
	SubsystemCheckers[subsystem] = fn
}

// RunChecks executes checks for the requested subsystems against collected data.
func RunChecks(data *ClusterData, subsystems []string) *Results {
	start := time.Now()
	results := &Results{}

	shouldCheck := func(name string) bool {
		if len(subsystems) == 0 {
			return true
		}
		for _, s := range subsystems {
			if s == name || s == "all" {
				return true
			}
			// Alias: "wg" matches "wireguard"
			if s == "wg" && name == "wireguard" {
				return true
			}
		}
		return false
	}

	for name, fn := range SubsystemCheckers {
		if shouldCheck(name) {
			checks := fn(data)
			results.Checks = append(results.Checks, checks...)
		}
	}

	results.Duration = time.Since(start)
	return results
}

// Pass creates a passing check result.
func Pass(id, name, subsystem, node, msg string, sev Severity) CheckResult {
	return CheckResult{
		ID: id, Name: name, Subsystem: subsystem,
		Severity: sev, Status: StatusPass, Message: msg, Node: node,
	}
}

// Fail creates a failing check result.
func Fail(id, name, subsystem, node, msg string, sev Severity) CheckResult {
	return CheckResult{
		ID: id, Name: name, Subsystem: subsystem,
		Severity: sev, Status: StatusFail, Message: msg, Node: node,
	}
}

// Warn creates a warning check result.
func Warn(id, name, subsystem, node, msg string, sev Severity) CheckResult {
	return CheckResult{
		ID: id, Name: name, Subsystem: subsystem,
		Severity: sev, Status: StatusWarn, Message: msg, Node: node,
	}
}

// Skip creates a skipped check result.
func Skip(id, name, subsystem, node, msg string, sev Severity) CheckResult {
	return CheckResult{
		ID: id, Name: name, Subsystem: subsystem,
		Severity: sev, Status: StatusSkip, Message: msg, Node: node,
	}
}
