package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Output file names, in the run's artifact dir.
const (
	FileJSON    = "report.json"
	FileJUnit   = "report.junit.xml"
	FileHTML    = "report.html"
	FileSummary = "summary.txt"
	FileBugs    = "bugboard-drafts.json"
)

// BugDraft is a Bugboard task draft for one failure. It is written to a file
// only; filing it is a human's (or a later tool's) decision.
type BugDraft struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Area        string   `json:"area"`
	Subtasks    []int    `json:"related_tasks,omitempty"`
	Evidence    []string `json:"evidence"`
}

// BugDrafts drafts one task per failure.
func BugDrafts(r Report) []BugDraft {
	var out []BugDraft
	for _, f := range r.Failures {
		ev := []string{}
		for _, rec := range f.Evidence {
			ev = append(ev, fmt.Sprintf("%s %s → %d", rec.Kind, rec.Summary, rec.Status))
		}
		desc := fmt.Sprintf("e2e run %s at %s: %s/%s failed (%s).\n\nTest output:\n%s",
			r.RunID, r.Commit, f.Feature, f.Test, f.Flakiness, strings.TrimSpace(f.Output))
		out = append(out, BugDraft{
			Title:       fmt.Sprintf("[e2e] %s: %s fails", f.Feature, f.Test),
			Description: desc, Area: f.Area, Subtasks: f.Subtasks, Evidence: ev,
		})
	}
	return out
}

// WriteAll writes every output into dir; bug drafts only when withBugs.
func WriteAll(dir string, r Report, withBugs bool) error {
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode report.json: %w", err)
	}
	junit, err := JUnit(r)
	if err != nil {
		return err
	}
	html, err := HTML(r)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		FileJSON: append(js, '\n'), FileJUnit: junit, FileHTML: html, FileSummary: []byte(r.Notification + "\n"),
	}
	if withBugs {
		bugs, err := json.MarshalIndent(BugDrafts(r), "", "  ")
		if err != nil {
			return fmt.Errorf("failed to encode bug drafts: %w", err)
		}
		files[FileBugs] = append(bugs, '\n')
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}
	return nil
}
