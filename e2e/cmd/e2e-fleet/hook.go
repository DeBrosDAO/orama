package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
)

// hookBudget bounds one hook.
const hookBudget = 20 * time.Minute

// cmdHook implements the core/e2e/lifecycle hooks against the fleet in
// E2E_FLEET_STATE:
//
//	ORAMA_LIFECYCLE_DESTROY="e2e-fleet hook destroy"    (host as $1)
//	ORAMA_LIFECYCLE_BREAK="e2e-fleet hook break"        (host as $1)
//	ORAMA_LIFECYCLE_PROVISION="e2e-fleet hook provision" (prints the new IP)
func cmdHook(parent context.Context, args []string) (int, error) {
	if len(args) == 0 {
		return exitUsage, requireArgs(args, 1, "hook destroy <host> | break <host> | provision [--name N] [--location L]")
	}
	st, statePath, err := loadState()
	if err != nil {
		return exitFail, err
	}
	ctx, cancel := context.WithTimeout(parent, hookBudget)
	defer cancel()
	switch args[0] {
	case "destroy":
		return hookMember(ctx, st, statePath, args[1:], provision.DestroyNode, true)
	case "break":
		return hookMember(ctx, st, statePath, args[1:], provision.BreakUpgrade, false)
	case "provision":
		return hookProvision(ctx, st, statePath, args[1:])
	}
	return exitUsage, requireArgs(nil, 1, "hook destroy|break|provision")
}

func hookMember(ctx context.Context, st *fleet.State, statePath string, args []string,
	act func(context.Context, *fleet.State, string) error, changesState bool) (int, error) {
	if err := requireArgs(args, 1, "<host>"); err != nil {
		return exitUsage, err
	}
	host := args[0]
	if net.ParseIP(host) == nil {
		return exitUsage, fmt.Errorf("%w: host %q is not an IP address", errUsage, host)
	}
	if err := act(ctx, st, host); err != nil {
		return exitFail, err
	}
	if changesState {
		if err := st.Save(statePath); err != nil {
			return exitFail, err
		}
	}
	return exitOK, nil
}

func hookProvision(ctx context.Context, st *fleet.State, statePath string, args []string) (int, error) {
	fs := flag.NewFlagSet("hook provision", flag.ContinueOnError)
	name := fs.String("name", fleet.NextExtraName(st), "extra server name")
	location := fs.String("location", defaultLocation(st), "Hetzner location")
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	n, err := provision.AddExtra(ctx, st, *name, *location)
	if err != nil {
		return exitFail, err
	}
	if err := st.Save(statePath); err != nil {
		return exitFail, err
	}
	// stdout carries the IP only: the lifecycle harness reads it.
	fmt.Println(n.PublicIP)
	return exitOK, nil
}

func defaultLocation(st *fleet.State) string {
	if len(st.Nodes) > 0 && st.Nodes[0].Location != "" {
		return st.Nodes[0].Location
	}
	return provision.DefaultLocation
}
