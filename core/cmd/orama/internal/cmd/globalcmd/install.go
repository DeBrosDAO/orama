package globalcmd

import (
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

// defaultSSHPort is the port --enable-firewall allows before enabling ufw.
const defaultSSHPort = 22

var installFlags struct {
	services       []string
	stagedDir      string
	peers          string
	initChain      bool
	chainID        string
	moniker        string
	genesis        string
	enableFirewall bool
	sshPort        int
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the global services on this node (run as root)",
	Long: `Install global services on this machine: chain, and optionally provider,
archiver or repair. The chain is required: the other services reach it only on
this host's loopback RPC. provider and repair are never installed together.

For each service it creates the service's system account, copies its binaries
(oramad and this orama CLI for the chain, whose unit runs 'orama global
validator check-sign-floor' before every start; orama-global for the others)
from --staged-dir into /usr/lib/orama-global/bin
(root-owned, 0755; a symlink in the staged directory is refused, and as root the
directory must be root's and not writable by others), writes and enables its
orama-global-* unit, and opens its public port in ufw (31000 tcp+udp for the
chain, 31013 tcp for the provider) with the comment orama-global. It does not
start anything: 'orama global start' does, chain first.

The chain unit runs oramad directly. cosmovisor is not installed: no cosmovisor
release is pinned. A new chain binary is installed by running this command again
with the new oramad staged, then 'orama global restart chain'.

--init-chain creates the chain home with 'oramad init' as orama-chain and puts
the network's --genesis in place. It is never done without the flag, and it is
refused when the home already has a genesis.

An inactive ufw is refused unless --enable-firewall is given; then incoming is
denied by default, --ssh-port is allowed, and ufw is enabled; --ssh-port must
be a port 'sshd -T' reports, or nothing is changed. Running the
command again with the same flags changes nothing but the binaries' bytes.`,
	Args: cobra.NoArgs,
	RunE: runInstall,
}

func init() {
	f := installCmd.Flags()
	f.StringSliceVar(&installFlags.services, "services", nil, "Services: chain[,provider,archiver,repair] [required]")
	f.StringVar(&installFlags.stagedDir, "staged-dir", "", "Directory holding the release's oramad and orama-global [required]")
	f.StringVar(&installFlags.peers, "persistent-peers", "", "Chain peers, id@host:port,... (written into the chain unit)")
	f.BoolVar(&installFlags.initChain, "init-chain", false, "Create the chain home with oramad init and install --genesis")
	f.StringVar(&installFlags.chainID, "chain-id", "", "Chain id, with --init-chain")
	f.StringVar(&installFlags.moniker, "moniker", "", "Node moniker, with --init-chain")
	f.StringVar(&installFlags.genesis, "genesis", "", "The network's genesis.json, with --init-chain")
	f.BoolVar(&installFlags.enableFirewall, "enable-firewall", false, "Enable an inactive ufw (deny incoming, allow --ssh-port)")
	f.IntVar(&installFlags.sshPort, "ssh-port", defaultSSHPort, "SSH port --enable-firewall allows")
	Cmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, _ []string) error {
	services, err := install.ParseGlobalServices(installFlags.services)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	opts := install.GlobalInstallOptions{
		Services: services, StagedDir: installFlags.stagedDir, PersistentPeers: installFlags.peers,
		EnableFirewall: installFlags.enableFirewall, SSHPort: installFlags.sshPort,
	}
	if installFlags.initChain {
		opts.InitChain = &install.ChainInit{
			ChainID: installFlags.chainID, Moniker: installFlags.moniker, GenesisPath: installFlags.genesis,
		}
	} else if installFlags.chainID != "" || installFlags.moniker != "" || installFlags.genesis != "" {
		return clierr.Usage("--chain-id, --moniker and --genesis only apply with --init-chain")
	}
	if err := clierr.RequireRoot("installing the global services"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	host := install.DefaultGlobalHost(func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) })
	if err := install.InstallGlobal(opts, host); err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(out, "installed %v; start them with: orama global start\n", installFlags.services)
	return nil
}
