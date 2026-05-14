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
	SkipChecks      bool
	Nameserver      *bool // Pointer so we can detect if explicitly set vs default

	// Remote upgrade flags
	Env        string // Target environment for remote rolling upgrade
	NodeFilter string // Single node IP to upgrade (optional)
	Delay      int    // Delay in seconds between nodes during rolling upgrade

	// Anyone flags
	AnyoneClient     bool
	AnyoneRelay      bool
	AnyoneExit       bool
	AnyoneMigrate    bool
	AnyoneNickname   string
	AnyoneContact    string
	AnyoneWallet     string
	AnyoneORPort     int
	AnyoneFamily     string
	AnyoneBandwidth  int // Percentage of VPS bandwidth for relay (default: 30, 0=unlimited)
	AnyoneAccounting int // Monthly data cap for relay in GB (0=unlimited)
}

// ParseFlags parses upgrade command flags
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.BoolVar(&flags.Force, "force", false, "Reconfigure all settings")
	fs.BoolVar(&flags.RestartServices, "restart", false, "Automatically restart services after upgrade")
	fs.BoolVar(&flags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")

	// Remote upgrade flags
	fs.StringVar(&flags.Env, "env", "", "Target environment for remote rolling upgrade (devnet, testnet)")
	fs.StringVar(&flags.NodeFilter, "node", "", "Upgrade a single node IP only")
	fs.IntVar(&flags.Delay, "delay", 30, "Delay in seconds between nodes during rolling upgrade")

	// Nameserver flag - use pointer to detect if explicitly set
	nameserver := fs.Bool("nameserver", false, "Make this node a nameserver (uses saved preference if not specified)")

	// Anyone flags
	fs.BoolVar(&flags.AnyoneClient, "anyone-client", false, "Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay)")
	fs.BoolVar(&flags.AnyoneRelay, "anyone-relay", false, "Run as Anyone relay operator (earn rewards)")
	fs.BoolVar(&flags.AnyoneExit, "anyone-exit", false, "Run as exit relay (requires --anyone-relay, legal implications)")
	fs.BoolVar(&flags.AnyoneMigrate, "anyone-migrate", false, "Migrate existing Anyone installation into Orama Network")
	fs.StringVar(&flags.AnyoneNickname, "anyone-nickname", "", "Relay nickname (1-19 alphanumeric chars)")
	fs.StringVar(&flags.AnyoneContact, "anyone-contact", "", "Contact info (email or @telegram)")
	fs.StringVar(&flags.AnyoneWallet, "anyone-wallet", "", "Ethereum wallet address for rewards")
	fs.IntVar(&flags.AnyoneORPort, "anyone-orport", 9001, "ORPort for relay (default 9001)")
	fs.StringVar(&flags.AnyoneFamily, "anyone-family", "", "Comma-separated fingerprints of other relays you operate")
	fs.IntVar(&flags.AnyoneBandwidth, "anyone-bandwidth", 30, "Limit relay to N% of VPS bandwidth (0=unlimited, runs speedtest)")
	fs.IntVar(&flags.AnyoneAccounting, "anyone-accounting", 0, "Monthly data cap for relay in GB (0=unlimited)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Set nameserver if explicitly provided
	if *nameserver {
		flags.Nameserver = nameserver
	}

	return flags, nil
}

