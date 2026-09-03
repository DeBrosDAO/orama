package inspector

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FailureGroup groups identical check failures/warnings across nodes.
type FailureGroup struct {
	ID        string
	Name      string   // from first check in group
	Status    Status
	Severity  Severity
	Subsystem string
	Nodes     []string // affected node names (deduplicated)
	Messages  []string // unique messages (capped at 5)
	Count     int      // total raw occurrence count (before dedup)
}

// GroupFailures collapses CheckResults into unique failure groups keyed by (ID, Status).
// Only failures and warnings are grouped; passes and skips are ignored.
func GroupFailures(results *Results) []FailureGroup {
	type groupKey struct {
		ID     string
		Status Status
	}

	seen := map[groupKey]*FailureGroup{}
	nodesSeen := map[groupKey]map[string]bool{}
	var order []groupKey

	for _, c := range results.Checks {
		if c.Status != StatusFail && c.Status != StatusWarn {
			continue
		}
		k := groupKey{ID: c.ID, Status: c.Status}
		g, exists := seen[k]
		if !exists {
			g = &FailureGroup{
				ID:        c.ID,
				Name:      c.Name,
				Status:    c.Status,
				Severity:  c.Severity,
				Subsystem: c.Subsystem,
			}
			seen[k] = g
			nodesSeen[k] = map[string]bool{}
			order = append(order, k)
		}
		g.Count++
		node := c.Node
		if node == "" {
			node = "cluster-wide"
		}
		// Deduplicate nodes (a node may appear for multiple targets)
		if !nodesSeen[k][node] {
			nodesSeen[k][node] = true
			g.Nodes = append(g.Nodes, node)
		}
		// Track unique messages (cap at 5 to avoid bloat)
		if len(g.Messages) < 5 {
			found := false
			for _, m := range g.Messages {
				if m == c.Message {
					found = true
					break
				}
			}
			if !found {
				g.Messages = append(g.Messages, c.Message)
			}
		}
	}

	// Sort: failures before warnings, then by severity (high first), then by ID
	groups := make([]FailureGroup, 0, len(order))
	for _, k := range order {
		groups = append(groups, *seen[k])
	}
	sort.Slice(groups, func(i, j int) bool {
		oi, oj := statusOrder(groups[i].Status), statusOrder(groups[j].Status)
		if oi != oj {
			return oi < oj
		}
		if groups[i].Severity != groups[j].Severity {
			return groups[i].Severity > groups[j].Severity
		}
		return groups[i].ID < groups[j].ID
	})

	return groups
}

// WriteResults saves inspection results as markdown files to a timestamped directory.
// Returns the output directory path.
func WriteResults(baseDir, env string, results *Results, data *ClusterData, analysis *AnalysisResult) (string, error) {
	ts := time.Now().Format("2006-01-02_150405")
	dir := filepath.Join(baseDir, env, ts)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	groups := GroupFailures(results)

	// Build analysis lookup: groupID -> analysis text
	analysisMap := map[string]string{}
	if analysis != nil {
		for _, sa := range analysis.Analyses {
			key := sa.GroupID
			if key == "" {
				key = sa.Subsystem
			}
			if sa.Error == nil {
				analysisMap[key] = sa.Analysis
			}
		}
	}

	// Write summary.md
	if err := writeSummary(dir, env, ts, results, data, groups, analysisMap); err != nil {
		return "", fmt.Errorf("write summary: %w", err)
	}

	// Group checks by subsystem for per-subsystem files
	checksBySubsystem := map[string][]CheckResult{}
	for _, c := range results.Checks {
		checksBySubsystem[c.Subsystem] = append(checksBySubsystem[c.Subsystem], c)
	}

	groupsBySubsystem := map[string][]FailureGroup{}
	for _, g := range groups {
		groupsBySubsystem[g.Subsystem] = append(groupsBySubsystem[g.Subsystem], g)
	}

	// Write per-subsystem files
	for sub, checks := range checksBySubsystem {
		subGroups := groupsBySubsystem[sub]
		if err := writeSubsystem(dir, sub, ts, checks, subGroups, analysisMap); err != nil {
			return "", fmt.Errorf("write %s: %w", sub, err)
		}
	}

	return dir, nil
}

