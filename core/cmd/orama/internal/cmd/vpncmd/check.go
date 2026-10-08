package vpncmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainonion"
	"github.com/DeBrosOfficial/network/pkg/onionnet"
	"github.com/spf13/cobra"
)

const (
	// probePath is a validator onion service's CometBFT RPC status route.
	probePath = "/status"
	// probeBodyLimit bounds what a probe reads of the answer.
	probeBodyLimit = 1 << 16
	// probeTimeout covers one probe: a rendezvous circuit and one request.
	probeTimeout = 2 * time.Minute
)

func newCheckCmd() *cobra.Command {
	var c common
	var onion string
	check := &cobra.Command{
		Use:   "check",
		Short: "Join an Orama Tor network and reach a validator onion service through it",
		Long: `Start tor on the network (stopped again when the check ends) and request the status
route of a validator onion service through the proxy, over a fresh circuit each.

By default every validator onion service the network file lists is tried, and the check
passes when at least one answers; --onion tries only the one given. It fails when tor
cannot bootstrap on the network's authorities, when none of the onion services answers, and
when the network file lists none and no --onion is given. Nothing is tried outside the
network.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCheck(cmd, c, onion) },
	}
	c.addFlags(check)
	check.Flags().StringVar(&onion, "onion", "", "Check only this validator onion service (addr.onion[:port])")
	return check
}

func runCheck(cmd *cobra.Command, c common, onion string) error {
	socks, err := onionnet.FreeLoopbackAddr()
	if err != nil {
		return clierr.Failure("%v", err)
	}
	n, opts, err := c.load(socks, "")
	if err != nil {
		return err
	}
	targets := n.ValidatorOnions
	if onion != "" {
		targets = []string{onion}
	}
	if len(targets) == 0 {
		return clierr.Usage("%v", onionnet.ErrNoValidatorOnion)
	}
	for _, t := range targets {
		if _, err := chainonion.Base(t); err != nil {
			return clierr.Usage("onion service: %v", err)
		}
	}
	tor, err := join(cmd.Context(), cmd, c.tor, n, opts)
	if err != nil {
		return err
	}
	defer func() { _ = tor.Stop() }()
	return probeAll(cmd.Context(), cmd, tor, n.Name, targets)
}

func probeAll(ctx context.Context, cmd *cobra.Command, tor *onionnet.Tor, network string, targets []string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Joined the %s Tor network.\n", network)
	reached := 0
	for _, t := range targets {
		if err := probe(ctx, tor, t); err != nil {
			fmt.Fprintf(out, "FAIL  %s: %v\n", t, err)
			continue
		}
		reached++
		fmt.Fprintf(out, "OK    %s answered through the network\n", t)
	}
	if reached == 0 {
		return clierr.Unavailable("none of %d validator onion services answered through the %s Tor network", len(targets), network)
	}
	fmt.Fprintf(out, "%d of %d validator onion services reached.\n", reached, len(targets))
	return nil
}

// probe requests the status route of one onion service over a circuit of its
// own, through tor's SOCKS port and nothing else.
func probe(ctx context.Context, tor *onionnet.Tor, onion string) error {
	base, err := chainonion.Base(onion)
	if err != nil {
		return err
	}
	client, err := chainonion.NewClient(tor.SocksAddr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+probePath, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, probeBodyLimit)); err != nil {
		return fmt.Errorf("read the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered HTTP %d", resp.StatusCode)
	}
	return nil
}
