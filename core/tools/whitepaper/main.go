// Command whitepaper checks, generates and builds an Orama Whitepaper book:
// the Technical Reference (docs/whitepaper/technical-reference, the default)
// or another book chosen with -book.
//
//	whitepaper check     run every gate; exit 1 on any problem
//	whitepaper gen       regenerate the generated appendices from the code
//	whitepaper diagrams  render stale D2 diagrams to stamped SVGs
//	whitepaper build     typeset the printed volumes to PDF (pandoc, typst)
//
// Every command takes -book <dir>, a book directory relative to the
// repository root.
//
// It is run from anywhere inside the repository; see the root Makefile's
// whitepaper targets.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = "usage: whitepaper check|gen|diagrams|build [-book dir]"

func main() {
	cmd, book, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(cmd, book); err != nil {
		fmt.Fprintln(os.Stderr, "whitepaper:", err)
		os.Exit(1)
	}
}

// parseArgs reads the command and the `-book` directory (default bookDir)
// from the command line.
func parseArgs(args []string) (cmd, book string, err error) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("whitepaper", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&book, "book", bookDir, "book directory, relative to the repository root")
	if err := fs.Parse(args); err != nil {
		return "", "", fmt.Errorf("%w; %s", err, usage)
	}
	if cmd == "" && fs.NArg() == 1 {
		cmd = fs.Arg(0)
	} else if cmd == "" || fs.NArg() > 0 {
		return "", "", errors.New(usage)
	}
	if book, err = cleanBookRel(book); err != nil {
		return "", "", err
	}
	return cmd, book, nil
}

func run(cmd, book string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to read the working directory: %w", err)
	}
	root, err := findRepoRoot(cwd, book)
	if err != nil {
		return err
	}
	b, err := loadBook(root, book)
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
		return fmt.Errorf("%d problems; see %s/README.md", len(probs), b.RelDir())
	}
	fmt.Println("whitepaper: all gates pass")
	return nil
}
