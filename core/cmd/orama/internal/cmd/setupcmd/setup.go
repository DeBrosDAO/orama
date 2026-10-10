// Package setupcmd is `orama setup`: join an Orama network from fresh machines
// in one command. Run on a terminal with no flags it asks the questions
// (internal/setup/wizard); with flags and --yes it runs unattended. Both build the
// same plan and run the same steps (internal/setup).
package setupcmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// Cmd is the top-level "setup" command.
var Cmd = &cobra.Command{
	Use:   "setup [ip ...]",
	Short: "Join an Orama network: turn fresh VPSes into nodes",
	Long: `Turn fresh VPSes into nodes of an Orama network, in one command.

For each machine setup gives your RootWallet an SSH key (and pins the machine's host key),
checks the hardware against what the machine will run, installs the signed release of the
network's channel (verified against the release root the network pins), installs the cluster
node and, beside it, the global layer: the chain (it joins by state sync from two seeds that
must agree), public storage and its provider, and a Tor relay when you give the network's Tor
file. Then it registers your operator, each node, its bonds and its storage capacity on the
chain and creates your validator, signing every transaction with your RootWallet. Nodes are
restarted one at a time, each waiting until it carries its share of the cluster again.

The first machine creates the cluster; the others join it. Running setup again with more
addresses adds nodes to the same cluster, and a machine that already has a step does not get
it again, so a run that stopped can be run again as it was.

With no addresses and no --yes, on a terminal, setup asks for everything. With --yes it asks
nothing: give the addresses, --name, and a --host-key for each machine (the fingerprint your
provider's console shows; setup never trusts a host key it was not given).

The operator account needs ORAMA for the bonds and for the validator's 1,000 ORAMA self-bond.
On a network with a faucet and a node of it in your CLI configuration it is requested; otherwise
setup stops, says how much to send and to which address, and resumes when you run it again.

--cluster-only installs the cluster node alone (2 vCPU, 2 GiB, 10 GiB free). The full profile
needs 4 vCPU, 8 GiB and 80 GiB free plus the storage you offer. Nothing is installed on any
machine until every machine passes.`,
	Example: `  # Walk through it
  orama setup

  # Unattended: three machines, one cluster, host keys pinned
  orama setup --network stagenet --name alice --yes \
    --ip 203.0.113.10 --ip 203.0.113.11 --ip 203.0.113.12 \
    --host-key 203.0.113.10=SHA256:... --host-key 203.0.113.11=SHA256:... --host-key 203.0.113.12=SHA256:...

  # A cluster of your own, on your own domain, without the chain
  orama setup --cluster-only --domain cluster.example.org --yes --ip 203.0.113.10 --host-key SHA256:...`,
	Args: cobra.ArbitraryArgs,
	RunE: run,
}

var flags struct {
	network, name, user, bootstrapKey, domain, acmeCA, env, contact, torNetwork string
	ips, hostKeys                                                               []string
	clusterOnly, exit, yes, password, noValidator                               bool
	storageGB                                                                   uint64
	asn                                                                         uint32
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&flags.network, "network", "", "Network to join: a name from `orama network list` (default: the active network, or the only one)")
	f.StringSliceVar(&flags.ips, "ip", nil, "Public IPv4 address of a machine (repeatable; the addresses can also be given as arguments)")
	f.StringVar(&flags.name, "name", "", "Node name, the node's id on the chain; several machines are named <name>, <name>-2, ... (required unless --cluster-only)")
	f.BoolVar(&flags.clusterOnly, "cluster-only", false, "Install the cluster node only, without the chain, storage or relay")
	f.BoolVar(&flags.exit, "exit", false, "Make the relay an exit relay: other people's traffic leaves from your IP address. Needs --tor-network and --yes")
	f.Uint64Var(&flags.storageGB, "storage-gb", 0, fmt.Sprintf("Public storage each node offers, in GB (default %d); counts towards the disk floor", setup.DefaultStorageGB))
	f.BoolVarP(&flags.yes, "yes", "y", false, "Ask nothing: use the answers given as flags (every machine needs a --host-key)")
	f.StringVar(&flags.user, "user", setup.DefaultSSHUser, "SSH login on the machines")
	f.BoolVar(&flags.password, "password", false, "Log in with the password in your RootWallet vault login for the address (rw vault add <ip>), never from the command line")
	f.StringVar(&flags.bootstrapKey, "bootstrap-key", "", "A private key that opens the machines today (key-only images); used once to install the RootWallet key, never stored")
	f.StringArrayVar(&flags.hostKeys, "host-key", nil, "Expected SSH host-key fingerprint, SHA256:..., for a single machine or <ip>=SHA256:... for each (repeatable)")
	f.StringVar(&flags.domain, "domain", "", "Base domain of a cluster of your own: setup prints the NS and glue records to create, then waits until they resolve and the cluster has a certificate")
	f.StringVar(&flags.acmeCA, "acme-ca", "", "ACME directory for the cluster's certificates: letsencrypt, letsencrypt-staging or an https URL")
	f.StringVar(&flags.env, "env", "", "CLI environment to record the cluster under (default: the active one on this network, else <network>-<name>)")
	f.StringVar(&flags.contact, "contact", "", "Where an abuse complaint about the relay goes (default: your operator account)")
	f.Uint32Var(&flags.asn, "asn", 0, "Autonomous system number to declare for the nodes (default: looked up from the address; 0 leaves it undeclared)")
	f.StringVar(&flags.torNetwork, "tor-network", "", "The Orama Tor network's tor-network.json: with it each node also runs a relay")
	f.BoolVar(&flags.noValidator, "no-validator", false, "Do not create a validator (and do not bond the 1,000 ORAMA self-bond)")
}

