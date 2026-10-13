package vpncmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var c common
	var socks, dns string
	up := &cobra.Command{
		Use:   "up",
		Short: "Run a SOCKS5 proxy into an Orama Tor network",
		Long: `Start tor on the network and keep it running until interrupted.

The SOCKS5 proxy listens on loopback only (--socks). Point an application at it
as socks5h, so the proxy resolves names, and each distinct SOCKS username gets
its own circuit. --dns also offers a DNS resolver on loopback that answers
through the network.

If tor stops, up exits with an error and the proxy port closes; applications
using it fail instead of connecting some other way.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runUp(cmd, c, socks, dns) },
	}
	c.addFlags(up)
	up.Flags().StringVar(&socks, "socks", DefaultSOCKS, "Loopback address for the SOCKS5 proxy")
	up.Flags().StringVar(&dns, "dns", "", "Loopback address for a DNS resolver that answers through the network (off by default)")
	return up
}

func runUp(cmd *cobra.Command, c common, socks, dns string) error {
	n, opts, err := c.load(socks, dns)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tor, err := join(ctx, cmd, c.tor, n, opts)
	if err != nil {
		return err
	}
	defer func() { _ = tor.Stop() }()
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Joined the %s Tor network.\nSOCKS5 proxy: %s (use socks5h so the proxy resolves names)\n", n.Name, tor.SocksAddr)
	if tor.DNSAddr != "" {
		fmt.Fprintf(out, "DNS resolver: %s\n", tor.DNSAddr)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-tor.Done():
		if ctx.Err() != nil {
			return nil
		}
		return clierr.Unavailable("tor stopped (%v); the proxy at %s is closed and traffic is not routed around it", tor.Err(), tor.SocksAddr)
	}
}
