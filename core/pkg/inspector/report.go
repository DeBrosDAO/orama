package inspector

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PrintTable writes a human-readable table of check results.
func PrintTable(results *Results, w io.Writer) {
	if len(results.Checks) == 0 {
		fmt.Fprintf(w, "No checks executed.\n")
		return
	}

	// Sort: failures first, then warnings, then passes, then skips.
	// Within each group, sort by severity (critical first).
	sorted := make([]CheckResult, len(results.Checks))
	copy(sorted, results.Checks)
	sort.Slice(sorted, func(i, j int) bool {
		oi, oj := statusOrder(sorted[i].Status), statusOrder(sorted[j].Status)
		if oi != oj {
			return oi < oj
		}
		// Higher severity first
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		return sorted[i].ID < sorted[j].ID
	})

	// Group by subsystem
	groups := map[string][]CheckResult{}
	var subsystems []string
	for _, c := range sorted {
		if _, exists := groups[c.Subsystem]; !exists {
			subsystems = append(subsystems, c.Subsystem)
		}
		groups[c.Subsystem] = append(groups[c.Subsystem], c)
	}

	for _, sub := range subsystems {
		checks := groups[sub]
		fmt.Fprintf(w, "\n%s %s\n", severityIcon(Critical), strings.ToUpper(sub))
		fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))

		for _, c := range checks {
			icon := statusIcon(c.Status)
			sev := fmt.Sprintf("[%s]", c.Severity)
			nodePart := ""
			if c.Node != "" {
				nodePart = fmt.Sprintf(" (%s)", c.Node)
			}
			fmt.Fprintf(w, "  %s %-8s %s%s\n", icon, sev, c.Name, nodePart)
			if c.Message != "" {
				fmt.Fprintf(w, "    %s\n", c.Message)
			}
		}
	}

	passed, failed, warned, skipped := results.Summary()
	fmt.Fprintf(w, "\n%s\n", strings.Repeat("=", 70))
	fmt.Fprintf(w, "Summary: %d passed, %d failed, %d warnings, %d skipped (%.1fs)\n",
		passed, failed, warned, skipped, results.Duration.Seconds())
}

// PrintJSON writes check results as JSON.
func PrintJSON(results *Results, w io.Writer) {
	passed, failed, warned, skipped := results.Summary()
	output := struct {
		Summary struct {
			Passed  int     `json:"passed"`
			Failed  int     `json:"failed"`
			Warned  int     `json:"warned"`
			Skipped int     `json:"skipped"`
			Total   int     `json:"total"`
			Seconds float64 `json:"duration_seconds"`
		} `json:"summary"`
		Checks []CheckResult `json:"checks"`
	}{
		Checks: results.Checks,
	}
	output.Summary.Passed = passed
	output.Summary.Failed = failed
	output.Summary.Warned = warned
	output.Summary.Skipped = skipped
	output.Summary.Total = len(results.Checks)
	output.Summary.Seconds = results.Duration.Seconds()

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(output)
}

// SummaryLine returns a one-line summary string.
func SummaryLine(results *Results) string {
	passed, failed, warned, skipped := results.Summary()
	return fmt.Sprintf("%d passed, %d failed, %d warnings, %d skipped",
		passed, failed, warned, skipped)
}

func statusOrder(s Status) int {
	switch s {
	case StatusFail:
		return 0
	case StatusWarn:
		return 1
	case StatusPass:
		return 2
	case StatusSkip:
		return 3
	default:
		return 4
	}
}

func statusIcon(s Status) string {
	switch s {
	case StatusPass:
		return "OK"
	case StatusFail:
		return "FAIL"
	case StatusWarn:
		return "WARN"
	case StatusSkip:
		return "SKIP"
	default:
		return "??"
	}
}

func severityIcon(_ Severity) string {
	return "##"
}
