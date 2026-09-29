package globalcmd

import (
	"context"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainonion"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

const (
	onionFlag      = "onion"
	onionSOCKSFlag = "onion-socks"
	// OnionEnv and OnionSOCKSEnv are the configuration form of --onion and
	// --onion-socks. A flag wins over its variable.
	OnionEnv      = "ORAMA_CHAIN_ONION"
	OnionSOCKSEnv = "ORAMA_ONION_SOCKS"
)

// AddOnionFlags registers --onion and --onion-socks on a command that submits
// a chain transaction next to its --node flag.
func AddOnionFlags(f interface {
	String(name, value, usage string) *string
}) {
	f.String(onionFlag, "", "Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($"+OnionEnv+")")
	f.String(onionSOCKSFlag, "", "Tor SOCKS5 address for --onion, a loopback host:port (default "+chainonion.DefaultSOCKS+", $"+OnionSOCKSEnv+")")
}

// flagOrEnv is the flag's value, else the variable's.
func flagOrEnv(cmd *cobra.Command, name, env string) string {
	if f := cmd.Flags().Lookup(name); f != nil && f.Value.String() != "" {
		return f.Value.String()
	}
	return os.Getenv(env)
}

// chainTarget resolves where the chain API is reached. With --onion (or its
// variable) the base is the onion service and ctx carries a Tor-only HTTP
// client with a circuit of its own for this transaction; --node with it is a
// usage error, because two routes would leave the clearnet one a silent
// fallback. Otherwise it returns node unchanged.
func chainTarget(cmd *cobra.Command, ctx context.Context, node string) (context.Context, string, error) {
	onion := flagOrEnv(cmd, onionFlag, OnionEnv)
	if onion == "" {
		return ctx, node, nil
	}
	if node != "" {
		return ctx, "", clierr.Usage("--node and --onion are two routes to the chain; pass one")
	}
	base, err := chainonion.Base(onion)
	if err != nil {
		return ctx, "", clierr.Usage("--onion: %v", err)
	}
	socks := flagOrEnv(cmd, onionSOCKSFlag, OnionSOCKSEnv)
	if socks != "" {
		if err := chainonion.ValidateSOCKS(socks); err != nil {
			return ctx, "", clierr.Usage("--onion-socks: %v", err)
		}
	}
	client, err := chainonion.NewClient(socks)
	if err != nil {
		return ctx, "", clierr.Failure("%v", err)
	}
	return clusterreg.WithHTTPClient(ctx, client), base, nil
}
