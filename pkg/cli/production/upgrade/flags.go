package upgrade

import (
	"flag"
	"fmt"
	"os"
)

// Flags represents upgrade command flags
type Flags struct {
	Force           bool
	RestartServices bool
	NoPull          bool
	Branch          string
}

// ParseFlags parses upgrade command flags
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.BoolVar(&flags.Force, "force", false, "Reconfigure all settings")
	fs.BoolVar(&flags.RestartServices, "restart", false, "Automatically restart services after upgrade")
	fs.BoolVar(&flags.NoPull, "no-pull", false, "Skip git clone/pull, use existing /home/debros/src")
	fs.StringVar(&flags.Branch, "branch", "", "Git branch to use (main or nightly, uses saved preference if not specified)")

	// Support legacy flags for backwards compatibility
	nightly := fs.Bool("nightly", false, "Use nightly branch (deprecated, use --branch nightly)")
	main := fs.Bool("main", false, "Use main branch (deprecated, use --branch main)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Handle legacy flags
	if *nightly {
		flags.Branch = "nightly"
	}
	if *main {
		flags.Branch = "main"
	}

	// Validate branch if provided
	if flags.Branch != "" && flags.Branch != "main" && flags.Branch != "nightly" {
		return nil, fmt.Errorf("invalid branch: %s (must be 'main' or 'nightly')", flags.Branch)
	}

	return flags, nil
}
