// Package gotest reads `go test -json` output (test2json events) into per-test
// results: what the stage runner records and the report renders.
package gotest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// test2json actions the results depend on.
const (
	ActionPass   = "pass"
	ActionFail   = "fail"
	ActionSkip   = "skip"
	ActionOutput = "output"
	ActionRun    = "run"
	// ActionBuildOutput carries compiler output when a package does not build.
	ActionBuildOutput = "build-output"
)

// MaxOutputBytes bounds the output kept per test.
const MaxOutputBytes = 32 * 1024

// Event is one test2json line.
type Event struct {
	Action     string  `json:"Action"`
	Package    string  `json:"Package"`
	Test       string  `json:"Test"`
	Output     string  `json:"Output"`
	Elapsed    float64 `json:"Elapsed"`
	ImportPath string  `json:"ImportPath"`
}

// Result is one test's outcome, or a package's (Test empty).
type Result struct {
	Package string  `json:"package"`
	Test    string  `json:"test,omitempty"`
	Action  string  `json:"action"`
	Elapsed float64 `json:"elapsed"`
	Output  string  `json:"output,omitempty"`
}

// Key identifies a test across runs.
func (r Result) Key() string { return r.Package + " " + r.Test }

// Parse reads a test2json stream. A test that started and never finished (the
// binary panicked or timed out) is a failure, not a missing row. Non-JSON lines
// (the go command's own stderr, if merged) are attached to the package.
func Parse(r io.Reader) ([]Result, error) {
	acc := newAccumulator()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Action == "" {
			acc.stray(string(line) + "\n")
			continue
		}
		acc.add(ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read go test output: %w", err)
	}
	return acc.results(), nil
}

type accumulator struct {
	rows    map[string]*Result
	started map[string]bool
	lastPkg string
}

func newAccumulator() *accumulator {
	return &accumulator{rows: map[string]*Result{}, started: map[string]bool{}}
}

func (a *accumulator) row(pkg, test string) *Result {
	k := pkg + " " + test
	if a.rows[k] == nil {
		a.rows[k] = &Result{Package: pkg, Test: test}
	}
	return a.rows[k]
}

func (a *accumulator) add(ev Event) {
	pkg := ev.Package
	if pkg == "" {
		pkg = ev.ImportPath
	}
	a.lastPkg = pkg
	r := a.row(pkg, ev.Test)
	switch ev.Action {
	case ActionRun:
		a.started[r.Key()] = true
	case ActionOutput, ActionBuildOutput:
		r.Output = appendBounded(r.Output, ev.Output)
	case ActionPass, ActionFail, ActionSkip:
		r.Action, r.Elapsed = ev.Action, ev.Elapsed
	}
}

func (a *accumulator) stray(line string) {
	r := a.row(a.lastPkg, "")
	r.Output = appendBounded(r.Output, line)
}

func (a *accumulator) results() []Result {
	out := make([]Result, 0, len(a.rows))
	for _, r := range a.rows {
		if r.Action == "" {
			if !a.started[r.Key()] && r.Output == "" {
				continue
			}
			// Started or produced output, never finished: it failed.
			r.Action = ActionFail
			r.Output = appendBounded(r.Output, "\n[e2e] no pass/fail/skip event: the test binary crashed, timed out or did not build\n")
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Test < out[j].Test
	})
	return out
}

func appendBounded(s, more string) string {
	if len(s) >= MaxOutputBytes {
		return s
	}
	s += more
	if len(s) > MaxOutputBytes {
		s = s[:MaxOutputBytes]
		s = strings.ToValidUTF8(s, "") + "\n…[truncated]"
	}
	return s
}

// Failed returns the test-level failures (package rows excluded unless the
// package failed with no failing test, e.g. a build error or TestMain exit).
func Failed(results []Result) []Result {
	var out []Result
	failedTests := map[string]bool{}
	for _, r := range results {
		if r.Test != "" && r.Action == ActionFail {
			out = append(out, r)
			failedTests[r.Package] = true
		}
	}
	for _, r := range results {
		if r.Test == "" && r.Action == ActionFail && !failedTests[r.Package] {
			out = append(out, r)
		}
	}
	return out
}
