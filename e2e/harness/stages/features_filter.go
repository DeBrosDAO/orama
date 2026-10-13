package stages

import (
	"fmt"
	"slices"
)

// only is step reduced to the named packages.
func (s Step) only(features []string) Step {
	keep := func(list []string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(f string) bool { return !slices.Contains(features, f) })
	}
	s.Parallel, s.Destructive = keep(s.Parallel), keep(s.Destructive)
	return s
}

func (s Step) empty() bool { return len(s.Parallel) == 0 && len(s.Destructive) == 0 }

// merge records a run of some of a stage's packages: each package it ran
// replaces that package's earlier result, and the others stay. A stage never
// run before is added as it is.
func (t *Timeline) merge(run StageRun) {
	for i := range t.Stages {
		if t.Stages[i].Stage.ID != run.Stage.ID {
			continue
		}
		prev := &t.Stages[i]
		for _, p := range run.Packages {
			at := slices.IndexFunc(prev.Packages, func(q PackageRun) bool { return q.Feature == p.Feature })
			if at < 0 {
				prev.Packages = append(prev.Packages, p)
			} else {
				prev.Packages[at] = p
			}
		}
		prev.End = run.End
		prev.Completed = prev.Completed && run.Completed
		return
	}
	t.Stages = append(t.Stages, run)
}

// CheckFeatures refuses a name in features that no step runs.
func CheckFeatures(steps []Step, features []string) error {
	var unknown []string
	for _, f := range features {
		if !slices.ContainsFunc(steps, func(s Step) bool {
			return slices.Contains(s.Parallel, f) || slices.Contains(s.Destructive, f)
		}) {
			unknown = append(unknown, f)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("no stage runs %v", unknown)
	}
	return nil
}
