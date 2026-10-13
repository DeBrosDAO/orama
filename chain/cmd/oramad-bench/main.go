// Command oramad-bench is the load generator and saturation tester of the Orama chain. It signs
// transactions offline, broadcasts them straight to the validators' CometBFT RPC at a fixed rate
// (open loop) or a fixed number in flight (closed loop), and reports how many the chain took, how
// long they waited, how full the mempools got, what the base fee did, and whether every validator
// computed the same state at every height of the run.
//
//	oramad-bench run     -profile smoke -funder-key-file KEY   run a profile against a localnet
//	oramad-bench prepare -profile smoke -funder-key-file KEY   build the shielded bundle cache
//	oramad-bench list                                          the transaction types and profiles
//
// The localnet is scripts/localnet/localnet.sh; `make bench-load` starts one, runs the smoke
// profile and stops it. The full profiles are described in
// website/src/docs/contributor/testing.mdx.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/runner"
)

// Exit codes.
const (
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitMismatch = 3
)

const usage = "usage: oramad-bench run|prepare|list [flags]  (oramad-bench run -h for the flags)"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// execute runs one command and returns the process exit code.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "run":
		return doRun(ctx, args[1:], stdout, stderr)
	case "prepare":
		return doPrepare(ctx, args[1:], stderr)
	case "list":
		return doList(stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return exitUsage
	}
}

func doRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parseOptions("run", args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	o.cfg.Log = stderr
	rep, err := runner.Run(ctx, o.cfg)
	if err != nil {
		fmt.Fprintln(stderr, "run failed:", err)
		return exitError
	}
	if err := rep.WriteJSON(o.out); err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	if err := rep.WriteSummary(stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	fmt.Fprintf(stdout, "report: %s\n", o.out)
	if !rep.AppHashAgreed {
		fmt.Fprintln(stderr, "FAILED: the validators disagree on state; see the app hash section of the report")
		return exitMismatch
	}
	return exitOK
}

func doPrepare(ctx context.Context, args []string, stderr io.Writer) int {
	o, err := parseOptions("prepare", args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	o.cfg.Log = stderr
	if _, err := runner.Prepare(ctx, o.cfg); err != nil {
		fmt.Fprintln(stderr, "prepare failed:", err)
		return exitError
	}
	return exitOK
}

func doList(stdout io.Writer) int {
	fmt.Fprintln(stdout, "transaction types:")
	for _, name := range gen.Names() {
		g, err := gen.New(name)
		if err != nil {
			continue
		}
		fmt.Fprintf(stdout, "  %-22s %s\n", name, g.Describe())
	}
	fmt.Fprintln(stdout, "\nprofiles:")
	for _, name := range []string{"smoke", "full"} {
		p, err := profile.ByName(name)
		if err != nil {
			continue
		}
		var types []string
		for _, t := range p.Types {
			types = append(types, t.Type)
		}
		fmt.Fprintf(stdout, "  %-6s %d accounts: %s\n", name, p.Accounts, strings.Join(types, " "))
	}
	return exitOK
}
