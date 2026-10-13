// Command scangate judges the output of govulncheck and staticcheck the way the fleet scanners
// feature does (e2e/features/scanners), without a fleet, so CI can gate on it.
//
//	govulncheck -format json ./... | scangate vuln --module chain --accepted e2e/features/scanners/govulncheck-accepted.yaml
//	staticcheck -f json ./...      | scangate static
//
// vuln reads govulncheck's JSON from stdin and fails on a reachable vulnerability that the accepted
// list does not name, on a listed one that is no longer reported and on one past its review date
// (package vulnaccept). static reads staticcheck's JSON and fails on every finding except the
// deprecation warnings in files protoc-gen-grpc-gateway v1 generated (package staticfind).
//
// Exit codes: 0 clean, 1 findings, 2 usage or unreadable input. A scanner that printed nothing
// parseable is exit 2, never a pass.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/staticfind"
	"github.com/DeBrosOfficial/network/e2e/harness/vulnaccept"
)

const (
	exitClean    = 0
	exitFindings = 1
	exitUsage    = 2
)

// scannedModules are the modules the accepted list may name: the directories the fleet scanners
// feature runs govulncheck in (vulnModules in features/scanners/helpers_test.go).
var scannedModules = []string{
	"core", "chain", "e2e", "caddy",
	"core/thirdparty/ipfs-cluster", "core/thirdparty/olric",
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now()))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: scangate vuln --module <dir> --accepted <file> | scangate static")
		return exitUsage
	}
	switch args[0] {
	case "vuln":
		return runVuln(args[1:], stdin, stdout, stderr, now)
	case "static":
		return runStatic(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "scangate: unknown command %q (want vuln or static)\n", args[0])
		return exitUsage
	}
}

func runVuln(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("vuln", flag.ContinueOnError)
	fs.SetOutput(stderr)
	module := fs.String("module", "", "directory govulncheck ran in, as the accepted list names it (for example chain)")
	acceptedPath := fs.String("accepted", "", "the accepted-vulnerabilities file")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *module == "" || *acceptedPath == "" {
		fmt.Fprintln(stderr, "scangate vuln: --module and --accepted are required")
		return exitUsage
	}
	data, err := os.ReadFile(*acceptedPath)
	if err != nil {
		fmt.Fprintf(stderr, "scangate vuln: failed to read the accepted list: %v\n", err)
		return exitUsage
	}
	accepted, err := vulnaccept.Parse(data, now, scannedModules)
	if err != nil {
		fmt.Fprintf(stderr, "scangate vuln: %v\n", err)
		return exitUsage
	}
	out, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "scangate vuln: failed to read govulncheck output: %v\n", err)
		return exitUsage
	}
	found, err := vulnaccept.Called(out)
	if err != nil {
		fmt.Fprintf(stderr, "scangate vuln: %v\n", err)
		return exitUsage
	}
	problems := vulnaccept.Judge(*module, found, accepted, now)
	for _, p := range problems {
		fmt.Fprintln(stderr, p)
	}
	if len(problems) > 0 {
		return exitFindings
	}
	fmt.Fprintf(stdout, "govulncheck %s: %d reachable, all accepted\n", *module, len(found))
	return exitClean
}

func runStatic(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("static", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	out, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "scangate static: failed to read staticcheck output: %v\n", err)
		return exitUsage
	}
	found, err := staticfind.Parse(out)
	if err != nil {
		fmt.Fprintf(stderr, "scangate static: %v\n", err)
		return exitUsage
	}
	remaining := staticfind.Remaining(found, staticfind.GeneratedByGateway)
	for _, f := range remaining {
		fmt.Fprintln(stderr, f)
	}
	if len(remaining) > 0 {
		return exitFindings
	}
	fmt.Fprintf(stdout, "staticcheck: %d findings, all in generated gateway code\n", len(found))
	return exitClean
}
