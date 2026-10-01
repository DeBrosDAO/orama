package hostfunctions

import (
	"context"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// downOlric is an Olric client that is configured but cannot open a DMap, so a
// trigger-depth record the dispatcher tries to share cluster-wide fails.
type downOlric struct{ olriclib.Client }

func (downOlric) NewDMap(string, ...olriclib.DMapOption) (olriclib.DMap, error) {
	return nil, fmt.Errorf("olric unavailable (test)")
}

func depthHost(t *testing.T) (*HostFunctions, *publishingBus) {
	t.Helper()
	bus := &publishingBus{}
	h := &HostFunctions{logger: zap.NewNop(), pubsub: bus}
	h.SetTriggerDispatcher(triggers.NewPubSubDispatcher(nil, nil, downOlric{}, nil, zap.NewNop()))
	return h, bus
}

func publishAtDepth(h *HostFunctions, depth int) error {
	ctx := serverless.WithPublishCounter(invocationCtx(&serverless.InvocationContext{Namespace: testNamespace, TriggerDepth: depth}))
	return h.PubSubPublish(ctx, "loop", []byte("payload"))
}

// A publish from a triggered invocation records its depth before it leaves; a
// record that cannot be kept stops the publish, because publishing without it
// would restart the chain at depth 0.
func TestPubSubPublish_aTriggeredInvocationRecordsItsDepthBeforePublishing(t *testing.T) {
	h, bus := depthHost(t)
	if err := publishAtDepth(h, 2); err == nil {
		t.Fatal("a publish whose trigger depth could not be recorded went out")
	}
	if bus.published != 0 {
		t.Errorf("%d message(s) published without their depth", bus.published)
	}
}

func TestRecordPublishDepth_aDirectInvocationRecordsNothing(t *testing.T) {
	// The Olric behind this dispatcher is down: any record attempt would fail.
	h, _ := depthHost(t)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: testNamespace, TriggerDepth: 0})
	if err := h.recordPublishDepth(ctx, "loop", []byte("payload")); err != nil {
		t.Fatalf("a depth-0 publish needs no record: %v", err)
	}
}

func TestRecordPublishDepth_withoutADispatcherOrAnInvocationIsANoOp(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop()}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: testNamespace, TriggerDepth: 3})
	if err := h.recordPublishDepth(ctx, "loop", []byte("payload")); err != nil {
		t.Fatalf("no dispatcher wired: %v", err)
	}
	withDispatcher, _ := depthHost(t)
	if err := withDispatcher.recordPublishDepth(context.Background(), "loop", []byte("payload")); err != nil {
		t.Fatalf("no invocation: %v", err)
	}
}

func TestPubSubPublishBatch_aTriggeredInvocationRecordsEachMessage(t *testing.T) {
	h, bus := depthHost(t)
	ctx := serverless.WithPublishCounter(invocationCtx(&serverless.InvocationContext{Namespace: testNamespace, TriggerDepth: 2}))
	if err := h.PubSubPublishBatch(ctx, []byte(`[{"topic":"a","data_base64":"eA=="}]`)); err == nil {
		t.Fatal("a batch whose trigger depth could not be recorded went out")
	}
	if bus.published != 0 {
		t.Errorf("%d message(s) published without their depth", bus.published)
	}
}
