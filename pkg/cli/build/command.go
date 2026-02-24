package build

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Flags represents build command flags.
type Flags struct {
	Arch    string
	Output  string
	Verbose bool
}

// Handle is the entry point for the build command.
func Handle(args []string) {
	flags, err := parseFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	b := NewBuilder(flags)
	if err := b.Build(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.StringVar(&flags.Arch, "arch", "amd64", "Target architecture (amd64, arm64)")
	fs.StringVar(&flags.Output, "output", "", "Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz)")
	fs.BoolVar(&flags.Verbose, "verbose", false, "Verbose output")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	return flags, nil
}

// findProjectRoot walks up from the current directory looking for go.mod.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Verify it's the network project
			if _, err := os.Stat(filepath.Join(dir, "cmd", "cli")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find project root (no go.mod with cmd/cli found)")
}

// detectHostArch returns the host architecture in Go naming convention.
func detectHostArch() string {
	return runtime.GOARCH
}
