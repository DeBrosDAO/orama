package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// preflightInput is what the preflight looks at; lookup is os.LookupEnv in
// production and realHome the user database's home directory.
type preflightInput struct {
	lookup   func(string) (string, bool)
	realHome string
	runID    string
}

// preflight refuses a run that could touch a shared environment, is missing
// its secrets (named, never shown), or could reach the owner's real wallet.
// Every problem is reported at once.
func preflight(in preflightInput) error {
	var errs []error
	if missing := secrets.MissingEnv(config.RequiredRunEnv, in.lookup); len(missing) > 0 {
		errs = append(errs, fmt.Errorf("missing required environment (run through `make e2e-fleet`, which uses infisical run): %s",
			strings.Join(missing, ", ")))
	}
	zone, _ := in.lookup(config.EnvCFZone)
	errs = append(errs, checkNames([][2]string{{"run id", in.runID}, {"zone " + config.EnvCFZone, zone}})...)
	if err := config.CheckZone(zone); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", config.EnvCFZone, err))
	}
	if sock, ok := in.lookup(config.EnvAgentSock); ok && sock != "" {
		if err := secrets.CheckAgentSockNotRealWallet(sock, in.realHome); err != nil {
			errs = append(errs, fmt.Errorf("refusing to run with the inherited agent socket: %w", err))
		}
	}
	return errors.Join(errs...)
}

// checkProvisioned applies the run guards (fleet.CheckState) to a state:
// after provisioning, and on every state a command loads.
func checkProvisioned(st *fleet.State, realHome string) error {
	return fleet.CheckState(st, realHome)
}

// checkNames applies config.CheckRunName to (what, name) pairs, in order.
func checkNames(pairs [][2]string) []error {
	var errs []error
	for _, p := range pairs {
		if err := config.CheckRunName(p[0], p[1]); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
