// Command fuzzrun runs every Go native fuzz target of a module for a fixed time each.
//
// `go test -fuzz` runs one target of one package, so a module with fifty targets needs a loop. This
// finds the targets (a func FuzzX(f *testing.F) in a _test.go file whose build constraint holds for
// the given tags), runs a few at a time, and prints one line per target: the package, the target,
// the executions, the time and the verdict. A target that fails leaves the input Go wrote to the
// package's testdata/fuzz/<Target>/ directory; the file is copied under -artifacts (CI uploads that
// directory) and the run exits non-zero once all targets have finished. The input stays in the
// package, where the next plain `go test` runs it as a regression case until the bug is fixed.
//
// make -C chain fuzz-short and fuzz-long are the callers; website/src/docs/contributor/testing.mdx
// describes the budgets.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type target struct {
	rel  string // package directory relative to the module root, slash separated
	name string
}

type result struct {
	target
	execs    string
	elapsed  time.Duration
	ok       bool
	output   string
	crashers []string
}

func main() {
	module := flag.String("module", ".", "the module directory")
	tags := flag.String("tags", "", "build tags, space separated")
	fuzztime := flag.String("fuzztime", "10s", "time per target (go test -fuzztime)")
	jobs := flag.Int("jobs", 2, "targets run at once")
	parallel := flag.Int("parallel", 2, "fuzzing workers per target (go test -parallel)")
	only := flag.String("only", "", "run only targets whose package/name matches this regular expression")
	artifacts := flag.String("artifacts", "", "directory the inputs of failing targets are copied to")
	list := flag.Bool("list", false, "print the targets and stop")
	flag.Parse()

	targets, err := find(*module, strings.Fields(*tags))
	if err != nil {
		fatal(err)
	}
	if *only != "" {
		re, err := regexp.Compile(*only)
		if err != nil {
			fatal(err)
		}
		kept := targets[:0]
		for _, t := range targets {
			if re.MatchString(t.rel + "/" + t.name) {
				kept = append(kept, t)
			}
		}
		targets = kept
	}
	if len(targets) == 0 {
		fatal(fmt.Errorf("no fuzz target found in %s", *module))
	}
	if *list {
		for _, t := range targets {
			fmt.Printf("%s %s\n", t.rel, t.name)
		}
		return
	}

	started := time.Now()
	results := runAll(*module, *tags, *fuzztime, *jobs, *parallel, targets)
	failed := report(results, time.Since(started))
	if failed > 0 {
		if *artifacts != "" {
			if err := keep(*module, *artifacts, results); err != nil {
				fmt.Fprintln(os.Stderr, "fuzzrun: keeping the failing inputs:", err)
			}
		}
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fuzzrun:", err)
	os.Exit(2)
}

var fuzzFunc = regexp.MustCompile(`(?m)^func (Fuzz[A-Za-z0-9_]*)\(f \*testing\.F\)`)

// find returns the fuzz targets of the module's test files, in package then name order.
func find(module string, tags []string) ([]target, error) {
	var out []target
	err := filepath.WalkDir(module, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "target", "vendor", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names := fuzzFunc.FindAllSubmatch(raw, -1)
		if len(names) == 0 || !buildOK(path, raw, tags) {
			return nil
		}
		rel, err := filepath.Rel(module, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, n := range names {
			out = append(out, target{rel: filepath.ToSlash(rel), name: string(n[1])})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].rel != out[j].rel {
			return out[i].rel < out[j].rel
		}
		return out[i].name < out[j].name
	})
	return out, err
}

// buildOK reports whether the file's //go:build line, if it has one, holds for the tags (plus cgo and
// the host platform, which a plain `go test` has).
func buildOK(path string, src []byte, tags []string) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		return false
	}
	have := map[string]bool{"cgo": true, runtime.GOOS: true, runtime.GOARCH: true, "gc": true}
	for _, t := range tags {
		have[t] = true
	}
	for _, group := range f.Comments {
		if group.End() > f.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			return expr.Eval(func(tag string) bool { return have[tag] || strings.HasPrefix(tag, "go1.") })
		}
	}
	return true
}

func runAll(module, tags, fuzztime string, jobs, parallel int, targets []target) []result {
	results := make([]result, len(targets))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runOne(module, tags, fuzztime, parallel, t)
		}()
	}
	wg.Wait()
	return results
}

var lastProgress = regexp.MustCompile(`execs: (\d+)`)

func runOne(module, tags, fuzztime string, parallel int, t target) result {
	before := inputs(filepath.Join(module, filepath.FromSlash(t.rel), "testdata", "fuzz", t.name))
	args := []string{"test", "-run=^$", "-fuzz=^" + t.name + "$", "-fuzztime=" + fuzztime, fmt.Sprintf("-parallel=%d", parallel)}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./"+t.rel)
	cmd := exec.Command("go", args...)
	cmd.Dir = module
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	start := time.Now()
	err := cmd.Run()
	res := result{target: t, ok: err == nil, elapsed: time.Since(start), output: buf.String(), execs: "?"}
	if m := lastProgress.FindAllStringSubmatch(res.output, -1); len(m) > 0 {
		res.execs = m[len(m)-1][1]
	}
	after := inputs(filepath.Join(module, filepath.FromSlash(t.rel), "testdata", "fuzz", t.name))
	for name := range after {
		if !before[name] {
			res.crashers = append(res.crashers, name)
		}
	}
	sort.Strings(res.crashers)
	return res
}

// inputs lists the files of a corpus directory.
func inputs(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

func report(results []result, total time.Duration) int {
	failed := 0
	fmt.Printf("%-48s %-38s %10s %8s  %s\n", "package", "target", "execs", "time", "result")
	for _, r := range results {
		verdict := "ok"
		if !r.ok {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%-48s %-38s %10s %7.0fs  %s\n", r.rel, r.name, r.execs, r.elapsed.Seconds(), verdict)
	}
	for _, r := range results {
		if !r.ok {
			fmt.Printf("\n--- %s %s\n%s\n", r.rel, r.name, tail(r.output, 40))
		}
	}
	fmt.Printf("\n%d targets, %d failed, %.0fs\n", len(results), failed, total.Seconds())
	return failed
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// keep copies the inputs that made targets fail, and the output of the run, under dest.
func keep(module, dest string, results []result) error {
	for _, r := range results {
		if r.ok {
			continue
		}
		dir := filepath.Join(dest, filepath.FromSlash(r.rel), r.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "output.txt"), []byte(r.output), 0o644); err != nil {
			return err
		}
		for _, name := range r.crashers {
			src := filepath.Join(module, filepath.FromSlash(r.rel), "testdata", "fuzz", r.name, name)
			input, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, name), input, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
