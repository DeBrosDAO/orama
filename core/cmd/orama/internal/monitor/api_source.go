package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Timeouts for the telemetry API.
const (
	// apiRequestTimeout bounds a one-shot snapshot. The gateway assembles it
	// from every node's telemetry, so it can take longer than a plain call.
	apiRequestTimeout = 60 * time.Second
	// streamIdleMin is the shortest silence after which a stream counts as
	// dead. The gateway sends keepalives, so a silent stream is a broken one.
	streamIdleMin = 30 * time.Second
	// streamIdleIntervals scales the idle bound with the requested interval.
	streamIdleIntervals = 3
)

// SSE event names the telemetry stream sends.
const (
	eventSnapshot = "snapshot"
	eventError    = "error"
)

// apiSource reads snapshots from the gateway's operator telemetry API: one
// GET for a one-shot view, a server-sent event stream for the live one.
type apiSource struct {
	client *apiClient
	// wait sleeps between reconnects; tests replace it to run without delay.
	wait func(ctx context.Context, d time.Duration) error
	// jitter randomises the reconnect waits; nil uses math/rand. Tests fix it.
	jitter func() float64
}

// errStreamClosed is a stream the gateway ended cleanly, as it does on a
// schedule.
var errStreamClosed = errors.New("the gateway closed the telemetry stream")

func newAPISourceForEnv(env string) (*apiSource, error) {
	c, err := newAPIClientForEnv(env)
	if err != nil {
		return nil, err
	}
	return &apiSource{client: c, wait: sleepCtx}, nil
}

func (a *apiSource) Mode() Mode { return ModeAPI }

func (a *apiSource) Snapshot(ctx context.Context) (*cluster.ClusterSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	resp, err := a.client.get(ctx, telemetryPath, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var snap cluster.ClusterSnapshot
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSnapshotBytes)).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode the telemetry snapshot from %s: %w", a.client.gatewayURL, err)
	}
	return &snap, nil
}

func (a *apiSource) Watch(ctx context.Context, interval time.Duration) <-chan Update {
	out := make(chan Update)
	go a.watchLoop(ctx, interval, out)
	return out
}

// watchLoop keeps a stream open, reconnecting with backoff whenever it drops.
// A stream the gateway ends on schedule after delivering data is reopened
// after a short pause without any change of state, so the view does not flag
// seconds-old data as stale every time. Any other drop is reported, and the
// backoff starts over after a connection that delivered a snapshot. A refused
// credential ends the loop, as does a missing endpoint before any snapshot
// arrived: retrying cannot fix them.
func (a *apiSource) watchLoop(ctx context.Context, interval time.Duration, out chan<- Update) {
	defer close(out)
	b := newBackoff(a.jitter)
	failures, everDelivered, quiet := 0, false, false
	for {
		if !quiet && !send(ctx, out, Update{State: LinkConnecting, Attempt: failures}) {
			return
		}
		delivered, err := a.stream(ctx, interval, out)
		if ctx.Err() != nil {
			return
		}
		everDelivered = everDelivered || delivered
		if isFatal(err, everDelivered) {
			send(ctx, out, Update{State: LinkFailed, Err: err})
			return
		}
		if delivered {
			b.Reset()
			failures = 0
		}
		quiet = delivered && errors.Is(err, errStreamClosed)
		delay := reconnectMin
		if !quiet {
			failures++
			delay = b.Next()
			if !send(ctx, out, Update{State: LinkReconnecting, Err: err, RetryIn: delay, Attempt: failures}) {
				return
			}
		}
		if a.wait(ctx, delay) != nil {
			return
		}
	}
}

// isFatal reports whether retrying cannot help. A missing endpoint is fatal
// only until the stream has worked once: after that, a reconnect that reaches
// a gateway not yet upgraded in a rolling upgrade is worth retrying.
func isFatal(err error, everDelivered bool) bool {
	switch clierr.CodeOf(err) {
	case clierr.CodeAuth, clierr.CodeUsage:
		return true
	case clierr.CodeNotFound:
		return !everDelivered
	default:
		return false
	}
}

// stream runs one connection until it ends, forwarding its events. It reports
// whether any snapshot arrived and why the connection ended; it never ends
// without an error, since the gateway closing the stream is itself a reason to
// reconnect.
func (a *apiSource) stream(ctx context.Context, interval time.Duration, out chan<- Update) (bool, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	path := fmt.Sprintf("%s?interval=%d", telemetryStreamPath, intervalSeconds(interval))
	resp, err := a.client.get(sctx, path, "text/event-stream")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	idle := streamIdleTimeout(interval)
	watchdog := time.AfterFunc(idle, cancel)
	defer watchdog.Stop()
	delivered := false
	err = readSSE(resp.Body, func() { watchdog.Reset(idle) }, func(ev sseEvent) error {
		u, ok := eventUpdate(ev)
		if !ok {
			return nil
		}
		delivered = delivered || u.Snapshot != nil
		if !send(ctx, out, u) {
			return ctx.Err()
		}
		return nil
	})
	switch {
	case ctx.Err() != nil:
		return delivered, ctx.Err()
	case sctx.Err() != nil:
		return delivered, fmt.Errorf("the telemetry stream from %s went silent for %s", a.client.gatewayURL, idle)
	case err != nil:
		return delivered, fmt.Errorf("the telemetry stream from %s broke: %w", a.client.gatewayURL, err)
	}
	return delivered, fmt.Errorf("%w (%s)", errStreamClosed, a.client.gatewayURL)
}

// eventUpdate turns one stream event into an Update, and false for an event
// the monitor does not know.
func eventUpdate(ev sseEvent) (Update, bool) {
	switch ev.Name {
	case eventSnapshot:
		var snap cluster.ClusterSnapshot
		if err := json.Unmarshal([]byte(ev.Data), &snap); err != nil {
			return Update{State: LinkLive, Err: fmt.Errorf("decode a snapshot event: %w", err)}, true
		}
		return Update{State: LinkLive, Snapshot: &snap}, true
	case eventError:
		var e struct {
			Error string `json:"error"`
		}
		msg := ev.Data
		if json.Unmarshal([]byte(ev.Data), &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return Update{State: LinkLive, Err: fmt.Errorf("the gateway reported: %s", CleanText(msg))}, true
	default:
		return Update{}, false
	}
}

// intervalSeconds is the interval in whole seconds, rounded up, as the stream
// endpoint takes it.
func intervalSeconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

func streamIdleTimeout(interval time.Duration) time.Duration {
	return max(streamIdleMin, streamIdleIntervals*interval)
}
