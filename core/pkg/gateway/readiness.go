package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ReadinessState is the gateway's own start-up state — whether it has finished
// the work it must do before it can serve requests correctly. It is separate
// from the health of the things the gateway talks to: a gateway can be ready
// while its cache is down, and not ready while every subsystem it can see
// answers fine.
type ReadinessState string

const (
	// ReadinessStarting means the gateway is listening but has not yet brought
	// its database schema to the version this binary requires — almost always
	// because the local rqlite has no leader to serve a write to. It keeps
	// retrying; nothing about this state needs an operator.
	ReadinessStarting ReadinessState = "starting"

	// ReadinessReady means the schema is at the required version and the
	// gateway will serve.
	ReadinessReady ReadinessState = "ready"

	// ReadinessBlocked means a leader WAS reached and the schema is genuinely
	// below what this binary requires. Unlike starting, retrying cannot fix it:
	// something must migrate the database or roll the binary back. The gateway
	// stays up so the reason is visible, and refuses everything but health.
	ReadinessBlocked ReadinessState = "blocked"
)

// readiness tracks ReadinessState with the reason behind it, and closes a
// channel the first time the gateway becomes ready so background work can wait
// on it rather than poll.
type readiness struct {
	mu       sync.RWMutex
	state    ReadinessState
	reason   string
	since    time.Time
	readyCh  chan struct{}
	readOnce sync.Once
}

func newReadiness() readiness {
	return readiness{
		state:   ReadinessStarting,
		reason:  "waiting for the database schema",
		since:   time.Now(),
		readyCh: make(chan struct{}),
	}
}

func (r *readiness) set(state ReadinessState, reason string) {
	r.mu.Lock()
	if r.state == state && r.reason == reason {
		r.mu.Unlock()
		return
	}
	r.state = state
	r.reason = reason
	r.since = time.Now()
	ch := r.readyCh
	r.mu.Unlock()

	if state == ReadinessReady && ch != nil {
		r.readOnce.Do(func() { close(ch) })
	}
}

// waitReady blocks until the gateway is ready or ctx is done, and reports which
// happened.
//
// Background work that touches the database has to wait for this. Before the
// schema work moved off the start-up path it was implicitly ordered after the
// migrations; now it would otherwise start against a pre-migration database and
// race the apply on the same tables.
func (r *readiness) waitReady(ctx context.Context) bool {
	r.mu.RLock()
	ch := r.readyCh
	ready := r.state == ReadinessReady
	r.mu.RUnlock()

	if ready {
		return true
	}
	if ch == nil {
		// A readiness that was never initialised (a Gateway assembled without
		// New) never becomes ready.
		<-ctx.Done()
		return false
	}

	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *readiness) snapshot() (ReadinessState, string, time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == "" {
		// The zero value is "has not started converging yet", which is
		// starting, not ready. It matters because readiness is a value field on
		// Gateway: a Gateway assembled without New — only tests do that — must
		// fail closed rather than serve as if it had converged.
		return ReadinessStarting, "not initialized", r.since
	}
	return r.state, r.reason, r.since
}

// Readiness reports the gateway's start-up state and the reason for it.
func (g *Gateway) Readiness() (ReadinessState, string) {
	state, reason, _ := g.ready.snapshot()
	return state, reason
}

// AwaitReady blocks until the gateway has finished starting up, or ctx is
// done. Reports false if it gave up.
func (g *Gateway) AwaitReady(ctx context.Context) bool {
	return g.ready.waitReady(ctx)
}

// IsReady reports whether the gateway has finished starting up.
func (g *Gateway) IsReady() bool {
	state, _, _ := g.ready.snapshot()
	return state == ReadinessReady
}

// Schema readiness retry bounds. The first attempt happens immediately; a
// failure is retried with backoff for as long as the process lives. Variables
// so tests can drive the loop without waiting out real backoff.
var (
	schemaRetryBaseBackoff = 5 * time.Second
	schemaRetryMaxBackoff  = 60 * time.Second
)

// observedRaftState reports the local rqlite's raft state, for the retry log.
// "unknown" when it cannot be read — the log line is diagnostic, so a failure
// to gather it must not interrupt the retry.
func observedRaftState(ctx context.Context, cfg *Config) string {
	hostPort := rqliteHostPortFromDSN(cfg.RQLiteDSN)
	if hostPort == "" {
		return "unknown"
	}
	probeCtx, cancel := context.WithTimeout(ctx, raftStateProbeTimeout)
	defer cancel()

	state, err := rqlite.RaftState(probeCtx, hostPort)
	if err != nil {
		return "unknown"
	}
	return state
}

