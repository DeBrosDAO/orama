package gateway

import (
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// breakerReportIdle is how long an open breaker with no traffic is still
// reported: long enough to be seen across several telemetry collections, short
// enough that a deleted namespace's breaker stops raising an alert.
const breakerReportIdle = 5 * time.Minute

// logBreakerTransition writes one state change of a circuit breaker to the
// log: the namespace and node it guards, the consecutive failure count and the
// last error. The opening is a warning; the probe admitted after the open
// duration, the probe that failed and the close are information. Volume is
// bounded by CircuitBreaker itself (breakerCycleLogInterval).
func (g *Gateway) logBreakerTransition(tr BreakerTransition) {
	fields := []zap.Field{
		zap.String("breaker", tr.Key),
		zap.String("namespace", tr.Namespace),
		zap.String("node", tr.Node),
		zap.String("from", tr.From.String()),
		zap.String("to", tr.To.String()),
		zap.Int("failures", tr.Failures),
		zap.String("last_error", tr.LastError),
	}
	if tr.SuppressedCycles > 0 {
		fields = append(fields, zap.Int("suppressed_probe_cycles", tr.SuppressedCycles))
	}
	switch {
	case tr.To == CircuitOpen && tr.From == CircuitClosed:
		g.logger.ComponentWarn(logging.ComponentGeneral, "circuit breaker opened: requests to this target are refused until a probe succeeds", fields...)
	case tr.To == CircuitOpen:
		g.logger.ComponentWarn(logging.ComponentGeneral, "circuit breaker probe failed or was not answered: still open", fields...)
	case tr.To == CircuitHalfOpen:
		g.logger.ComponentInfo(logging.ComponentGeneral, "circuit breaker half-open: admitting one probe", fields...)
	default:
		g.logger.ComponentInfo(logging.ComponentGeneral, "circuit breaker closed: the target answers again", fields...)
	}
}

// breakersReport is what this gateway's breakers say, for the node report, or
// nil when it holds none.
func (g *Gateway) breakersReport() *report.BreakersReport {
	if g.circuitBreakers == nil {
		return nil
	}
	tracked := g.circuitBreakers.Len()
	if tracked == 0 {
		return nil
	}
	unhealthy := g.circuitBreakers.Unhealthy(breakerReportIdle)
	out := &report.BreakersReport{Tracked: tracked, NotClosed: len(unhealthy)}
	if len(unhealthy) > report.MaxBreakersReported {
		unhealthy = unhealthy[:report.MaxBreakersReported]
	}
	for _, b := range unhealthy {
		out.Unhealthy = append(out.Unhealthy, report.BreakerReport{
			Namespace: b.Namespace, Node: b.Node, State: b.State.String(),
			Failures: b.Failures, LastError: b.LastError, LastFailure: b.LastFailure,
		})
	}
	return out
}
