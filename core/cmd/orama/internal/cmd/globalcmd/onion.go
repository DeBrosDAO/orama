package globalcmd

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainonion"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onionnet"
	"github.com/spf13/cobra"
)

const (
	onionFlag        = "onion"
	onionSOCKSFlag   = "onion-socks"
	onionNetworkFlag = "onion-network"
	onionTorFlag     = "onion-tor"
	// OnionEnv, OnionSOCKSEnv and OnionNetworkEnv are the configuration form of
	// --onion, --onion-socks and --onion-network. A flag wins over its variable.
	OnionEnv        = "ORAMA_CHAIN_ONION"
	OnionSOCKSEnv   = "ORAMA_ONION_SOCKS"
	OnionNetworkEnv = onionnet.NetworkEnv
)

// AddOnionFlags registers --onion, --onion-socks, --onion-network and
// --onion-tor on a command that submits a chain transaction next to its --node
// flag.
func AddOnionFlags(f interface {
	String(name, value, usage string) *string
}) {
	f.String(onionFlag, "", "Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($"+OnionEnv+")")
	f.String(onionSOCKSFlag, "", "Tor SOCKS5 address for --onion, a loopback host:port (default "+chainonion.DefaultSOCKS+", $"+OnionSOCKSEnv+")")
	f.String(onionNetworkFlag, "", "Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($"+OnionNetworkEnv+")")
	f.String(onionTorFlag, onionnet.DefaultTorBinary, "The tor binary --onion-network starts")
}

// flagOrEnv is the flag's value, else the variable's.
func flagOrEnv(cmd *cobra.Command, name, env string) string {
	if f := cmd.Flags().Lookup(name); f != nil && f.Value.String() != "" {
		return f.Value.String()
	}
	return os.Getenv(env)
}

// chainTarget resolves where the chain API is reached. With --onion or
// --onion-network (or their variables) the base is an onion service and ctx
// carries a Tor-only HTTP client with a circuit of its own for this
// transaction; --node with either is a usage error, because two routes would
// leave the clearnet one a silent fallback. Otherwise it returns node
// unchanged. done stops a Tor client this call started; the caller defers it.
func chainTarget(cmd *cobra.Command, ctx context.Context, node string) (_ context.Context, base string, done func(), err error) {
	done = func() {}
	onion := flagOrEnv(cmd, onionFlag, OnionEnv)
	netFile := flagOrEnv(cmd, onionNetworkFlag, OnionNetworkEnv)
	if onion == "" && netFile == "" {
		return ctx, node, done, nil
	}
	if node != "" {
		return ctx, "", done, clierr.Usage("--node and --onion are two routes to the chain; pass one")
	}
	socks := flagOrEnv(cmd, onionSOCKSFlag, OnionSOCKSEnv)
	if netFile != "" {
		if socks != "" {
			return ctx, "", done, clierr.Usage("--onion-socks and --onion-network are two Tor clients; pass one")
		}
		if onion, socks, done, err = startNetworkTor(cmd, ctx, netFile, onion); err != nil {
			return ctx, "", done, err
		}
	}
	base, client, err := onionClient(onion, socks)
	if err != nil {
		done()
		return ctx, "", func() {}, err
	}
	return clusterreg.WithHTTPClient(ctx, client), base, done, nil
}

// onionClient validates the onion address and the SOCKS address and returns the
// base URL and an HTTP client whose only route is that SOCKS proxy, under one
// new circuit-isolation credential.
func onionClient(onion, socks string) (string, *http.Client, error) {
	base, err := chainonion.Base(onion)
	if err != nil {
		return "", nil, clierr.Usage("--onion: %v", err)
	}
	if socks != "" {
		if err := chainonion.ValidateSOCKS(socks); err != nil {
			return "", nil, clierr.Usage("--onion-socks: %v", err)
		}
	}
	client, err := chainonion.NewClient(socks)
	if err != nil {
		return "", nil, clierr.Failure("%v", err)
	}
	return base, client, nil
}

// startNetworkTor loads the network file, picks a validator onion when none
// was given, and starts a tor client for the network on a private loopback
// port. It returns the onion to submit to, the tor's SOCKS address and the
// function that stops it.
func startNetworkTor(cmd *cobra.Command, ctx context.Context, netFile, onion string) (string, string, func(), error) {
	noop := func() {}
	network, err := onionnet.Load(netFile)
	if err != nil {
		return "", "", noop, clierr.Usage("--onion-network: %v", err)
	}
	if onion == "" {
		if onion, err = network.RandomValidatorOnion(); err != nil {
			return "", "", noop, clierr.Usage("--onion-network: %v", err)
		}
	}
	if _, err := chainonion.Base(onion); err != nil {
		return "", "", noop, clierr.Usage("--onion: %v", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Joining the %s Tor network (this can take a minute)...\n", network.Name)
	tor, err := onionnet.StartOnFreePort(ctx, flagString(cmd, onionTorFlag, onionnet.DefaultTorBinary), network, "", nil)
	if err != nil {
		return "", "", noop, clierr.Unavailable("the %s Tor network could not be joined, and the transaction was not sent: %v", network.Name, err)
	}
	return onion, tor.SocksAddr, func() { _ = tor.Stop() }, nil
}

func flagString(cmd *cobra.Command, name, fallback string) string {
	if f := cmd.Flags().Lookup(name); f != nil && f.Value.String() != "" {
		return f.Value.String()
	}
	return fallback
}