func run(cmd *cobra.Command, args []string) error {
	opts, err := optionsFromFlags(cmd, args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	tty := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
	if wantWizard(opts, tty) {
		return runWizard(ctx, cmd, opts)
	}
	return runFlags(ctx, cmd, opts, tty)
}

// runFlags runs the setup the flags describe, printing its progress as lines.
func runFlags(ctx context.Context, cmd *cobra.Command, opts setup.Options, tty bool) error {
	if err := requireHostKeys(opts, tty); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if opts.Exit && !opts.Yes {
		if err := confirmExit(os.Stdin, out, &opts); err != nil {
			return err
		}
	}
	deps := setup.NewDeps(&setup.TextReporter{Out: out}, nil)
	if !opts.Yes {
		deps.Confirm = func(p *setup.Plan) (bool, error) { return askYesNo(os.Stdin, out, "Go ahead?") }
	}
	res, err := setup.Run(ctx, opts, deps)
	if err != nil {
		return err
	}
	printSummary(out, res)
	if tty && !opts.Yes {
		return openStatus(ctx, res.Env)
	}
	return nil
}

// confirmExit shows the warning of an exit relay and asks for it to be accepted.
func confirmExit(in io.Reader, out io.Writer, opts *setup.Options) error {
	fmt.Fprintf(out, "%s\n", setup.ExitWarning)
	ok, err := askYesNo(in, out, "Make the relay an exit relay?")
	if err != nil {
		return err
	}
	if !ok {
		return clierr.Aborted("the exit relay was not accepted: nothing was changed")
	}
	opts.ExitConfirmed = true
	return nil
}

// askYesNo asks a question on out and reads the answer from in. Anything but
// y or yes is no.
func askYesNo(in io.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", question)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		return false, fmt.Errorf("could not read the answer: %w (pass --yes to answer for it)", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// printSummary says what the run left behind and what is still to do.
func printSummary(out io.Writer, res *setup.Result) {
	fmt.Fprintf(out, "\nDone. The cluster is recorded as %q.\n", res.Env)
	for _, n := range res.Plan.Nodes {
		fmt.Fprintf(out, "  %s\n", n.IP)
	}
	if res.Operator != "" {
		fmt.Fprintf(out, "Operator account: %s\n", res.Operator)
	}
	for _, p := range res.Pending {
		fmt.Fprintf(out, "Still to do: %s\n", p)
	}
	fmt.Fprintf(out, "See how it is doing with: orama status --env %s\n", res.Env)
}

// openStatus runs `orama status` for the environment, on the terminal.
func openStatus(ctx context.Context, env string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this program to open orama status: %w", err)
	}
	c := exec.CommandContext(ctx, self, "status", "--env", env)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("orama status --env %s: %w", env, err)
	}
	return nil
}