// raftStateProbeTimeout bounds the diagnostic /status read in the retry log.
const raftStateProbeTimeout = 3 * time.Second

// rqliteHostPortFromDSN extracts host:port from an rqlite DSN, dropping any
// credentials. Returns "" when the DSN carries no usable address, which the
// caller reads as "cannot tell".
func rqliteHostPortFromDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return ""
	}
	if _, err := strconv.Atoi(u.Port()); err != nil {
		return ""
	}
	return u.Host
}

// startSchemaReadiness brings the gateway's schema up in the background and
// publishes the outcome as readiness.
//
// It runs in the background because "the local rqlite has no leader yet" is a
// condition that fixes itself and says nothing about this gateway. It used to
// be handled at start-up: the leader wait, the migrations and the schema
// contract were one call whose error the caller swallowed with a warning, so a
// namespace whose raft had no leader ended up with every gateway serving on an
// unmigrated database — a cryptic SQL error to end users instead of a clear
// "not ready yet". Separating the two lets the honest answer be given for each:
// keep retrying the transient one, refuse to serve on the permanent one.
func (g *Gateway) startSchemaReadiness(ctx context.Context, cfg *Config, deps *Dependencies) {
	prepare := func(attemptCtx context.Context) error {
		return prepareSchema(attemptCtx, g.logger, cfg, deps)
	}
	raftState := func() string { return observedRaftState(ctx, cfg) }

	go g.convergeSchema(ctx, prepare, raftState)
}

// convergeSchema is startSchemaReadiness's loop, with its two effects injected
// so it can be driven without a database.
func (g *Gateway) convergeSchema(ctx context.Context, prepare func(context.Context) error, raftState func() string) {
	backoff := schemaRetryBaseBackoff
	attempt := 0

	for {
		attempt++
		err := prepare(ctx)
		if err == nil {
			g.ready.set(ReadinessReady, "")
			g.logger.ComponentInfo(logging.ComponentGeneral, "Gateway ready: schema is at the required version",
				zap.Int("attempts", attempt))
			return
		}

		if isSchemaContractViolation(err) {
			// A leader answered and told us the schema is behind. Retrying
			// cannot change that, and serving on it corrupts data.
			g.ready.set(ReadinessBlocked, err.Error())
			g.logger.ComponentError(logging.ComponentGeneral,
				"Gateway will not serve: the database schema is below the version this binary requires. "+
					"Migrate the database or roll this binary back; retrying will not help.",
				zap.Error(err))
			return
		}

		g.ready.set(ReadinessStarting, err.Error())
		g.logger.ComponentWarn(logging.ComponentGeneral,
			"Gateway not ready yet, retrying schema preparation",
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", backoff),
			zap.String("raft_state", raftState()),
			zap.Error(err))

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		backoff *= 2
		if backoff > schemaRetryMaxBackoff {
			backoff = schemaRetryMaxBackoff
		}
	}
}

// readinessPassthrough is what stays reachable while the gateway cannot serve:
// diagnosis, peer liveness, and the two things a certificate depends on.
//
// It is deliberately NOT isPublicPath. That answers a different question —
// "does this need an API key?" — and its answer is yes for a large set of
// endpoints that write to the database: /v1/auth/verify and /register insert
// refresh tokens and API keys, /v1/invoke runs serverless functions,
// /v1/vault/* and /v1/internal/join are control-plane writes. Reusing it would
// have let a gateway that had just decided its schema is BELOW what this binary
// requires keep writing to that schema, which is the exact corruption
// ReadinessBlocked exists to prevent.
//
// Everything listed here answers from config or process state alone.
func readinessPassthrough(p string) bool {
	switch p {
	case "/health", "/v1/health", "/status", "/v1/status", "/v1/version",
		"/v1/internal/ping", "/v1/internal/tls/check":
		return true
	}
	// Caddy's HTTP-01 challenge: a gateway that cannot serve must still be
	// able to obtain the certificate it will serve with once it can.
	return strings.HasPrefix(p, "/.well-known/acme-challenge/")
}

// readinessGate refuses traffic the gateway cannot serve correctly yet, with a
// reason, instead of letting it reach handlers that will fail on a database
// with no leader or a schema below the one they were written against.
func (g *Gateway) readinessGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, reason, since := g.ready.snapshot()
		if state == ReadinessReady || readinessPassthrough(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":  fmt.Sprintf("gateway is %s", state),
			"status": string(state),
			"reason": reason,
			"since":  since,
		})
	})
}
