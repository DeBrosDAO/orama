package main

import (
	"fmt"
	"io"
	"strings"
)

// Status is the verdict of one smoke check.
type Status string

const (
	// Pass means the behaviour was observed.
	Pass Status = "PASS"
	// Fail means it was not, or the opposite was.
	Fail Status = "FAIL"
	// Skip means the check cannot say on this environment. It is never used to hide a failure: a
	// skip names the environmental cause it detected.
	Skip Status = "SKIP"
)

// Result is one line of the smoke report.
type Result struct {
	Name   string
	Status Status
	Detail string
}

func pass(name, format string, args ...any) Result {
	return Result{Name: name, Status: Pass, Detail: fmt.Sprintf(format, args...)}
}

func fail(name, format string, args ...any) Result {
	return Result{Name: name, Status: Fail, Detail: fmt.Sprintf(format, args...)}
}

func skip(name, format string, args ...any) Result {
	return Result{Name: name, Status: Skip, Detail: fmt.Sprintf(format, args...)}
}

// Line is how a result prints: "PASS name - detail", the detail on one line.
func (r Result) Line() string {
	detail := strings.Join(strings.Fields(r.Detail), " ")
	if detail == "" {
		return fmt.Sprintf("%s %s", r.Status, r.Name)
	}
	return fmt.Sprintf("%s %s - %s", r.Status, r.Name, detail)
}

// Report prints every result and a summary, and returns the number of failures.
func Report(w io.Writer, results []Result) int {
	counts := map[Status]int{}
	for _, r := range results {
		fmt.Fprintln(w, r.Line())
		counts[r.Status]++
	}
	fmt.Fprintf(w, "SUMMARY %d pass, %d fail, %d skip\n", counts[Pass], counts[Fail], counts[Skip])
	return counts[Fail]
}
