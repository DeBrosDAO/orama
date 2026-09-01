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

	// ReexecedAfterBinarySwap is set by the orchestrator when it re-execs
	// itself with the NEWLY-INSTALLED binary, post Phase 2b. The new
	// process detects this flag, skips the pre-binary phases (1, 2, 2b)
	// already done by the old binary, and runs Phase 3+ using its OWN
	// up-to-date compiled config-generation logic. Closes bugboard #15
	// chicken-and-egg: pre-fix, Phase 4 ran with the old binary's
	// compiled Phase4GenerateConfigs, so config changes only took effect
	// on the NEXT rollout.
	//
	// Hidden flag — set programmatically by orchestrator.go via os.Args,
	// not a documented user-facing option.
	ReexecedAfterBinarySwap bool

	// Anyone flags
	AnyoneClient bool
}

// ParseFlags parses upgrade command flags
func ParseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}

	fs.BoolVar(&flags.Force, "force", false, "Reconfigure all settings")
	fs.BoolVar(&flags.RestartServices, "restart", false, "Automatically restart services after upgrade")
	fs.BoolVar(&flags.SkipChecks, "skip-checks", false, "Skip minimum resource checks (RAM/CPU)")

	// Hidden flag — see Flags.ReexecedAfterBinarySwap doc. The fs.Bool
	// registers it without exposing in help output (no .Usage doc text
	// that operators would normally search for).
	fs.BoolVar(&flags.ReexecedAfterBinarySwap, "reexeced-after-binary-swap", false, "")

	// Remote upgrade flags
	fs.StringVar(&flags.Env, "env", "", "Target environment for remote rolling upgrade (devnet, testnet)")
	fs.StringVar(&flags.NodeFilter, "node", "", "Upgrade a single node IP only")
	fs.IntVar(&flags.Delay, "delay", 30, "Delay in seconds between nodes during rolling upgrade")

	// Nameserver flag - use pointer to detect if explicitly set
	nameserver := fs.Bool("nameserver", false, "Make this node a nameserver (uses saved preference if not specified)")

	// Anyone flags
	fs.BoolVar(&flags.AnyoneClient, "anyone-client", false, "Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay)")

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
