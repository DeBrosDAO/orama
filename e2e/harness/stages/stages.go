// Package stages orders a run: e2e/stages/stages.yaml names the stages, each
// feature's manifest says which stage it belongs to, and the runner executes
// `go test -json` for every feature package, stage by stage.
package stages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// FilePath is stages.yaml relative to the e2e module root.
const FilePath = "stages/stages.yaml"

// Stage is one ordered step of a run.
type Stage struct {
	ID      int      `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Timeout Duration `yaml:"timeout" json:"timeout"`
}

// Duration is a time.Duration written as "45m" in YAML.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	parsed, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", n.Line, n.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON writes the duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON reads the duration string MarshalJSON writes.
func (d *Duration) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

type stagesFile struct {
	Stages []Stage `yaml:"stages"`
}

// Load reads and validates stages.yaml: ids exactly 1..manifest.MaxStage in
// order, unique non-empty names, positive timeouts.
func Load(path string) ([]Stage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read stages %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f stagesFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("failed to parse stages %s: %w", path, err)
	}
	if err := validate(f.Stages); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Stages, nil
}

func validate(stages []Stage) error {
	if len(stages) != manifest.MaxStage {
		return fmt.Errorf("want %d stages, found %d", manifest.MaxStage, len(stages))
	}
	names := map[string]bool{}
	for i, s := range stages {
		if s.ID != i+1 {
			return fmt.Errorf("stage #%d has id %d; ids must run 1..%d in order", i+1, s.ID, manifest.MaxStage)
		}
		if s.Name == "" || names[s.Name] {
			return fmt.Errorf("stage %d has an empty or duplicate name %q", s.ID, s.Name)
		}
		names[s.Name] = true
		if s.Timeout <= 0 {
			return fmt.Errorf("stage %d (%s) needs a positive timeout", s.ID, s.Name)
		}
	}
	return nil
}

// Step is what one stage runs: independent packages in parallel, then each
// destructive package alone, so a destructive failure cannot disturb (or hide)
// the stage's other results.
type Step struct {
	Stage       Stage    `json:"stage"`
	Parallel    []string `json:"parallel"`
	Destructive []string `json:"destructive"`
}

// Features returns every feature of the step, parallel ones first.
func (s Step) Features() []string {
	return append(append([]string{}, s.Parallel...), s.Destructive...)
}

// Plan assigns manifests to stages. Stages with no features are kept so the
// timeline shows them as empty rather than silently absent.
func Plan(stages []Stage, manifests []manifest.Manifest) ([]Step, error) {
	byStage := map[int]*Step{}
	steps := make([]Step, len(stages))
	for i, s := range stages {
		steps[i] = Step{Stage: s}
		byStage[s.ID] = &steps[i]
	}
	for _, m := range manifests {
		step := byStage[m.Stage]
		if step == nil {
			return nil, fmt.Errorf("feature %s names stage %d, which stages.yaml does not define", m.ID, m.Stage)
		}
		if m.Destructive {
			step.Destructive = append(step.Destructive, m.ID)
		} else {
			step.Parallel = append(step.Parallel, m.ID)
		}
	}
	for i := range steps {
		sort.Strings(steps[i].Parallel)
		sort.Strings(steps[i].Destructive)
	}
	return steps, nil
}

// WorstCase is the longest steps can run: in each stage the parallel
// packages together within its timeout, then each destructive package alone
// within it. The provisioner's e2e-ttl label is derived from it, so the
// orphan sweep never takes a run that is still inside its plan.
func WorstCase(steps []Step) time.Duration {
	var d time.Duration
	for _, s := range steps {
		n := len(s.Destructive)
		if len(s.Parallel) > 0 {
			n++
		}
		d += time.Duration(n) * time.Duration(s.Stage.Timeout)
	}
	return d
}
