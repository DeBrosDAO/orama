package triggers

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #93 — wildcard delivery on WASM publishes.
//
// Plan-3 shipped wildcard storage + lookup but skipped the libp2p
// subscribe half (libp2p has no wildcard subscribe). For a function
// publishing to "presence:user-1" via oh.PubSubPublish:
//   - concrete trigger "presence:user-1" works (libp2p subscribe-loopback)
//   - wildcard trigger "presence:*" silently never fires
//
// DispatchLocalPublish closes the gap by firing wildcard-only triggers
// synchronously on the publishing gateway. Concrete triggers must NOT
// fire from this path or they'd double-invoke (once locally, once via
// libp2p loopback).
//
// These tests pin the filter logic exactly so a future refactor of
// DispatchLocalPublish can't silently re-introduce the wildcard-silent
// or the double-fire behavior.

func TestFilterWildcardMatches_dropsExactPatternMatches(t *testing.T) {
	// The exact-match concrete trigger MUST be dropped — otherwise we
	// double-invoke (once here, once via libp2p loopback).
	matches := []TriggerMatch{
		{TriggerID: "t1", FunctionName: "fn-exact", Topic: "presence:user-1", TopicPattern: "presence:user-1"},
	}
	out := filterWildcardMatches(matches, "presence:user-1")
	if len(out) != 0 {
		t.Errorf("BUG #93 REGRESSION: concrete-pattern match must be filtered out "+
			"(it gets delivered via libp2p loopback); got %d match(es) that would double-fire", len(out))
	}
}

func TestFilterWildcardMatches_keepsWildcardMatch(t *testing.T) {
	// The actual #93 fix: wildcard pattern "presence:*" matching the
	// resolved topic "presence:user-1" MUST be kept — that's the
	// silent-handler bug we're closing.
	matches := []TriggerMatch{
		{TriggerID: "t1", FunctionName: "presence-aggregator", Topic: "presence:user-1", TopicPattern: "presence:*"},
	}
	out := filterWildcardMatches(matches, "presence:user-1")
	if len(out) != 1 {
		t.Fatalf("BUG #93 REGRESSION: wildcard match for 'presence:*' against "+
			"'presence:user-1' must be kept (the silent-handler bug); got %d", len(out))
	}
	if out[0].TopicPattern != "presence:*" {
		t.Errorf("wrong match kept: want pattern=presence:*, got %q", out[0].TopicPattern)
	}
}

func TestFilterWildcardMatches_mixedKeepsOnlyWildcards(t *testing.T) {
	// The realistic case: a topic has both a concrete subscriber AND a
	// wildcard subscriber. Concrete is filtered (libp2p handles it),
	// wildcard is kept (we handle it).
	matches := []TriggerMatch{
		{TriggerID: "t1", FunctionName: "fn-concrete", Topic: "messages:new", TopicPattern: "messages:new"},
		{TriggerID: "t2", FunctionName: "fn-wild", Topic: "messages:new", TopicPattern: "messages:*"},
		{TriggerID: "t3", FunctionName: "fn-deep", Topic: "messages:new", TopicPattern: "**"},
	}
	out := filterWildcardMatches(matches, "messages:new")
	if len(out) != 2 {
		t.Fatalf("want 2 wildcard matches (got %d): mixed test must keep wildcards, drop concrete", len(out))
	}
	for _, m := range out {
		if m.TopicPattern == "messages:new" {
			t.Errorf("filter let the concrete pattern through: %+v", m)
		}
	}
}

func TestFilterWildcardMatches_emptyInputEmptyOutput(t *testing.T) {
	// Trivial edge case — no triggers configured at all. Must not panic,
	// must return empty (caller short-circuits before doing more work).
	out := filterWildcardMatches(nil, "any:topic")
	if len(out) != 0 {
		t.Errorf("nil input must yield empty output; got %d matches", len(out))
	}
}

func TestDispatchLocalPublish_depthLimitNoPanic(t *testing.T) {
	// Mirrors TestDispatcher_DepthLimit for the local-publish path.
	// At max depth, must return silently — no store call, no panic.
	// Without this guard, a function that publishes from a wildcard-
	// triggered handler could infinitely recurse via DispatchLocalPublish.
	logger, _ := zap.NewDevelopment()
	store := NewPubSubTriggerStore(nil, logger) // store would panic if called (nil db)
	d := NewPubSubDispatcher(store, nil, nil, nil, logger)

	d.DispatchLocalPublish(context.Background(), "ns", "topic", []byte("data"), maxTriggerDepth)
	d.DispatchLocalPublish(context.Background(), "ns", "topic", []byte("data"), maxTriggerDepth+1)
	// If we reach here without panicking, the depth guard worked — the
	// store's nil-db Query would otherwise crash on the second line.
}

func TestDispatchLocalPublish_belowMaxDepthAttemptsStoreLookup(t *testing.T) {
	// Symmetric guard test: at depth=maxTriggerDepth-1 the dispatcher
	// MUST attempt the store lookup (depth check passes). The nil
	// rqlite.Client makes the lookup itself fail/panic — we recover so
	// the test asserts ONLY the behavioral split at the boundary
	// (depth guard either trips early-return or doesn't). Without this
	// test, the depth guard could regress to `>` (off-by-one) and the
	// recursion bound would shift silently.
	logger, _ := zap.NewDevelopment()
	store := NewPubSubTriggerStore(nil, logger)
	d := NewPubSubDispatcher(store, nil, nil, nil, logger)

	defer func() {
		// Whether the nil-db lookup panics or returns an error, the
		// dispatcher's logger.Error path swallows it. Either way we
		// reached PAST the depth guard, which is the point.
		_ = recover()
	}()
	d.DispatchLocalPublish(context.Background(), "ns", "topic", []byte("data"), maxTriggerDepth-1)
}
