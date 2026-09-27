package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/telemetry/hub"
)

// internalTelemetryHandler serves this node's latest report to a peer's
// cluster gateway, with its age measured on this node's clock
// (hub.ReportAgeHeader). Only a coordination-MAC-signed request over the mesh
// gets an answer — checked before anything else, so an unsigned request of any
// method is told the route does not exist, as the other mesh-only routes do.
func (g *Gateway) internalTelemetryHandler(w http.ResponseWriter, r *http.Request) {
	if !g.verifyCoordination(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t := g.clusterTelemetry(w)
	if t == nil {
		return
	}
	rpt, err := t.self.Latest()
	if rpt == nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	age := hub.ReportAge(rpt, time.Now())
	w.Header().Set(hub.ReportAgeHeader, strconv.FormatInt(age.Milliseconds(), 10))
	writeJSON(w, http.StatusOK, rpt)
}

// operatorTelemetryHandler is the full cluster snapshot — every node's report
// and the alerts derived from them — for an operator.
func (g *Gateway) operatorTelemetryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !g.authorizeOperator(w, r) {
		return
	}
	t := g.clusterTelemetry(w)
	if t == nil {
		return
	}
	ctx, cancel := snapshotContext(r)
	defer cancel()
	snap, err := t.agg.Snapshot(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("assemble cluster snapshot: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// authorizeOperator requires a caller on the operator list.
func (g *Gateway) authorizeOperator(w http.ResponseWriter, r *http.Request) bool {
	if g.operatorHandler == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway cannot check the operator list")
		return false
	}
	_, ok := g.operatorHandler.Authorize(w, r)
	return ok
}

const (
	// streamDefaultInterval, streamMinInterval and streamMaxInterval bound how
	// often a stream sends a snapshot. Below the minimum a viewer would only
	// receive the same cached snapshot again.
	streamDefaultInterval = 5 * time.Second
	streamMinInterval     = 2 * time.Second
	streamMaxInterval     = time.Minute
	// streamKeepalive keeps idle proxies from closing a quiet stream.
	streamKeepalive = 15 * time.Second
	// streamMaxDuration ends a stream before the server's 120s WriteTimeout
	// would cut it mid-event. The client reconnects, which also re-checks
	// that the caller is still an operator.
	streamMaxDuration = 100 * time.Second
)

// operatorTelemetryStreamHandler streams cluster snapshots as server-sent
// events: `event: snapshot` with the snapshot as one line of JSON, or
// `event: error` when one could not be assembled.
func (g *Gateway) operatorTelemetryStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !g.authorizeOperator(w, r) {
		return
	}
	t := g.clusterTelemetry(w)
	if t == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported on this connection")
		return
	}
	interval, err := streamInterval(r.URL.Query().Get("interval"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	g.streamSnapshots(r, w, flusher, t.agg, interval)
}

func (g *Gateway) streamSnapshots(r *http.Request, w http.ResponseWriter, f http.Flusher, agg *hub.Aggregator, interval time.Duration) {
	deadline := time.NewTimer(streamMaxDuration)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	keepalive := time.NewTicker(streamKeepalive)
	defer keepalive.Stop()
	if g.sendSnapshotEvent(r, w, agg) != nil {
		return
	}
	f.Flush()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-keepalive.C:
			_, err = fmt.Fprint(w, ": keepalive\n\n")
		case <-tick.C:
			err = g.sendSnapshotEvent(r, w, agg)
		}
		if err != nil {
			return
		}
		f.Flush()
	}
}

// sendSnapshotEvent writes one event. The error is a write failure, which
// ends the stream; a snapshot that could not be assembled is an error event.
func (g *Gateway) sendSnapshotEvent(r *http.Request, w http.ResponseWriter, agg *hub.Aggregator) error {
	ctx, cancel := snapshotContext(r)
	defer cancel()
	event, payload := "snapshot", any(nil)
	snap, err := agg.Snapshot(ctx)
	if err != nil {
		event, payload = "error", map[string]string{"error": fmt.Sprintf("assemble cluster snapshot: %v", err)}
	} else {
		payload = snap
	}
	body, err := json.Marshal(payload)
	if err != nil {
		g.logger.Logger.Error("encode telemetry stream event", zap.String("event", event), zap.Error(err))
		return fmt.Errorf("encode %s event: %w", event, err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
	return err
}

// streamInterval parses ?interval=<seconds>, bounded to the allowed range.
func streamInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return streamDefaultInterval, nil
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("interval must be whole seconds, got %q", raw)
	}
	d := time.Duration(secs) * time.Second
	if d < streamMinInterval || d > streamMaxInterval {
		return 0, fmt.Errorf("interval must be between %d and %d seconds", int(streamMinInterval.Seconds()), int(streamMaxInterval.Seconds()))
	}
	return d, nil
}
