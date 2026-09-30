package stages

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// StateFileName is the runner's timeline and resume state, in the artifact dir.
const StateFileName = "stages-state.json"

// PackageRun is one `go test` of one feature package.
type PackageRun struct {
	Feature string `json:"feature"`
	// Output is the go test -json file, relative to the artifact dir.
	Output string `json:"output"`
	// Evidence is the directory the package recorded its evidence in,
	// relative to the artifact dir.
	Evidence string    `json:"evidence,omitempty"`
	Exit     int       `json:"exit"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	// Error is set when the package could not be run at all.
	Error string `json:"error,omitempty"`
}

// StageRun is one executed stage.
type StageRun struct {
	Stage    Stage        `json:"stage"`
	Start    time.Time    `json:"start"`
	End      time.Time    `json:"end"`
	Packages []PackageRun `json:"packages"`
	// Completed is true once every package of the stage ran. A stage whose
	// tests failed is still completed: resuming never re-runs a failed test.
	Completed bool `json:"completed"`
}

// Failed reports whether any package of the stage failed or did not run.
func (s StageRun) Failed() bool {
	for _, p := range s.Packages {
		if p.Exit != 0 || p.Error != "" {
			return true
		}
	}
	return false
}

// Timeline is every stage run so far, in order.
type Timeline struct {
	Stages []StageRun `json:"stages"`
}

// Completed reports whether stage id already ran to completion.
func (t *Timeline) Completed(id int) bool {
	for _, s := range t.Stages {
		if s.Stage.ID == id && s.Completed {
			return true
		}
	}
	return false
}

// put replaces the entry for run's stage or appends it.
func (t *Timeline) put(run StageRun) {
	for i := range t.Stages {
		if t.Stages[i].Stage.ID == run.Stage.ID {
			t.Stages[i] = run
			return
		}
	}
	t.Stages = append(t.Stages, run)
}

// LoadTimeline reads the state file; a missing file is an empty timeline.
func LoadTimeline(path string) (*Timeline, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Timeline{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read stage state %s: %w", path, err)
	}
	var t Timeline
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("failed to parse stage state %s: %w", path, err)
	}
	return &t, nil
}

// Save writes the state file atomically.
func (t *Timeline) Save(path string) error {
	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode stage state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("failed to write stage state %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to replace stage state %s: %w", path, err)
	}
	return nil
}
