package autoupdate

import "fmt"

// Step is one reversible part of a node's upgrade. Undo must put back what
// Do changed. A step that did not run is not undone.
type Step struct {
	Name string
	Do   func() error
	Undo func() error
	// Blames says a failure of Do is the release's fault: the new binaries
	// ran and did not come up. A step that fails before the release runs
	// (a stop, a copy) says nothing about the release.
	Blames bool
}

// Apply runs steps in order, then the health gate.
//
// A failure, including the gate, undoes the steps that ran, last first.
// releaseBad is set only when the gate or a Blames step failed: the caller
// marks the release bad then, and only then. This function does not write
// cluster state.
func Apply(steps []Step, gate func() error) (releaseBad bool, err error) {
	if gate == nil {
		return false, fmt.Errorf("a health gate is required")
	}
	done := make([]Step, 0, len(steps))
	for _, step := range steps {
		if step.Do == nil {
			return false, undo(done, fmt.Errorf("step %s has no action", step.Name))
		}
		if err := step.Do(); err != nil {
			return step.Blames, undo(done, fmt.Errorf("%s: %w", step.Name, err))
		}
		done = append(done, step)
	}
	if err := gate(); err != nil {
		return true, undo(done, fmt.Errorf("health gate: %w", err))
	}
	return false, nil
}

func undo(done []Step, cause error) error {
	var first error
	for i := len(done) - 1; i >= 0; i-- {
		if done[i].Undo == nil {
			continue
		}
		if err := done[i].Undo(); err != nil && first == nil {
			first = fmt.Errorf("undo %s: %w", done[i].Name, err)
		}
	}
	if first != nil {
		return fmt.Errorf("%w; %v", cause, first)
	}
	return cause
}
