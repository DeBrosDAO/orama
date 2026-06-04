package serverless

import (
	"context"
	"testing"
)

// feat-6 follow-up: removing the 2s publish wait removed the only implicit
// throttle on intra-invocation publish volume, so a per-invocation publish
// counter bounds it. These pin the counter's tracked/untracked behavior and
// the per-scope freshness that keeps a nested function_invoke from inheriting
// its caller's count.

func TestAddPublishCount_untrackedReturnsNegative(t *testing.T) {
	if got := AddPublishCount(context.Background(), 1); got != -1 {
		t.Errorf("untracked ctx must return -1 (no enforcement); got %d", got)
	}
	if got := AddPublishCount(nil, 1); got != -1 {
		t.Errorf("nil ctx must return -1; got %d", got)
	}
}

func TestAddPublishCount_tracksAndAccumulates(t *testing.T) {
	ctx := WithPublishCounter(context.Background())
	if got := AddPublishCount(ctx, 1); got != 1 {
		t.Errorf("first publish: got %d, want 1", got)
	}
	if got := AddPublishCount(ctx, 4); got != 5 {
		t.Errorf("after +4: got %d, want 5", got)
	}
	// n<=0 is a no-op (returns -1) and must not change the running total.
	if got := AddPublishCount(ctx, 0); got != -1 {
		t.Errorf("n=0 must return -1 (no-op); got %d", got)
	}
	if got := AddPublishCount(ctx, 1); got != 6 {
		t.Errorf("total must be unaffected by the n=0 call; got %d, want 6", got)
	}
}

func TestWithPublishCounter_freshPerScope(t *testing.T) {
	parent := WithPublishCounter(context.Background())
	AddPublishCount(parent, 10)

	// A nested invocation attaches its own fresh counter and must start at 0.
	child := WithPublishCounter(parent)
	if got := AddPublishCount(child, 1); got != 1 {
		t.Errorf("nested counter must start fresh (independent of parent); got %d", got)
	}
	// Parent total is unaffected by the child.
	if got := AddPublishCount(parent, 1); got != 11 {
		t.Errorf("parent total must be independent of child; got %d, want 11", got)
	}
}
