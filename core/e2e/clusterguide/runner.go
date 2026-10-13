package clusterguide

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Executor runs one command and returns its combined output. The real one
// runs the orama binary; a test supplies a fake.
type Executor interface {
	Run(ctx context.Context, argv []string) (string, error)
}

// Kind says how a step is carried out.
type Kind int

const (
	// Run executes the guide's command, with the fixture's values in it.
	Run Kind = iota
	// Provided is a command the fixture stands in for: the guide tells a
	// reader to type it, the harness cannot (a build, a vault entry). It is
	// matched against the guide and not executed.
	Provided
)

// Step is one command the guide must contain, in order.
type Step struct {
	Name    string
	Section string
	// Words are the leading non-flag words of the command ("orama", "node",
	// "setup"). The guide's command must start with them.
	Words []string
	// Need are flags the guide's command must carry; Deny flags it must not.
	Need []string
	Deny []string
	Kind Kind
	// Skip returns a reason to leave the step out for this fixture, "" to run
	// it. A skipped step must still be in the guide.
	Skip func(fx *Fixture) string
	// Extra returns arguments appended to the guide's command: what the guide
	// leaves to the reader (a host-key fingerprint) or the harness needs to
	// read the result (--json).
	Extra func(ctx context.Context, fx *Fixture, argv []string) ([]string, error)
	// Check inspects the command's output after it succeeded.
	Check func(fx *Fixture, out string) error
	// After runs once the step is done or skipped, before the next step.
	After func(ctx context.Context, fx *Fixture) error
}

// Result is what happened to one step.
type Result struct {
	Step    string
	Argv    []string
	Skipped string
	Output  string
}

// Match reports why a guide command is not this step, or nil.
func (s Step) Match(c Command) error {
	if c.Section != s.Section {
		return fmt.Errorf("it is under %q, the step is in %q", c.Section, s.Section)
	}
	words := leadingWords(c.Argv)
	if !slices.Equal(words[:min(len(words), len(s.Words))], s.Words) {
		return fmt.Errorf("it starts %q, the step is %q", strings.Join(words, " "), strings.Join(s.Words, " "))
	}
	flags := Flags(c.Argv)
	for _, need := range s.Need {
		if !slices.Contains(flags, need) {
			return fmt.Errorf("it lacks %s", need)
		}
	}
	for _, deny := range s.Deny {
		if slices.Contains(flags, deny) {
			return fmt.Errorf("it has %s, which this step must not", deny)
		}
	}
	return nil
}

// leadingWords are the arguments before the first flag or path-like value.
func leadingWords(argv []string) []string {
	var out []string
	for _, a := range argv {
		if strings.HasPrefix(a, "-") || strings.ContainsAny(a, "/.@:") {
			break
		}
		out = append(out, a)
	}
	return out
}

// Runner walks the guide against the plan.
type Runner struct {
	Exec Executor
	Fx   *Fixture
	Logf func(format string, args ...any)
}

// Verify checks the guide and the plan agree, executing nothing: the same
// commands, in the same order, each with the flags its step needs, and no
// example value left unbound. A run calls it first.
func (r Runner) Verify(cmds []Command, plan []Step) error {
	for i, s := range plan {
		if i >= len(cmds) {
			return fmt.Errorf("the guide ends before step %d (%s): it has %d commands, the plan %d", i+1, s.Name, len(cmds), len(plan))
		}
		if err := s.Match(cmds[i]); err != nil {
			return fmt.Errorf("guide command %d %q is not step %q: %w", i+1, cmds[i].Line, s.Name, err)
		}
		if _, err := r.Fx.Bind(cmds[i]); err != nil {
			return fmt.Errorf("guide command %d %q: %w", i+1, cmds[i].Line, err)
		}
	}
	if len(cmds) > len(plan) {
		return fmt.Errorf("the guide has a command the plan does not run: %q (command %d, after %q); add a step for it", cmds[len(plan)].Line, len(plan)+1, plan[len(plan)-1].Name)
	}
	return nil
}

// Execute verifies, then runs each step in order, stopping at the first
// failure. Results include every step reached.
func (r Runner) Execute(ctx context.Context, cmds []Command, plan []Step) ([]Result, error) {
	if err := r.Verify(cmds, plan); err != nil {
		return nil, err
	}
	var results []Result
	for i, s := range plan {
		res, err := r.step(ctx, s, cmds[i])
		results = append(results, res)
		if err != nil {
			return results, fmt.Errorf("step %d (%s): %w", i+1, s.Name, err)
		}
		if s.After != nil {
			if err := s.After(ctx, r.Fx); err != nil {
				return results, fmt.Errorf("after step %d (%s): %w", i+1, s.Name, err)
			}
		}
	}
	return results, nil
}

func (r Runner) step(ctx context.Context, s Step, c Command) (Result, error) {
	res := Result{Step: s.Name}
	argv, err := r.Fx.Bind(c)
	if err != nil {
		return res, err
	}
	res.Argv = argv
	switch {
	case s.Kind == Provided:
		res.Skipped = "provided by the fixture"
		r.logf("skip   %s: %s", s.Name, res.Skipped)
		return res, nil
	case s.Skip != nil && s.Skip(r.Fx) != "":
		res.Skipped = s.Skip(r.Fx)
		r.logf("skip   %s: %s", s.Name, res.Skipped)
		return res, nil
	}
	if s.Extra != nil {
		extra, err := s.Extra(ctx, r.Fx, argv)
		if err != nil {
			return res, err
		}
		argv = append(argv, extra...)
		res.Argv = argv
	}
	r.logf("run    %s: %s", s.Name, strings.Join(argv, " "))
	out, err := r.Exec.Run(ctx, argv)
	res.Output = out
	if err != nil {
		return res, fmt.Errorf("%s: %w\n%s", strings.Join(argv, " "), err, out)
	}
	if s.Check != nil {
		if err := s.Check(r.Fx, out); err != nil {
			return res, fmt.Errorf("%s ran, but: %w\n%s", strings.Join(argv, " "), err, out)
		}
	}
	return res, nil
}

func (r Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// exampleValue matches a value the guide uses as an example: an RFC 5737
// address or an example domain. Bind refuses to run one.
var exampleValue = regexp.MustCompile(`203\.0\.113\.\d+|[a-z0-9.-]*example\.(com|org|net)`)
