// Command benchgate is the regression gate of `make bench-compare` (chain/Makefile).
//
//	benchgate stamp [-baseline] [-allow-unstable]
//	    Prints the one comment line `make bench` puts at the top of a benchmark file: where it was
//	    measured and whether it is a release baseline. With -baseline it refuses, unless
//	    -allow-unstable is given, on a machine that is not linux/amd64 with at least 8 CPUs.
//
//	benchgate compare -base FILE -new FILE [-threshold PERCENT] [-allow-unstable] < benchstat.csv
//	    Reads `benchstat -format csv FILE FILE` on stdin and exits 1 when a benchmark's time/op got
//	    more than the threshold slower (and benchstat says it is not noise) or a baseline
//	    benchmark has no result in the new run.
//
// Exit status: 0 the gate passed, 1 it failed, 2 the input or the arguments are unusable.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
)

const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2

	usage = "usage: benchgate stamp [-baseline] [-allow-unstable] | benchgate compare -base FILE -new FILE [-threshold PERCENT] [-allow-unstable]"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "stamp":
		return runStamp(args[1:], stdout, stderr)
	case "compare":
		return runCompare(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "benchgate: unknown command %q\n%s\n", args[0], usage)
		return exitUsage
	}
}

func runStamp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseline := fs.Bool("baseline", false, "the file is a release baseline")
	allowUnstable := fs.Bool("allow-unstable", false, "write a baseline from an unstable host, marked as not a release baseline")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	host := Host{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	stamp, err := NewStamp(host, *baseline, *allowUnstable)
	if err != nil {
		fmt.Fprintf(stderr, "benchgate: %v\n", err)
		return exitFailed
	}
	fmt.Fprintln(stdout, stamp.Line())
	return exitOK
}

func runCompare(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	basePath := fs.String("base", "", "the baseline benchmark file")
	newPath := fs.String("new", "", "the new benchmark file")
	threshold := fs.Float64("threshold", DefaultRegressionPercent, "percent slower in time/op that fails")
	allowUnstable := fs.Bool("allow-unstable", false, "compare a non-release baseline or a run from an unstable host")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *basePath == "" || *newPath == "" || *threshold <= 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	if err := checkFiles(*basePath, *newPath, *allowUnstable); err != nil {
		fmt.Fprintf(stderr, "benchgate: %v\n", err)
		return exitUsage
	}
	rows, err := ParseBenchstatCSV(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "benchgate: %v\n", err)
		return exitUsage
	}
	verdict, err := Judge(rows, *threshold)
	if err != nil {
		fmt.Fprintf(stderr, "benchgate: %v\n", err)
		return exitUsage
	}
	report(stdout, verdict, *threshold)
	if !verdict.OK() {
		return exitFailed
	}
	return exitOK
}

// checkFiles reads both stamps and applies CheckProvenance.
func checkFiles(basePath, newPath string, allowUnstable bool) error {
	base, err := stampOf(basePath)
	if err != nil {
		return fmt.Errorf("baseline %s: %w", basePath, err)
	}
	fresh, err := stampOf(newPath)
	if err != nil {
		return fmt.Errorf("new run %s: %w", newPath, err)
	}
	return CheckProvenance(base, fresh, allowUnstable)
}

func stampOf(path string) (Stamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stamp{}, fmt.Errorf("open benchmark file: %w", err)
	}
	defer f.Close()
	return ParseStamp(f)
}

func report(w io.Writer, v Verdict, threshold float64) {
	fmt.Fprintf(w, "benchgate: %d benchmark(s) compared, threshold %.0f%% time/op\n", v.Compared, threshold)
	for _, r := range v.Regressions {
		fmt.Fprintf(w, "REGRESSION %s: %+.1f%% (%.4g s/op -> %.4g s/op)\n", r.Name, r.Delta(), r.Old, r.New)
	}
	for _, name := range v.Missing {
		fmt.Fprintf(w, "MISSING %s: in the baseline, no result in the new run (skipped, renamed or removed)\n", name)
	}
	for _, name := range v.New {
		fmt.Fprintf(w, "new %s: no baseline yet\n", name)
	}
	if v.OK() {
		fmt.Fprintln(w, "benchgate: PASS")
		return
	}
	fmt.Fprintln(w, "benchgate: FAIL")
}
