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
	Nameserver      *bool  // Pointer so we can detect if explicitly set vs default

	// Anyone relay operator flags
	AnyoneRelay    bool
	AnyoneExit     bool
	AnyoneMigrate  bool
	AnyoneNickname string
	AnyoneContact  string
	AnyoneWallet   string
	AnyoneORPort   int
	AnyoneFamily   string
}

// ParseFlags parses upgrade command flags
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.BoolVar(&flags.Force, "force", false, "Reconfigure all settings")
	fs.BoolVar(&flags.RestartServices, "restart", false, "Automatically restart services after upgrade")
	fs.BoolVar(&flags.NoPull, "no-pull", false, "Skip source download, use existing /home/debros/src")
	fs.StringVar(&flags.Branch, "branch", "", "Git branch to use (uses saved preference if not specified)")

	// Nameserver flag - use pointer to detect if explicitly set
	nameserver := fs.Bool("nameserver", false, "Make this node a nameserver (uses saved preference if not specified)")

	// Anyone relay operator flags
	fs.BoolVar(&flags.AnyoneRelay, "anyone-relay", false, "Run as Anyone relay operator (earn rewards)")
	fs.BoolVar(&flags.AnyoneExit, "anyone-exit", false, "Run as exit relay (requires --anyone-relay, legal implications)")
	fs.BoolVar(&flags.AnyoneMigrate, "anyone-migrate", false, "Migrate existing Anyone installation into Orama Network")
	fs.StringVar(&flags.AnyoneNickname, "anyone-nickname", "", "Relay nickname (1-19 alphanumeric chars)")
	fs.StringVar(&flags.AnyoneContact, "anyone-contact", "", "Contact info (email or @telegram)")
	fs.StringVar(&flags.AnyoneWallet, "anyone-wallet", "", "Ethereum wallet address for rewards")
	fs.IntVar(&flags.AnyoneORPort, "anyone-orport", 9001, "ORPort for relay (default 9001)")
	fs.StringVar(&flags.AnyoneFamily, "anyone-family", "", "Comma-separated fingerprints of other relays you operate")

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

	// Set nameserver if explicitly provided
	if *nameserver {
		flags.Nameserver = nameserver
	}

	return flags, nil
}
