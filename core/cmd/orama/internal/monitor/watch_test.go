package monitor

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeEvents writes a stream body and flushes it.
func writeEvents(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, body)
	w.(http.Flusher).Flush()
}

func drain(ch <-chan Update) []Update {
	var out []Update
	for u := range ch {
		out = append(out, u)
	}
	return out
}

func TestWatch_forwardsEventsThenReconnects(t *testing.T) {
	snap := snapshotJSON(t, "1.1.1.1")
	var gotInterval atomic.Value
	src, ctx, waits := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		gotInterval.Store(r.URL.Query().Get("interval"))
		if r.URL.Path != telemetryStreamPath || r.Header.Get("Accept") != "text/event-stream" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		writeEvents(w, "event: snapshot\ndata: "+snap+"\n\n: keepalive\n\n"+
			"event: error\ndata: {\"error\":\"peer 10.0.0.2 timed out\"}\n\nevent: snapshot\ndata: "+snap+"\n\n")
	}, 1)

	updates := drain(src.Watch(ctx, 5*time.Second))

	if gotInterval.Load() != "5" {
		t.Fatalf("interval query = %v, want 5", gotInterval.Load())
	}
	var states []string
	for _, u := range updates {
		switch {
		case u.Snapshot != nil:
			states = append(states, "snapshot")
		case u.State == LinkLive && u.Err != nil:
			states = append(states, "server-error:"+u.Err.Error())
		default:
			states = append(states, fmt.Sprint(u.State))
		}
	}
	// The gateway ending a stream that delivered is reopened quietly: no
	// reconnecting update, so the view never flags fresh data as stale.
	want := fmt.Sprint([]string{"0", "snapshot", "server-error:the gateway reported: peer 10.0.0.2 timed out", "snapshot"})
	if fmt.Sprint(states) != want {
		t.Fatalf("updates = %v\nwant      %v", states, want)
	}
	if len(*waits) != 1 || (*waits)[0] != reconnectMin {
		t.Fatalf("waits = %v, want one short pause after a stream that delivered", *waits)
	}
}

func TestWatch_backsOffWhileTheGatewayIsDown(t *testing.T) {
	src, ctx, waits := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"starting"}`, http.StatusServiceUnavailable)
	}, 6)

	updates := drain(src.Watch(ctx, 2*time.Second))

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, reconnectMax}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
	var reconnects int
	for _, u := range updates {
		if u.State == LinkReconnecting {
			reconnects++
			if !strings.Contains(u.Err.Error(), "not ready") || u.Attempt != reconnects {
				t.Fatalf("reconnect %d = %+v", reconnects, u)
			}
		}
	}
	if reconnects != len(want) {
		t.Fatalf("%d reconnect updates, want %d", reconnects, len(want))
	}
}

func TestWatch_backoffStartsOverAfterDataArrives(t *testing.T) {
	snap := snapshotJSON(t, "1.1.1.1")
	var conns atomic.Int32
	src, ctx, waits := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		if conns.Add(1) == 3 {
			writeEvents(w, "event: snapshot\ndata: "+snap+"\n\n")
			return
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}, 4)

	drain(src.Watch(ctx, 2*time.Second))

	// 503, 503, a stream that delivers and ends (quiet pause), then 503 with
	// the backoff started over.
	want := []time.Duration{time.Second, 2 * time.Second, reconnectMin, time.Second}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
}

func TestWatch_refusedCredentialStopsWithoutRetrying(t *testing.T) {
	src, ctx, waits := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
	}, 1)

	updates := drain(src.Watch(ctx, 2*time.Second))

	if len(*waits) != 0 {
		t.Fatalf("a refused credential was retried: waits %v", *waits)
	}
	last := updates[len(updates)-1]
	if last.State != LinkFailed || !strings.Contains(last.Err.Error(), "orama auth login") {
		t.Fatalf("last update = %+v, want a failure that says how to sign in", last)
	}
}

func TestWatch_badSnapshotEventIsReportedNotFatal(t *testing.T) {
	src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		writeEvents(w, "event: snapshot\ndata: {not json\n\n")
	}, 1)

	updates := drain(src.Watch(ctx, 2*time.Second))

	found := false
	for _, u := range updates {
		if u.State == LinkLive && u.Err != nil && strings.Contains(u.Err.Error(), "decode a snapshot event") {
			found = true
		}
		if u.State == LinkFailed {
			t.Fatalf("a bad event stopped the stream: %+v", u)
		}
	}
	if !found {
		t.Fatalf("the bad event was not reported: %+v", updates)
	}
}

func TestStreamIdleTimeout_scalesWithInterval(t *testing.T) {
	if got := streamIdleTimeout(2 * time.Second); got != streamIdleMin {
		t.Errorf("short interval: %v, want the floor %v", got, streamIdleMin)
	}
	if got := streamIdleTimeout(time.Minute); got != 3*time.Minute {
		t.Errorf("long interval: %v, want 3 intervals", got)
	}
}

// A reconnect that lands on a gateway not yet upgraded (404) must not end a
// view that was already working during a rolling upgrade.
func TestWatch_notFoundAfterDeliveryIsRetried(t *testing.T) {
	snap := snapshotJSON(t, "1.1.1.1")
	var conns atomic.Int32
	src, ctx, waits := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		if conns.Add(1) == 1 {
			writeEvents(w, "event: snapshot\ndata: "+snap+"\n\n")
			return
		}
		http.NotFound(w, r)
	}, 3)

	updates := drain(src.Watch(ctx, 2*time.Second))

	for _, u := range updates {
		if u.State == LinkFailed {
			t.Fatalf("a 404 after a working stream stopped the view: %v", u.Err)
		}
	}
	if len(*waits) != 3 {
		t.Fatalf("waits = %v, want retries", *waits)
	}
}

func TestWatch_notFoundBeforeAnySnapshotIsFatal(t *testing.T) {
	src, ctx, waits := testAPISource(t, http.NotFound, 1)
	updates := drain(src.Watch(ctx, 2*time.Second))
	if last := updates[len(updates)-1]; last.State != LinkFailed || len(*waits) != 0 {
		t.Fatalf("last = %+v, waits %v", last, *waits)
	}
}

func TestBackoff_jitterStaysWithinBounds(t *testing.T) {
	for _, r := range []float64{0, 0.25, 0.999} {
		b := newBackoff(func() float64 { return r })
		for range 8 {
			d := b.Next()
			lo := time.Duration(float64(b.cur) * (1 - reconnectJitter))
			hi := time.Duration(float64(b.cur) * (1 + reconnectJitter))
			if d < lo || d > hi {
				t.Fatalf("random %v: wait %v outside [%v, %v]", r, d, lo, hi)
			}
		}
	}
}
