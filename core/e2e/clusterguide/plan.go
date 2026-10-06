package clusterguide

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Defaults for the waits the guide tells a reader to do by hand.
const (
	// DefaultDelegationWait is how long a run waits for the NS records and the
	// genesis certificate. DNS at a registrar can take much longer; a sandbox
	// domain whose glue already exists answers at once.
	DefaultDelegationWait = 20 * time.Minute
	delegationPollEvery   = 15 * time.Second
)

func skipIfUseOnly(fx *Fixture) string {
	if fx.UseOnly {
		return "the cluster is already up (use-only run)"
	}
	return ""
}

// Plan is every command of the guide, in order, and how to carry each out.
// It has to match docs/RUN_YOUR_OWN_CLUSTER.md exactly (Runner.Verify): the
// Install section, "Use it" and "Check it".
func Plan() []Step {
	setup := func(name string, need, deny []string) Step {
		return Step{
			Name: name, Section: SectionInstall, Words: []string{"orama", "node", "setup"},
			Need: append([]string{"--ip", "--password", "--env", "--archive", "--base-domain", "--role", "--acme-ca"}, need...),
			Deny: deny, Skip: skipIfUseOnly, Extra: hostKeyArg,
		}
	}
	delegation := Step{
		Name: "print the delegation records", Section: SectionInstall,
		Words: []string{"orama", "node", "dns", "delegation"}, Need: []string{"--env"}, Deny: []string{"--cloudflare-token-file"},
		Skip: skipIfUseOnly, Check: mentionsDomain,
	}
	return []Step{
		{Name: "store the VPS login", Section: SectionInstall, Words: []string{"rw", "vault", "add"}, Kind: Provided},
		{Name: "build the archive", Section: SectionInstall, Words: []string{"orama", "build"}, Kind: Provided},
		setup("install the genesis node", []string{"--genesis"}, nil),
		delegation,
		{
			Name: "write the delegation at Cloudflare", Section: SectionInstall,
			Words: []string{"orama", "node", "dns", "delegation"}, Need: []string{"--env", "--cloudflare-token-file"},
			Skip: func(fx *Fixture) string {
				if r := skipIfUseOnly(fx); r != "" {
					return r
				}
				if fx.TokenFile == "" {
					return "no Cloudflare token: the delegation must already exist"
				}
				return ""
			},
			After: awaitDelegation,
		},
		setup("join the second node", []string{"--join-via"}, []string{"--genesis"}),
		setup("join the third node", []string{"--join-via"}, []string{"--genesis"}),
		delegation,
		{Name: "use the environment", Section: SectionUse, Words: []string{"orama", "env", "use"}},
		{Name: "sign in", Section: SectionUse, Words: []string{"orama", "auth", "login"}, Deny: []string{"--namespace"}},
		{Name: "create a namespace", Section: SectionUse, Words: []string{"orama", "namespace", "create"}},
		{Name: "sign in to the namespace", Section: SectionUse, Words: []string{"orama", "auth", "login"}, Need: []string{"--namespace"}},
		{Name: "deploy a static site", Section: SectionUse, Words: []string{"orama", "deploy", "static"}, Need: []string{"--name"}},
		{
			Name: "every node is healthy", Section: SectionCheck, Words: []string{"orama", "status"}, Need: []string{"--env"},
			Extra: func(context.Context, *Fixture, []string) ([]string, error) { return []string{"--json"}, nil },
			Check: allNodesHealthy,
		},
		{Name: "the deployment is listed", Section: SectionCheck, Words: []string{"orama", "app", "list"}, Check: listsDeployment("www")},
	}
}

// hostKeyArg is the --host-key the guide leaves to a reader to confirm at the
// prompt. The harness cannot answer a prompt, so it reads the fingerprint the
// machine presents now, from a machine the operator has just provisioned.
func hostKeyArg(_ context.Context, fx *Fixture, argv []string) ([]string, error) {
	ip := flagValue(argv, "--ip")
	if ip == "" {
		return nil, fmt.Errorf("the command has no --ip")
	}
	fp, err := fx.HostKey(ip)
	if err != nil {
		return nil, fmt.Errorf("read the SSH host key of %s: %w", ip, err)
	}
	return []string{"--host-key", fp}, nil
}

func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

func mentionsDomain(fx *Fixture, out string) error {
	if !strings.Contains(out, fx.BaseDomain) {
		return fmt.Errorf("the delegation output never names the base domain %s", fx.BaseDomain)
	}
	return nil
}

// awaitDelegation is the guide's "wait until dig NS lists the nameservers and
// the genesis certificate has been issued", run after the records exist.
func awaitDelegation(ctx context.Context, fx *Fixture) error {
	wait := fx.DelegationWait
	if wait <= 0 {
		wait = DefaultDelegationWait
	}
	sleep := fx.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	deadline := time.Now().Add(wait)
	var last error
	for {
		if last = delegationReady(fx); last == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for the delegation: %w (last: %v)", err, last)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the delegation did not resolve within %s: %w", wait, last)
		}
		sleep(delegationPollEvery)
	}
}

func delegationReady(fx *Fixture) error {
	ns, err := fx.LookupNS(fx.BaseDomain)
	if err != nil {
		return fmt.Errorf("NS lookup for %s: %w", fx.BaseDomain, err)
	}
	if len(ns) == 0 {
		return fmt.Errorf("the internet has no NS records for %s yet", fx.BaseDomain)
	}
	if err := fx.CertServed(fx.BaseDomain); err != nil {
		return fmt.Errorf("the genesis certificate is not served yet: %w", err)
	}
	return nil
}

// allNodesHealthy reads `orama status --json`: one entry per node, every one
// healthy, and at least the guide's three.
func allNodesHealthy(fx *Fixture, out string) error {
	var nodes []struct {
		Host   string `json:"host"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	start := strings.Index(out, "[")
	if start < 0 {
		return fmt.Errorf("orama status --json printed no node list")
	}
	if err := json.Unmarshal([]byte(out[start:]), &nodes); err != nil {
		return fmt.Errorf("orama status --json did not print a node list: %w", err)
	}
	want := MinNodes
	if !fx.UseOnly {
		want = len(fx.IPs)
	}
	if len(nodes) < want {
		return fmt.Errorf("status lists %d nodes, want at least %d", len(nodes), want)
	}
	for _, n := range nodes {
		if n.Status != "healthy" {
			return fmt.Errorf("node %s is %s: %s", n.Host, n.Status, n.Error)
		}
	}
	return nil
}

func listsDeployment(name string) func(*Fixture, string) error {
	return func(_ *Fixture, out string) error {
		if !strings.Contains(out, name) {
			return fmt.Errorf("the deployment %q is not in the list", name)
		}
		return nil
	}
}

// uncoveredSections are guide sections whose commands are not part of the
// path from a fresh install to a working cluster, with why. A command in any
// other section that Plan does not list fails the run.
var uncoveredSections = map[string]string{
	"A sealed backup": "an operation on a running cluster, not part of standing one up",
}

// CoveredCommands are the guide's commands the plan must match: everything
// outside uncoveredSections. A guide with a command in an unknown section, or
// in a section Plan has no step for, is refused by Runner.Verify.
func CoveredCommands(cmds []Command) []Command {
	var out []Command
	for _, c := range cmds {
		if _, skip := uncoveredSections[c.Section]; !skip {
			out = append(out, c)
		}
	}
	return out
}
