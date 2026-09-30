package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/telemetry/hub"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Telemetry runs on the cluster gateway only: it is the one gateway per node,
// it holds the cluster registry that lists the nodes, and it is what peers
// reach at constants.GatewayAPIPort over the mesh.
const (
	// telemetryInterval is how often this node collects its own report. The
	// report takes well under a second to collect; the interval is what keeps
	// it from costing a node more than it is worth while still showing a
	// failure within seconds.
	telemetryInterval = 10 * time.Second
	// telemetryCollectTimeout bounds one collection. The slowest collectors
	// (DNS resolution, journal scans) run several 4s commands in sequence.
	telemetryCollectTimeout = 60 * time.Second
	// telemetryStaleAfter is when a report stops counting: three missed
	// collections, or one that hung past its timeout.
	telemetryStaleAfter = 3*telemetryInterval + telemetryCollectTimeout
	// telemetryCacheTTL is how long an assembled snapshot is reused.
	telemetryCacheTTL = 5 * time.Second
	// telemetryPeerTimeout bounds one peer's answer. A peer serves its
	// report from memory, so anything slower is a peer in trouble.
	telemetryPeerTimeout = 4 * time.Second
	// telemetryMaxParallel bounds concurrent peer requests per assembly.
	telemetryMaxParallel = 16
	// telemetryMaxUnknown is how long a node on an older release may be
	// shown as unknown before it counts as unreachable: longer than any
	// rolling upgrade, short enough that a forgotten node is noticed.
	telemetryMaxUnknown = 2 * time.Hour
)

// telemetryService is the cluster gateway's monitoring state.
type telemetryService struct {
	self   *hub.SelfCollector
	agg    *hub.Aggregator
	uptime hub.UptimeStore
}

// startTelemetry starts this node's own collection and the uptime recorder,
// and wires the aggregator the monitoring endpoints read. Called for the
// cluster gateway only (see New).
func (g *Gateway) startTelemetry() {
	logger := g.logger.Logger.With(zap.String("component", "telemetry"))
	self := &hub.SelfCollector{
		Collect:  privhelper.NodeReport,
		Decorate: g.decorateNodeReport,
		Interval: telemetryInterval,
		Timeout:  telemetryCollectTimeout,
		Logger:   logger,
	}
	peers := hub.DBPeerLister{DB: g.sqlDB}
	agg := &hub.Aggregator{
		SelfID:      g.cfg.NodePeerID,
		Self:        self.Latest,
		Peers:       peers,
		Fetch:       g.telemetryFetcher(),
		CacheTTL:    telemetryCacheTTL,
		PeerTimeout: telemetryPeerTimeout,
		MaxParallel: telemetryMaxParallel,
		StaleAfter:  telemetryStaleAfter,
		MaxUnknown:  telemetryMaxUnknown,
	}
	store := hub.UptimeStore{DB: g.sqlDB}
	recorder := &hub.UptimeRecorder{
		SelfID: g.cfg.NodePeerID, Snapshot: agg.Snapshot, Peers: peers,
		Store: store, Logger: logger, Now: time.Now,
	}
	g.telemetry = &telemetryService{self: self, agg: agg, uptime: store}

	go self.Run(g.shutdownCtx)
	go func() {
		if g.AwaitReady(g.shutdownCtx) {
			recorder.Run(g.shutdownCtx)
		}
	}()
}

// decorateNodeReport adds what only this gateway knows to its node's report.
func (g *Gateway) decorateNodeReport(r *report.NodeReport) {
	r.Traffic = g.TrafficSnapshot()
	if g.registryDisownedTenants != nil {
		r.RegistryDisownedTenants = g.registryDisownedTenants()
	}
}

// telemetryFetcher asks peers' cluster gateways over the mesh, each request
// signed with the coordination MAC they verify.
func (g *Gateway) telemetryFetcher() hub.HTTPFetcher {
	return hub.HTTPFetcher{
		Client:  &http.Client{Timeout: telemetryPeerTimeout},
		BaseURL: constants.GatewayURLFor,
		Sign: func(r *http.Request) error {
			key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
			if err != nil {
				return err
			}
			return nodeauth.SignCoordination(key, r, time.Now())
		},
	}
}

// clusterTelemetry is this gateway's telemetry, or answers 503 when it has
// none: a namespace gateway does not monitor the cluster.
func (g *Gateway) clusterTelemetry(w http.ResponseWriter) *telemetryService {
	if g.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf(
			"cluster telemetry is served by the cluster gateway (port %d on each node), not by a namespace gateway",
			constants.GatewayAPIPort))
		return nil
	}
	return g.telemetry
}

// snapshotContext bounds how long a monitoring request waits for assembly.
func snapshotContext(r *http.Request) (context.Context, context.CancelFunc) {
	const snapshotWait = 20 * time.Second
	return context.WithTimeout(r.Context(), snapshotWait)
}
