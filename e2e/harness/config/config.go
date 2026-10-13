// Package config holds the environment contract between the e2e-fleet runner
// and the feature test packages it starts, and the checks that keep a run away
// from shared environments.
package config

import (
	"fmt"
	"strings"
)

// Environment variables the runner sets for every feature package.
const (
	// EnvState is the path of the fleet state JSON the provisioner wrote.
	EnvState = "E2E_FLEET_STATE"
	// EnvStrict makes a feature package fail, instead of skip, when EnvState is
	// unset. The runner always sets it to 1.
	EnvStrict = "E2E_STRICT"
	// EnvCoverageEnforce makes the coverage gate's failure fail the command.
	EnvCoverageEnforce = "E2E_COVERAGE_ENFORCE"
	// EnvAgentSock is the throwaway RootWallet agent socket the CLI signs through.
	EnvAgentSock = "RW_AGENT_SOCK"
	// EnvE2E is set to 1 in the environment of every orama CLI invocation.
	EnvE2E = "ORAMA_E2E"
	// EnvEvidenceDir is the directory one package run records its evidence
	// in. The runner sets a directory per package run, so a re-run or a
	// resumed stage never appends to the evidence of an earlier attempt.
	EnvEvidenceDir = "E2E_EVIDENCE_DIR"
)

// Secret environment the run needs; `infisical run` injects it.
const (
	EnvHCloudToken = "HCLOUD_TOKEN"
	EnvCFToken     = "CF_API_TOKEN"
	EnvCFZone      = "CF_ZONE"
)

// RequiredRunEnv is what `e2e-fleet run` and `provision` refuse to start without.
var RequiredRunEnv = []string{EnvHCloudToken, EnvCFToken, EnvCFZone}

// forbiddenNameParts are the shared environments a run must never touch.
var forbiddenNameParts = []string{"testnet", "mainnet", "devnet", "stagenet"}

// forbiddenChainParts are the shared networks a run's chain id must never
// name. The run's own chain is a devnet chain by construction
// (orama-devnet-e2e-<run>), so devnet is not on this list.
var forbiddenChainParts = []string{"testnet", "mainnet", "stagenet"}

// AllowedZone is the only Cloudflare zone an e2e run may delegate from.
const AllowedZone = "dbrsteting.bid"

// RunNamePrefix starts every name a run creates: the CLI environment, the
// base domain's first label, the Hetzner servers.
const RunNamePrefix = "e2e-"

// chainIDMarker is what every run chain id contains.
const chainIDMarker = "-e2e-"

// Mode is how a feature package was started.
type Mode struct {
	// StatePath is EnvState; empty outside a fleet run.
	StatePath string
	// Strict is EnvStrict.
	Strict bool
}

// FromEnv reads the mode through lookup (os.LookupEnv in production).
func FromEnv(lookup func(string) (string, bool)) (Mode, error) {
	strict, err := Bool(lookup, EnvStrict, false)
	if err != nil {
		return Mode{}, err
	}
	path, _ := lookup(EnvState)
	return Mode{StatePath: strings.TrimSpace(path), Strict: strict}, nil
}

// Bool reads a boolean variable: 1/true/yes or 0/false/no, case-insensitive;
// unset or empty is def. Anything else is an error rather than a guess.
func Bool(lookup func(string) (string, bool), name string, def bool) (bool, error) {
	v, ok := lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("%s=%q is not a boolean (use 1 or 0)", name, v)
}

// CheckRunName refuses a name that contains testnet, mainnet, devnet or
// stagenet. what says which name it is ("run id", "zone"), so the refusal is
// actionable.
func CheckRunName(what, name string) error {
	return denyParts(what, name, forbiddenNameParts)
}

func denyParts(what, name string, parts []string) error {
	lower := strings.ToLower(name)
	for _, part := range parts {
		if strings.Contains(lower, part) {
			return fmt.Errorf("%s %q contains %q: e2e runs destroy servers and must never touch a shared environment", what, name, part)
		}
	}
	return nil
}

// CheckZone requires zone to be AllowedZone exactly (case and a trailing dot
// aside): a denylist of substrings cannot tell a production zone from a test one.
func CheckZone(zone string) error {
	if normalizeDomain(zone) != AllowedZone {
		return fmt.Errorf("zone %q is not %s, the only zone an e2e run may use", zone, AllowedZone)
	}
	return nil
}

// CheckEnvName requires the CLI environment to be a run's own (e2e-...) and
// to name no shared environment.
func CheckEnvName(env string) error {
	if !strings.HasPrefix(env, RunNamePrefix) {
		return fmt.Errorf("environment %q does not start with %q: the CLI must only ever point at the run's own environment", env, RunNamePrefix)
	}
	return CheckRunName("environment", env)
}

// CheckBaseDomain requires the base domain to be an e2e-... name directly
// under AllowedZone.
func CheckBaseDomain(domain string) error {
	d := normalizeDomain(domain)
	label, zone, ok := strings.Cut(d, ".")
	if !ok || zone != AllowedZone || !strings.HasPrefix(label, RunNamePrefix) {
		return fmt.Errorf("base domain %q is not %s<run>.%s", domain, RunNamePrefix, AllowedZone)
	}
	return CheckRunName("base domain", label)
}

// CheckChainID accepts no chain (empty) or a run chain: it contains -e2e- and
// names no shared network.
func CheckChainID(id string) error {
	if id == "" {
		return nil
	}
	if !strings.Contains(id, chainIDMarker) {
		return fmt.Errorf("chain id %q does not contain %q: only the run's own chain may be used", id, chainIDMarker)
	}
	return denyParts("chain id", id, forbiddenChainParts)
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}