func writeSummary(dir, env, ts string, results *Results, data *ClusterData, groups []FailureGroup, analysisMap map[string]string) error {
	var b strings.Builder
	passed, failed, warned, skipped := results.Summary()

	b.WriteString(fmt.Sprintf("# %s Inspection Report\n\n", strings.ToUpper(env)))
	b.WriteString(fmt.Sprintf("**Date:** %s  \n", ts))
	b.WriteString(fmt.Sprintf("**Nodes:** %d  \n", len(data.Nodes)))
	b.WriteString(fmt.Sprintf("**Total:** %d passed, %d failed, %d warnings, %d skipped  \n\n", passed, failed, warned, skipped))

	// Per-subsystem table
	subStats := map[string][4]int{} // [pass, fail, warn, skip]
	var subsystems []string
	for _, c := range results.Checks {
		if _, exists := subStats[c.Subsystem]; !exists {
			subsystems = append(subsystems, c.Subsystem)
		}
		s := subStats[c.Subsystem]
		switch c.Status {
		case StatusPass:
			s[0]++
		case StatusFail:
			s[1]++
		case StatusWarn:
			s[2]++
		case StatusSkip:
			s[3]++
		}
		subStats[c.Subsystem] = s
	}
	sort.Strings(subsystems)

	// Count issue groups per subsystem
	issueCountBySub := map[string]int{}
	for _, g := range groups {
		issueCountBySub[g.Subsystem]++
	}

	b.WriteString("## Subsystems\n\n")
	b.WriteString("| Subsystem | Pass | Fail | Warn | Skip | Issues |\n")
	b.WriteString("|-----------|------|------|------|------|--------|\n")
	for _, sub := range subsystems {
		s := subStats[sub]
		issues := issueCountBySub[sub]
		link := fmt.Sprintf("[%s](%s.md)", sub, sub)
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d |\n", link, s[0], s[1], s[2], s[3], issues))
	}
	b.WriteString("\n")

	// Critical issues section
	critical := filterGroupsBySeverity(groups, High)
	if len(critical) > 0 {
		b.WriteString("## Critical Issues\n\n")
		for i, g := range critical {
			icon := "FAIL"
			if g.Status == StatusWarn {
				icon = "WARN"
			}
			nodeInfo := fmt.Sprintf("%d nodes", len(g.Nodes))
			if g.Count > len(g.Nodes) {
				nodeInfo = fmt.Sprintf("%d nodes (%d occurrences)", len(g.Nodes), g.Count)
			}
			b.WriteString(fmt.Sprintf("%d. **[%s]** %s — %s  \n", i+1, icon, g.Name, nodeInfo))
			b.WriteString(fmt.Sprintf("   *%s* → [details](%s.md#%s)  \n",
				g.Messages[0], g.Subsystem, anchorID(g.Name)))
		}
		b.WriteString("\n")
	}

	// Collection errors
	var errs []string
	for _, nd := range data.Nodes {
		for _, e := range nd.Errors {
			errs = append(errs, fmt.Sprintf("- **%s**: %s", nd.Node.Name(), e))
		}
	}
	if len(errs) > 0 {
		b.WriteString("## Collection Errors\n\n")
		for _, e := range errs {
			b.WriteString(e + "\n")
		}
		b.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(b.String()), 0o600)
}

func writeSubsystem(dir, subsystem, ts string, checks []CheckResult, groups []FailureGroup, analysisMap map[string]string) error {
	var b strings.Builder

	// Count
	var passed, failed, warned, skipped int
	for _, c := range checks {
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

	b.WriteString(fmt.Sprintf("# %s\n\n", strings.ToUpper(subsystem)))
	b.WriteString(fmt.Sprintf("**Date:** %s  \n", ts))
	b.WriteString(fmt.Sprintf("**Checks:** %d passed, %d failed, %d warnings, %d skipped  \n\n", passed, failed, warned, skipped))

	// Issues section
	if len(groups) > 0 {
		b.WriteString("## Issues\n\n")
		for i, g := range groups {
			icon := "FAIL"
			if g.Status == StatusWarn {
				icon = "WARN"
			}
			b.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, g.Name))
			nodeInfo := fmt.Sprintf("%d nodes", len(g.Nodes))
			if g.Count > len(g.Nodes) {
				nodeInfo = fmt.Sprintf("%d nodes (%d occurrences)", len(g.Nodes), g.Count)
			}
			b.WriteString(fmt.Sprintf("**Status:** %s | **Severity:** %s | **Affected:** %s  \n\n", icon, g.Severity, nodeInfo))

			// Affected nodes
			b.WriteString("**Affected nodes:**\n")
			for _, n := range g.Nodes {
				b.WriteString(fmt.Sprintf("- `%s`\n", n))
			}
			b.WriteString("\n")

			// Messages
			if len(g.Messages) == 1 {
				b.WriteString(fmt.Sprintf("**Detail:** %s\n\n", g.Messages[0]))
			} else {
				b.WriteString("**Details:**\n")
				for _, m := range g.Messages {
					b.WriteString(fmt.Sprintf("- %s\n", m))
				}
				b.WriteString("\n")
			}

			// AI analysis (if available)
			if ai, ok := analysisMap[g.ID]; ok {
				b.WriteString(ai)
				b.WriteString("\n\n")
			}

			b.WriteString("---\n\n")
		}
	}

	// All checks table
	b.WriteString("## All Checks\n\n")
	b.WriteString("| Status | Severity | Check | Node | Detail |\n")
	b.WriteString("|--------|----------|-------|------|--------|\n")

	// Sort: failures first
	sorted := make([]CheckResult, len(checks))
	copy(sorted, checks)
	sort.Slice(sorted, func(i, j int) bool {
		oi, oj := statusOrder(sorted[i].Status), statusOrder(sorted[j].Status)
		if oi != oj {
			return oi < oj
		}
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		return sorted[i].ID < sorted[j].ID
	})

	for _, c := range sorted {
		node := c.Node
		if node == "" {
			node = "cluster-wide"
		}
		msg := strings.ReplaceAll(c.Message, "|", "\\|")
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
			statusIcon(c.Status), c.Severity, c.Name, node, msg))
	}

	return os.WriteFile(filepath.Join(dir, subsystem+".md"), []byte(b.String()), 0o600)
}

func filterGroupsBySeverity(groups []FailureGroup, minSeverity Severity) []FailureGroup {
	var out []FailureGroup
	for _, g := range groups {
		if g.Severity >= minSeverity {
			out = append(out, g)
		}
	}
	return out
}

func anchorID(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, s)
	return s
}
