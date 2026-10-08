package globalcmd

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

// bytesPerGB is the decimal gigabyte Kubo's StorageMax counts in.
const bytesPerGB = 1_000_000_000

// defaultSSHPort is the port --enable-firewall allows before enabling ufw.
const defaultSSHPort = 22

var installFlags struct {
	services         []string
	stagedDir        string
	publicStorageGB  uint64
	peers            string
	initChain        bool
	chainID          string
	moniker          string
	genesis          string
	enableFirewall   bool
	sshPort          int
	colocated        bool
	chainClientUsers []string
	torAddress       string
	torContact       string
	torNodeID        string
	torBandwidthMbit uint
	torFamily        []string
	torAuthorityKeys string
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the global services on this node (run as root)",
	Long: `Install global services on this machine: chain, and optionally ipfs,
provider, archiver, indexer, repair, and the roles of the Orama Tor network
(dirauth, relay, relay,exit, onion). The chain is required unless the machine
only runs dirauth or relay: the other services reach it only on this host's
loopback RPC. provider needs ipfs beside
it (it pins public deals through the public Kubo). provider and repair are
never installed together. indexer is optional: it serves the chain read API on
loopback for a node that runs an RPC or index endpoint.

For each service it creates the service's system account, copies its binaries
(oramad and this orama CLI for the chain, whose unit runs 'orama global
validator check-sign-floor' before every start; ipfs, Kubo v0.43.1, for ipfs;
orama-global for the others) from --staged-dir into /usr/lib/orama-global/bin
(root-owned, 0755; a symlink in the staged directory is refused, and as root the
directory must be root's and not writable by others), writes and enables its
orama-global-* unit, and opens its public port in ufw (31000 tcp+udp for the
chain, 31010 tcp+udp for the public Kubo swarm, 31013 tcp for the provider)
with the comment orama-global. It does not start anything: 'orama global start'
does, chain first.

The chain unit runs oramad under cosmovisor v1.7.3. Stage the official
cosmovisor-v1.7.3-linux-<amd64|arm64>.tar.gz beside the other binaries: its
SHA-256 must equal the pin built into this CLI, and only its cosmovisor file is
installed. oramad itself is placed in the chain home's cosmovisor layout as the
genesis binary, so the chain home must already have a genesis (--init-chain, or
an existing home). A binary already staged there with different bytes is
refused: change the chain binary with 'orama global stage-oramad --upgrade'.

The ipfs service is a public Kubo of its own: no swarm.key, its own repo in
/var/lib/orama-global/ipfs, swarm on 31010, RPC on 127.0.0.1:31011 (198.18.0.2:31011 with --colocated) behind a
token only the provider's group can read, and a GC timer. --public-storage-gb
is the capacity you will declare with 'orama global capacity'; Kubo's
StorageMax is that plus 10%. It never touches a private cluster's Kubo.

--init-chain creates the chain home with 'oramad init' as orama-chain and puts
the network's --genesis in place. It is never done without the flag, and it is
refused when the home already has a genesis.

An inactive ufw is refused unless --enable-firewall is given; then incoming is
denied by default, --ssh-port is allowed, and ufw is enabled; --ssh-port must
be a port 'sshd -T' reports, or nothing is changed. Running the
command again with the same flags changes nothing but the binaries' bytes.

The roles of the Orama Tor network (docs/TOR_NETWORK.md) run the distro's tor,
installed from the Tor Project's repository, with a torrc this command writes
from the network's tor-network.json, staged beside the binaries; the network file
is checked before anything on the host changes.
  relay           a relay (ORPort 31020/tcp). --tor-address, --tor-contact and
                  --tor-node-id are required; the nickname is derived from the
                  node id.
  relay,exit      the same relay as an exit, under a reduced exit policy. Opt-in:
                  the network file must say allow_exit. Destinations in
                  /var/lib/orama-global/tor-exit-reject (one CIDR or address, with
                  an optional :port, per line) are refused first.
  dirauth         a directory authority (ORPort 31020/tcp, DirPort 31021/tcp). It
                  is a relay already, so it never goes beside relay. It needs
                  --tor-address to be one of the network's authorities and
                  --tor-authority-keys, its bundle from 'orama global tor
                  ceremony'; a bundle that is not this authority's is refused.
                  A dirauth or relay host needs no chain.
  onion           the validator's onion service, forwarding to a tx gate on
                  loopback that serves only account read, broadcast and tx lookup.
                  It publishes no port and needs the chain.

--colocated installs the services on a machine that already runs a cluster node
(orama node setup first). The global units run in their own network namespace,
orama-global, joined to the root namespace by a veth pair (198.18.0.0/30): they
have their own loopback and port space, cannot reach the cluster's loopback,
WireGuard mesh or any private network, and only the ports they publish are
forwarded in. It writes orama-global-netns.service, two nftables rulesets and a
resolv.conf under /etc/orama-global, and records role both in preferences.yaml.
The machine must have iproute2, nftables, a kernel with network namespaces and
veth, and systemd 242 or newer; otherwise nothing is changed. A machine that is
co-located must keep using --colocated on later installs.

On a co-located machine the chain's RPC and REST API (and the indexer) are
reachable on the namespace address only by root and the cluster node's account.
--chain-client-user <name> (repeatable) also allows a local account, for example
the ssh login that tunnels to the chain or runs 'orama chain'; an unknown account
refuses the install, and the set is kept by later installs.`,
	Args: cobra.NoArgs,
	RunE: runInstall,
}

