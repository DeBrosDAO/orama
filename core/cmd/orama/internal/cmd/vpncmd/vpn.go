// Package vpncmd is the VPN client for an Orama Tor network: `orama maint vpn up`
// runs a tor client on the network and offers its SOCKS5 proxy on loopback, and
// `orama maint vpn check` joins the network and proves a circuit reaches a validator
// onion service. Plans: track E, E6.
package vpncmd

import (
	"context"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/onionnet"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

const (
	networkFlag = "network"
	torFlag     = "tor"
	dataDirFlag = "data-dir"
	// NetworkEnv is the configuration form of --network, shared with
	// onion transaction submission (--onion-network).
	NetworkEnv = tornet.NetworkEnv
	// DefaultSOCKS is where `orama maint vpn up` offers the proxy: Tor Browser's port,
	// not 9050, which a node's own Tor client uses.
	DefaultSOCKS = "127.0.0.1:9150"
)

// Cmd is the vpn command group.
var Cmd = New()

// New builds the vpn command group. Each call returns a tree of its own, so a
// test never inherits the flags or context of an earlier run.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vpn",
		Short: "Route traffic through an Orama Tor network",
		Long: `Join an Orama Tor network from this machine.

A network is described by its tor-network.json file: the directory authorities
and, optionally, the validator onion services it lists. up starts an unmodified
upstream tor on it and offers a SOCKS5 proxy on loopback; check joins the
network and proves a circuit reaches a validator's onion service.

The client has one route: the tor it starts, configured with the network's
authorities and no others. It never falls back to the public Tor network or to
a direct connection. When tor stops, the proxy port closes and whatever was
using it fails; nothing is routed around it. This is a proxy, not a system-wide
tunnel: only applications pointed at the SOCKS port, with names resolved by the
proxy (socks5h), use the network.

Only a private network can be joined: the public Orama network is not launched.`,
	}
	cmd.AddCommand(newUpCmd(), newCheckCmd())
	return cmd
}

type common struct {
	network, tor, dataDir string
}

func (c *common) addFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.network, networkFlag, "", "Orama Tor network file (tor-network.json) [required] ($"+NetworkEnv+")")
	f.StringVar(&c.tor, torFlag, onionnet.DefaultTorBinary, "The tor binary to run")
	f.StringVar(&c.dataDir, dataDirFlag, "", "Tor state directory (default: the user cache directory, per network)")
}

// load reads the network file and fills in the tor options with socks and dns.
func (c *common) load(socks, dns string) (tornet.Network, onionnet.Options, error) {
	path := c.network
	if path == "" {
		path = os.Getenv(NetworkEnv)
	}
	if path == "" {
		return tornet.Network{}, onionnet.Options{}, clierr.Usage("--network is required: the Orama Tor network file")
	}
	n, err := tornet.Load(path)
	if err != nil {
		return tornet.Network{}, onionnet.Options{}, clierr.Usage("%v", err)
	}
	dir := c.dataDir
	if dir == "" {
		if dir, err = onionnet.DefaultDataDir(n.Name); err != nil {
			return n, onionnet.Options{}, clierr.Failure("%v", err)
		}
	}
	return n, onionnet.Options{DataDir: dir, SocksAddr: socks, DNSAddr: dns}, nil
}

// join starts tor on the network and reports progress on stderr.
func join(ctx context.Context, cmd *cobra.Command, bin string, n tornet.Network, o onionnet.Options) (*onionnet.Tor, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "Joining the %s Tor network (this can take a minute)...\n", n.Name)
	tor, err := onionnet.Start(ctx, bin, n, o, nil)
	if err != nil {
		return nil, clierr.Unavailable("the %s Tor network could not be joined: %v", n.Name, err)
	}
	return tor, nil
}
