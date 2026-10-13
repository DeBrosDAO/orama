package setupcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	psetup "github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup/wizard"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// runWizard asks the questions on the terminal, runs the setup with live
// progress, and opens `orama status` if the person asks to.
func runWizard(ctx context.Context, cmd *cobra.Command, preset setup.Options) error {
	outcome, err := wizard.Start(ctx, wizardServices(), preset)
	if err != nil {
		return fmt.Errorf("the setup wizard: %w", err)
	}
	if outcome.Err != nil {
		return outcome.Err
	}
	if outcome.Result != nil {
		printSummary(cmd.OutOrStdout(), outcome.Result)
	}
	if outcome.OpenStatus && outcome.Result != nil {
		return openStatus(ctx, outcome.Result.Env)
	}
	return nil
}

// wizardServices connects the wizard to the setup package.
func wizardServices() wizard.Services {
	quiet := &setup.TextReporter{Out: io.Discard}
	deps := setup.NewDeps(quiet)
	return wizard.Services{
		Wallet:   deps.Wallet.Unlocked,
		Networks: networkChoices,
		HostKeys: hostKeys,
		Inspect: func(ctx context.Context, o setup.Options) ([]setup.Inspection, error) {
			if err := setup.ResolveAnnounced(ctx, &o, deps); err != nil {
				return nil, err
			}
			return setup.Inspect(ctx, o, deps.Enroll)
		},
		Plan: func(ctx context.Context, o setup.Options) (*setup.Plan, error) { return setup.PlanFor(ctx, o, deps) },
		Run: func(ctx context.Context, o setup.Options, rep setup.Reporter) (*setup.Result, error) {
			return setup.Run(ctx, o, setup.NewDeps(rep))
		},
	}
}

// hostKeys scans the host keys a machine presents.
func hostKeys(_ context.Context, ip string) ([]wizard.HostKey, error) {
	keys, err := psetup.HostKeys(ip)
	if err != nil {
		return nil, err
	}
	out := make([]wizard.HostKey, len(keys))
	for i, k := range keys {
		out[i] = wizard.HostKey{Type: k.Type, Fingerprint: k.Fingerprint}
	}
	return out, nil
}

// networkChoices are the networks the CLI knows, the active one first chosen.
func networkChoices() ([]wizard.NetworkChoice, error) {
	reg, err := cli.LoadNetworks()
	if err != nil {
		return nil, err
	}
	return choicesFrom(reg, activeNetwork())
}

func activeNetwork() string {
	if env, err := cli.GetActiveEnvironment(); err == nil && env != nil {
		if env.Network != "" {
			return env.Network
		}
		return env.Name
	}
	return ""
}

func choicesFrom(reg *netregistry.Registry, active string) ([]wizard.NetworkChoice, error) {
	var out []wizard.NetworkChoice
	for _, name := range reg.Names() {
		n, err := reg.Get(name)
		if err != nil {
			return nil, fmt.Errorf("read network %s: %w", name, err)
		}
		out = append(out, choiceOf(n, active))
	}
	return out, nil
}

// choiceOf is the wizard's view of a network: whether it is the active one, and whether its
// chain is created yet, and whether its manifest pins the Tor network file its relays join.
func choiceOf(n *netregistry.Network, active string) wizard.NetworkChoice {
	m := n.Manifest
	return wizard.NetworkChoice{Name: m.Name, ChainID: m.ChainID, Default: m.Name == active, Announced: m.Announced(), TorNetwork: m.TorNetworkSHA256 != ""}
}