func init() {
	f := installCmd.Flags()
	f.StringSliceVar(&installFlags.services, "services", nil, "Services: chain[,ipfs,provider,archiver,indexer,repair,dirauth,relay,exit,onion] [required]")
	f.StringVar(&installFlags.stagedDir, "staged-dir", "", "Directory holding the release's oramad, orama, orama-global, ipfs and the cosmovisor tarball [required]")
	f.Uint64Var(&installFlags.publicStorageGB, "public-storage-gb", 0, "Capacity in GB you will declare for the provider; sizes the public Kubo (required with ipfs)")
	f.StringVar(&installFlags.peers, "persistent-peers", "", "Chain peers, id@host:port,... (written into the chain unit)")
	f.BoolVar(&installFlags.initChain, "init-chain", false, "Create the chain home with oramad init and install --genesis")
	f.StringVar(&installFlags.chainID, "chain-id", "", "Chain id, with --init-chain")
	f.StringVar(&installFlags.moniker, "moniker", "", "Node moniker, with --init-chain")
	f.StringVar(&installFlags.genesis, "genesis", "", "The network's genesis.json, with --init-chain")
	f.BoolVar(&installFlags.enableFirewall, "enable-firewall", false, "Enable an inactive ufw (deny incoming, allow --ssh-port)")
	f.IntVar(&installFlags.sshPort, "ssh-port", defaultSSHPort, "SSH port --enable-firewall allows")
	f.StringSliceVar(&installFlags.chainClientUsers, "chain-client-user", nil, "With --colocated: a local account, besides root and the cluster node's, allowed to connect to the chain's RPC and REST ports on the namespace address (repeatable; kept by later installs)")
	f.StringVar(&installFlags.torAddress, "tor-address", "", "dirauth, relay: the public IPv4 address the relay publishes")
	f.StringVar(&installFlags.torContact, "tor-contact", "", "dirauth, relay: ContactInfo published in the descriptor (the operator, and where an abuse complaint goes)")
	f.StringVar(&installFlags.torNodeID, "tor-node-id", "", "relay: the on-chain node id the relay's nickname is derived from")
	f.UintVar(&installFlags.torBandwidthMbit, "tor-bandwidth-mbit", 0, "dirauth, relay: limit on what the relay carries for others, in Mbit/s each way (0 = unlimited)")
	f.StringSliceVar(&installFlags.torFamily, "tor-family", nil, "dirauth, relay: the RSA fingerprints of the operator's other relays")
	f.StringVar(&installFlags.torAuthorityKeys, "tor-authority-keys", "", "dirauth: the authority's key bundle from 'orama global tor ceremony' (deploy/<nickname>)")
	f.BoolVar(&installFlags.colocated, "colocated", false, "Run the services in their own network namespace on a machine that also runs a cluster node")
	Cmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, _ []string) error {
	services, exit, err := install.ParseGlobalRoles(installFlags.services)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	opts := install.GlobalInstallOptions{
		Services: services, StagedDir: installFlags.stagedDir, PersistentPeers: installFlags.peers,
		EnableFirewall: installFlags.enableFirewall, SSHPort: installFlags.sshPort, Colocated: installFlags.colocated,
		PublicStorageBytes: installFlags.publicStorageGB * bytesPerGB,
		ChainClientUsers:   installFlags.chainClientUsers,
		Tor: install.TorOptions{
			Exit: exit, Address: installFlags.torAddress, Contact: installFlags.torContact, NodeID: installFlags.torNodeID,
			BandwidthMbit: installFlags.torBandwidthMbit, Family: installFlags.torFamily, DirauthKeysDir: installFlags.torAuthorityKeys,
		},
	}
	if err := checkChainClientUsers(opts.ChainClientUsers, opts.Colocated); err != nil {
		return err
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
	warnIfMigratedAway(cmd)
	return nil
}

// checkChainClientUsers refuses an empty --chain-client-user value, and the flag without --colocated.
func checkChainClientUsers(users []string, colocated bool) error {
	for _, u := range users {
		if strings.TrimSpace(u) == "" {
			return clierr.Usage("--chain-client-user needs an account name, got an empty value")
		}
	}
	if len(users) > 0 && !colocated {
		return clierr.Usage("--chain-client-user only applies with --colocated")
	}
	return nil
}
