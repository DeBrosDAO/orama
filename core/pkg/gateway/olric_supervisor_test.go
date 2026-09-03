package gateway

import (
	"context"
	"testing"
	"time"
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
