// Command oramad-state writes a large application state into a node home and measures what that
// state does to oramad: boot, snapshot and restore, genesis export, the indexer's catch-up and block
// pruning. It is a separate binary, not a subcommand of oramad, so a production node never ships the
// generator; it drives the oramad binary it is given for everything a node does itself.
//
//	oramad-state prepare  --home H            a home for a chain of one validator
//	oramad-state stategen --home H ...        write the state (resumable, deterministic)
//	oramad-state boot     --home H            node start to first block
//	oramad-state snapshot --home H            snapshot create, dump, load and restore
//	oramad-state export   --home H            genesis export
//	oramad-state indexer  --home H --blocks N the follower's catch-up over N blocks
//	oramad-state prune                        block-store pruning at x/archive's retain height
//
// Every command prints one JSON object on standard output and its progress on standard error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
)

// command runs one subcommand and returns the value that is printed as JSON.
type command func(ctx context.Context, args []string, logf func(string, ...any)) (any, error)

var commands = map[string]command{
	"prepare":  cmdPrepare,
	"stategen": cmdStategen,
	"boot":     cmdBoot,
	"snapshot": cmdSnapshot,
	"export":   cmdExport,
	"indexer":  cmdIndexer,
	"prune":    cmdPrune,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "oramad-state:", err)
		os.Exit(1)
	}
}

// run dispatches args to a subcommand and writes its result as indented JSON to out.
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q; %w", args[0], usage())
	}
	logf := func(format string, a ...any) { fmt.Fprintf(errOut, format+"\n", a...) }
	result, err := cmd(ctx, args[1:], logf)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func usage() error {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return errors.New("usage: oramad-state <" + strings.Join(names, "|") + "> [flags]; each command's -h lists its flags")
}

// newFlags is a flag set that returns its errors instead of exiting.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}
