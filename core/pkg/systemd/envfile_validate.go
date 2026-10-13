package systemd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// A namespace unit's env file is written as plain KEY=value lines, and some
// values end up inside a shell command line. GenerateEnvFile used to write
// whatever it was handed: a value with a newline added assignments of its
// own, and the rqlite unit runs
//
//	/bin/sh -c 'exec rqlited … ${JOIN_ARGS} …'
//
// where systemd substitutes the value into the script text before sh reads it,
// so a join address carrying `;` or `$(…)` ran as a command in that unit. The
// values come from the cluster — a spawn request, the registry — so they are
// validated where they are written, whoever sent them.

// envKeyPattern is a name systemd and a shell both accept.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// shellSafeValue is every character a value substituted into a unit's shell
// command line may contain: enough for addresses, paths, peer IDs, hex
// secrets and flags (`-join 10.0.0.2:10001,10.0.0.3:10001 -join-as orama`),
// and nothing a shell reads as syntax, quoting, expansion or a glob.
var shellSafeValue = regexp.MustCompile(`^[A-Za-z0-9 ._:/,=@+-]*$`)

// ShellInterpolatedServices are the services whose unit substitutes env
// values into a `sh -c`/`bash -c` script. A test reads the shipped templates
// and fails if one that does is missing here.
var ShellInterpolatedServices = map[ServiceType]bool{
	ServiceTypeRQLite:      true,
	ServiceTypeIPFS:        true,
	ServiceTypeIPFSCluster: true,
}

// validateEnvFile checks everything GenerateEnvFile is about to write for
// service in namespace.
func validateEnvFile(namespace, nodeID string, service ServiceType, envVars map[string]string) error {
	if !unitenv.Valid(namespace, string(service)) {
		return fmt.Errorf("namespace %q and service %q cannot name a unit env file (letters, digits, '-' and '_')", namespace, service)
	}
	shell := ShellInterpolatedServices[service]
	if err := validateEnvValue("NODE_ID", nodeID, shell); err != nil {
		return err
	}
	for key, value := range envVars {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("env var name %q for %s@%s is not a valid name", key, service, namespace)
		}
		if err := validateEnvValue(key, value, shell); err != nil {
			return fmt.Errorf("%s@%s: %w", service, namespace, err)
		}
	}
	return nil
}

// validateEnvValue refuses a value the plain KEY=value format cannot carry —
// a line break or NUL ends the assignment, a backslash or a quote is consumed
// by systemd's parser — and, for a shell-interpolated service, anything
// outside shellSafeValue.
func validateEnvValue(key, value string, shell bool) error {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the value of %s contains control character %U, which would end or corrupt its line in the env file", key, r)
		}
	}
	if strings.ContainsAny(value, "\\\"'") {
		return fmt.Errorf("the value of %s contains a quote or backslash, which systemd's env file parser would consume", key)
	}
	if shell && !shellSafeValue.MatchString(value) {
		return fmt.Errorf("the value of %s (%q) contains a character a shell would interpret; this unit substitutes it into a shell command line", key, value)
	}
	return nil
}
