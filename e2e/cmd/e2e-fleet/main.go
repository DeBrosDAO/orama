// Command e2e-fleet provisions a disposable Orama fleet, runs the feature
// packages against it stage by stage, collects artifacts, writes the report,
// and always tears the fleet down.
//
//	e2e-fleet run [--keep-on-fail] [--bug-drafts]
//	e2e-fleet provision
//	e2e-fleet test [--stage N] [--resume]
//	e2e-fleet teardown
//	e2e-fleet sweep [--max-age 6h] [--force]
//	e2e-fleet report [--bug-drafts]
//	e2e-fleet coverage [--json]
//	e2e-fleet hook destroy <host> | break <host> | provision [--name N] [--location L]
//
// Secrets (HCLOUD_TOKEN, CF_API_TOKEN, CF_ZONE) come from the environment that
// `infisical run` builds; the runner prints their names when missing and never
// their values. `run` and `test` re-execute themselves with the secret
// variables removed from their environ (see seal.go) and hand the cloud
// credentials to a broker child over a pipe (see brokerchild.go).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// Exit codes.
const (
	exitOK         = 0
	exitFail       = 1
	exitUsage      = 2
	exitIncomplete = 3
)

// errUsage marks a command-line mistake.
var errUsage = errors.New("usage")

type command struct {
	summary string
	run     func(ctx context.Context, args []string) (int, error)
}

func commands() map[string]command {
	return map[string]command{
		"run":       {"provision, test every stage, collect, report, always tear down", cmdRun},
		"provision": {"preflight and provision a fleet; prints the state path", cmdProvision},
		"test":      {"run stages against the fleet in " + "E2E_FLEET_STATE", cmdTest},
		"teardown":  {"tear down the fleet in E2E_FLEET_STATE", cmdTeardown},
		"sweep":     {"delete every e2e resource older than --max-age", cmdSweep},
		"report":    {"rebuild the report from the artifact dir", cmdReport},
		"coverage":  {"run the coverage gate (no servers)", cmdCoverage},
		"hook":      {"lifecycle hooks: destroy <host> | break <host> | provision", cmdHook},
		// broker-serve is started by run and test, never by hand.
		cmdBrokerServeName: {"internal: the broker child `run` and `test` start (credentials on fd 3)", cmdBrokerServe},
	}
}

func main() {
	os.Exit(dispatch(context.Background(), os.Args[1:]))
}

func dispatch(ctx context.Context, args []string) int {
	cmds := commands()
	if len(args) == 0 {
		usage(cmds)
		return exitUsage
	}
	c, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "e2e-fleet: unknown command %q\n", args[0])
		usage(cmds)
		return exitUsage
	}
	if err := sealProcess(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "e2e-fleet:", err)
		return exitFail
	}
	code, err := c.run(ctx, args[1:])
	if errors.Is(err, errUsage) {
		fmt.Fprintln(os.Stderr, "e2e-fleet:", redactText(err.Error()))
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e-fleet:", redactText(err.Error()))
		if code == exitOK {
			code = exitFail
		}
	}
	return code
}

func usage(cmds map[string]command) {
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("usage: e2e-fleet <command> [flags]\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %-10s %s\n", n, cmds[n].summary)
	}
	fmt.Fprint(os.Stderr, b.String())
}

// stdLogger is provision.Logger on stdout, redacted.
type stdLogger struct{}

func (stdLogger) Infof(format string, args ...any) {
	fmt.Fprintln(os.Stdout, "e2e-fleet:", redactText(fmt.Sprintf(format, args...)))
}

// redactText masks the secret environment (sealed or not), the run's token
// registry when E2E_FLEET_STATE names the run, and every credential shape,
// in text the runner prints.
func redactText(s string) string {
	if path, ok := os.LookupEnv(config.EnvState); ok && path != "" {
		red, err := secrets.ForRun(secrets.LookupEnv, path)
		if err != nil {
			return secrets.Withheld
		}
		return red.Redact(s)
	}
	return secrets.FromEnv(secrets.LookupEnv).Redact(s)
}
