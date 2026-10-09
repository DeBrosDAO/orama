// Command whitepaper checks, generates and builds the Orama Whitepaper —
// Technical Reference (docs/whitepaper/technical-reference).
//
//	whitepaper check     run every gate; exit 1 on any problem
//	whitepaper gen       regenerate the generated appendices from the code
//	whitepaper diagrams  render stale D2 diagrams to stamped SVGs
//	whitepaper build     typeset the printed volumes to PDF (pandoc, typst)
//
// It is run from anywhere inside the repository; see the root Makefile's
// whitepaper targets.
package main

import (
	"fmt"
	"os"
)

const usage = "usage: whitepaper check|gen|diagrams|build"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "whitepaper:", err)
		os.Exit(1)
	}
}

func run(cmd string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to read the working directory: %w", err)
	}
	root, err := findRepoRoot(cwd)
	if err != nil {
		return err
	}
	b, err := loadBook(root)
	if err != nil {
		return err
	}
	switch cmd {
	case "check":
		return runCheck(b)
	case "gen":
		return writeGenerated(b)
	case "diagrams":
		return renderDiagrams(b)
	case "build":
		return buildBook(b)
	default:
		return fmt.Errorf("unknown command %q; %s", cmd, usage)
	}
}

func runCheck(b *Book) error {
	probs, err := runChecks(b)
	if err != nil {
		return err
	}
	for _, p := range probs {
		fmt.Println(p)
	}
	if len(probs) > 0 {
		return fmt.Errorf("%d problems; see docs/whitepaper/technical-reference/README.md", len(probs))
	}
	fmt.Println("whitepaper: all gates pass")
	return nil
}
