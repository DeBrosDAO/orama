// Package wizard is the terminal UI of `orama setup`: the questions a person is
// asked when they run it without the flags, then the same plan and the same run
// as the flags give, with live progress per machine.
//
// The model asks the setup package for everything it shows (the plan, the
// inspection of each machine, the run) through Services, so a test drives it by
// feeding messages and reading what it would draw.
package wizard

import (
	"context"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// HostKey is one SSH host key a machine presents.
type HostKey struct {
	Type        string
	Fingerprint string
}

// NetworkChoice is a network the registry knows.
type NetworkChoice struct {
	Name    string
	ChainID string
	// Default marks the one chosen when the person just presses enter.
	Default bool
	// Announced marks a network whose chain does not exist yet: it cannot be
	// joined, and creating it needs no chain id or release root.
	Announced bool
}

// Services is what the wizard needs from the outside. Each is called from a
// command the model returns, never from Update itself.
type Services struct {
	// Wallet fails with what to do when the RootWallet is not ready.
	Wallet func(ctx context.Context) error
	// Networks are the networks to choose from.
	Networks func() ([]NetworkChoice, error)
	// HostKeys scans the host keys a machine presents.
	HostKeys func(ctx context.Context, ip string) ([]HostKey, error)
	// Inspect reaches the machines and checks their hardware.
	Inspect func(ctx context.Context, opts setup.Options) ([]setup.Inspection, error)
	// Plan is the plan the options give.
	Plan func(ctx context.Context, opts setup.Options) (*setup.Plan, error)
	// Run does the setup, reporting to rep.
	Run func(ctx context.Context, opts setup.Options, rep setup.Reporter) (*setup.Result, error)
}

// Outcome is how the wizard ended.
type Outcome struct {
	// Result is set when the run finished.
	Result *setup.Result
	// Err is why it stopped: a failed run, or the person quitting.
	Err error
	// OpenStatus says the person asked to open `orama status`.
	OpenStatus bool
	// Quit says the person left before the run started.
	Quit bool
}
