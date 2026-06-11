package hostfunctions

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// feat-6 follow-up: the per-invocation publish budget bounds gossipsub flooding
// now that the implicit 2s/publish throttle is gone. These verify the cap is
// enforced before the message ever reaches the pubsub layer. A non-nil sentinel
// adapter is used because once the budget is exceeded the publish is rejected
// before h.pubsub.Publish is reached, so the adapter is never dereferenced.

func TestPubSubPublish_budgetEnforced(t *testing.T) {
	h := &HostFunctions{pubsub: &pubsub.ClientAdapter{}}
	ctx := serverless.WithPublishCounter(context.Background())
	serverless.AddPublishCount(ctx, maxPublishesPerInvocation) // exhaust to the cap

	if err := h.PubSubPublish(ctx, "t", []byte("d")); err == nil {
		t.Fatal("expected publish-budget-exceeded error once the per-invocation cap is reached")
	}
}

func TestPubSubPublishBatch_budgetEnforced(t *testing.T) {
	h := &HostFunctions{pubsub: &pubsub.ClientAdapter{}}
	ctx := serverless.WithPublishCounter(context.Background())
	serverless.AddPublishCount(ctx, maxPublishesPerInvocation)

	in := []byte(`[{"topic":"t","data_base64":""}]`)
	if err := h.PubSubPublishBatch(ctx, in); err == nil {
		t.Fatal("expected publish-budget-exceeded error for the batch once over the cap")
	}
}

func TestExecAndPublish_budgetEnforced(t *testing.T) {
	// exec_and_publish reaches the same shared gossipsub path, so it must also
	// be bounded. db is non-nil but BatchWithSeq is never reached once the
	// budget check rejects (it runs before the write).
	fake := &fakeBatchClient{}
	h := &HostFunctions{pubsub: &pubsub.ClientAdapter{}, db: fake}
	ctx := serverless.WithInvocationContext(
		serverless.WithPublishCounter(context.Background()),
		&serverless.InvocationContext{Namespace: "ns-test"},
	)
	serverless.AddPublishCount(ctx, maxPublishesPerInvocation)

	in := []byte(`{"ops":[{"kind":"exec","sql":"INSERT INTO t VALUES (1)"}]}`)
	if _, err := h.ExecAndPublish(ctx, in, "topic", []byte("data")); err == nil {
		t.Fatal("expected publish-budget-exceeded error from exec_and_publish once over the cap")
	}
	// The budget check runs before the write — an over-budget call must have
	// no side effects (no BatchWithSeq, hence no commit + no publish).
	if fake.seqCalls != 0 {
		t.Errorf("over-budget exec_and_publish must not write; got %d BatchWithSeq call(s)", fake.seqCalls)
	}
}
