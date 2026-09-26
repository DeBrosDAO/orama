package node

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/spf13/cobra"
)

var setupOpts setup.Options

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up a fresh VPS as an Orama node",
	Long: `Bootstrap a fresh VPS into a running Orama node in one command.

Creates an SSH key in rootwallet, installs it on the VPS, uploads the binary
archive, and runs the node install. For the first node, use --genesis to
create a new cluster.

Examples:
  # Genesis node (first node, creates new cluster)
  orama node setup --ip 1.2.3.4 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network --role nameserver --genesis

  # Join existing cluster
  orama node setup --ip 5.6.7.8 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network

  # Key-only VPS (no password login): install the RootWallet key once
  # with the key that opens it today
  orama node setup --ip 5.6.7.8 --user ubuntu --bootstrap-key ~/.ssh/id_ed25519 \
    --env devnet --base-domain orama-devnet.network

  # Join as nameserver
  orama node setup --ip 9.10.11.12 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network --role nameserver`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return setup.Run(setupOpts)
	},
}

func init() {
	setupCmd.Flags().StringVar(&setupOpts.IP, "ip", "", "Public IP address of the VPS (required)")
	setupCmd.Flags().StringVar(&setupOpts.Env, "env", "", "Target environment (default: active)")
	setupCmd.Flags().StringVar(&setupOpts.Role, "role", "node", "Node role: node or nameserver")
	setupCmd.Flags().StringVar(&setupOpts.User, "user", "root", "SSH user on the VPS")
	setupCmd.Flags().BoolVar(&setupOpts.UsePassword, "password", false, "Bootstrap over password login; the password is read from your RootWallet vault login for the IP (rw vault add <ip>), never from the command line")
	setupCmd.Flags().StringVar(&setupOpts.BaseDomain, "base-domain", "", "Base domain for the network")
	setupCmd.Flags().StringVar(&setupOpts.Gateway, "gateway", "", "Gateway URL of the cluster to join (default: the environment's): its domain, e.g. https://orama-devnet.network; the invite is minted through one of its nodes and pins that node's certificate")
	setupCmd.Flags().BoolVar(&setupOpts.Genesis, "genesis", false, "Create a new cluster (first node)")
	setupCmd.Flags().StringVar(&setupOpts.HostKey, "host-key", "", "Expected SSH host-key fingerprint (SHA256:...) of the VPS; omit to confirm it interactively")
	setupCmd.Flags().StringVar(&setupOpts.BootstrapKey, "bootstrap-key", "", "SSH private key that opens the VPS today (key-only images, e.g. --user ubuntu); used once to install the RootWallet key, never stored")
	setupCmd.Flags().StringVar(&setupOpts.Archive, "archive", "", "Build archive to install — the path `orama build` printed [required]; a node already running this exact build is not re-uploaded")
	setupCmd.Flags().StringVar(&setupOpts.JoinVia, "join-via", "", "user@ip of a node already in the cluster; the invite is minted there over SSH (no 'orama auth login' needed)")
	setupCmd.Flags().StringVar(&setupOpts.ACMECA, "acme-ca", "", "ACME directory for the node's TLS certificates (passed to node install): an https URL or letsencrypt-staging")
	setupCmd.MarkFlagRequired("ip")
}
