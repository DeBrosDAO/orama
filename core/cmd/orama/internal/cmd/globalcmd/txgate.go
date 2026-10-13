package globalcmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/txgate"
	"github.com/spf13/cobra"
)

const (
	// txgateReadHeaderTimeout and txgateIdleTimeout keep a stalled Tor circuit
	// from holding a connection of the gate.
	txgateReadHeaderTimeout = 10 * time.Second
	txgateReadTimeout       = 30 * time.Second
	// txgateWriteTimeout covers the chain API call (txgate.DefaultUpstreamTimeout) and the answer.
	txgateWriteTimeout    = 40 * time.Second
	txgateIdleTimeout     = 60 * time.Second
	txgateShutdownTimeout = 10 * time.Second
)

var txgateFlags struct {
	listen   string
	upstream string
	rate     float64
	burst    int
	inFlight int
}

var txgateCmd = &cobra.Command{
	Use:   "txgate",
	Short: "Serve the validator's transaction gate on loopback (run by orama-global-txgate.service)",
	Long: `Serve the three calls a wallet needs to submit one transaction (read the signer's
account, broadcast, look the transaction up) from the chain's REST API, and
nothing else. The validator's onion service forwards to this listener, so the
rest of the chain API is not reachable over the onion. Requests arrive from the
local Tor process, so the limits are on the whole gate and no request is logged.
--listen must be a loopback address.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := txgateFlags
		host, _, err := net.SplitHostPort(f.listen)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			return clierr.Usage("--listen %q must be a loopback host:port: the gate has no authentication of its own", f.listen)
		}
		gate, err := txgate.New(txgate.Config{
			Upstream: f.upstream, Rate: f.rate, Burst: f.burst, InFlight: f.inFlight, UpstreamTimeout: txgate.DefaultUpstreamTimeout,
		})
		if err != nil {
			return clierr.Usage("%v", err)
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return serveGate(ctx, f.listen, gate)
	},
}

// newGateServer is the gate's HTTP server. Every connection is bounded in time
// from the first byte to the last: a caller that drips a body holds a
// connection of the gate, which no limiter has counted yet.
func newGateServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler: h, ReadHeaderTimeout: txgateReadHeaderTimeout, ReadTimeout: txgateReadTimeout,
		WriteTimeout: txgateWriteTimeout, IdleTimeout: txgateIdleTimeout,
	}
}

func serveGate(ctx context.Context, listen string, h http.Handler) error {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return clierr.Failure("listen on %s: %v", listen, err)
	}
	srv := newGateServer(h)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(lis) }()
	select {
	case err := <-done:
		return clierr.Failure("tx gate stopped: %v", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), txgateShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return clierr.Failure("stop the tx gate: %v", err)
	}
	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return clierr.Failure("tx gate: %v", err)
	}
	return nil
}

func init() {
	f := txgateCmd.Flags()
	f.StringVar(&txgateFlags.listen, "listen", "", "Loopback host:port to listen on [required]")
	f.StringVar(&txgateFlags.upstream, "upstream", "", "The chain REST API, http://host:port [required]")
	f.Float64Var(&txgateFlags.rate, "rate", txgate.DefaultRate, "Requests per second the gate forwards, in total")
	f.IntVar(&txgateFlags.burst, "burst", txgate.DefaultBurst, "Requests that may arrive at once")
	f.IntVar(&txgateFlags.inFlight, "max-in-flight", txgate.DefaultInFlight, "Most requests asked of the chain API at once")
	MaintCmd.AddCommand(cmdmeta.MarkNodeLocal(txgateCmd))
	// orama-global-txgate.service runs `orama maint global txgate`. The command moved
	// to `orama maint global txgate`; the installed unit keeps its path until an
	// upgrade rewrites it.
	Cmd.AddCommand(cmdmeta.HiddenAlias(txgateCmd))
}
