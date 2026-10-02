package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"go.uber.org/zap"
)

func TestSleepCtx(t *testing.T) {
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("a completed sleep reported false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Minute) {
		t.Fatal("a cancelled context reported a completed sleep")
	}
}

// The supervisor's thresholds encode a judgement: one failed probe is a blip,
// several across half a minute is the cache being gone. Pinned so a retune
// cannot silently make the gateway hang on a dead cache for minutes, which is
// the behaviour this replaced.
func TestOlricSupervisorBounds(t *testing.T) {
	if olricUnhealthyThreshold < 2 {
		t.Error("a single failed probe must not drop the client; that is a blip, not an outage")
	}

	detection := time.Duration(olricUnhealthyThreshold) * olricProbeInterval
	if detection > 60*time.Second {
		t.Errorf("a dead cache takes %s to detect; requests hang for that long", detection)
	}

	if olricReconnectBase > olricReconnectMax {
		t.Error("the backoff base exceeds its ceiling")
	}
}

func TestGateway_probeOlricWithNoClient(t *testing.T) {
	// The supervisor calls this only when a client exists, but a nil one must
	// report a failure rather than panic — it is the state the gateway spends
	// its whole life in when Olric is down.
	g := &Gateway{}
	if err := g.probeOlric(context.Background()); err == nil {
		t.Fatal("probing with no client reported success")
	}
}

func TestGateway_setAndGetOlricClient(t *testing.T) {
	g := &Gateway{}
	if g.getOlricClient() != nil {
		t.Fatal("a fresh gateway reported a client")
	}

	// Dropping to nil is what makes cache handlers answer 503 instead of
	// returning transport errors from a client that cannot reach anything.
	g.setOlricClient(nil)
	if g.getOlricClient() != nil {
		t.Fatal("setOlricClient(nil) did not clear the client")
	}
}

// Dropping the client must also drop the cache handlers built on it: left in
// place they keep calling the dead client and the routes never answer 503.
func TestGateway_droppingTheClientDropsTheCacheHandlers(t *testing.T) {
	g := &Gateway{}
	srv := olrictest.Start(t)
	client, err := olric.NewClient(olric.Config{Servers: []string{srv.Addr}}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	g.setOlricClient(client)
	if g.cacheHandlers == nil {
		t.Fatal("connecting did not wire the cache handlers")
	}
	g.setOlricClient(nil)
	if g.cacheHandlers != nil {
		t.Fatal("setOlricClient(nil) left the cache handlers wired to the dropped client")
	}

	rec := httptest.NewRecorder()
	g.cachePutHandler(rec, httptest.NewRequest(http.MethodPost, "/v1/cache/put", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("put with no cache answered %d, want 503", rec.Code)
	}
}
