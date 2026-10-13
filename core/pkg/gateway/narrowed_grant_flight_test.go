package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// gatedGrantRegistry holds the first query of a grant lookup (the namespace
// row) until release is closed, so a test can put callers behind a lookup that
// is still in flight. After release it answers as a registry that honours its
// caller's context.
type gatedGrantRegistry struct {
	*countingGrantRegistry
	mu      sync.Mutex
	lookups atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (g *gatedGrantRegistry) Database() client.DatabaseClient { return g }

func (g *gatedGrantRegistry) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	if strings.Contains(query, "FROM namespaces WHERE name") {
		if g.lookups.Add(1) == 1 {
			close(g.entered)
		}
		<-g.release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.countingGrantRegistry.Query(ctx, query, args...)
}

func gatedGateway(t *testing.T) (*Gateway, *gatedGrantRegistry) {
	t.Helper()
	g, registry := namespaceGatewayForHops(t, "runtime")
	gated := &gatedGrantRegistry{countingGrantRegistry: registry, entered: make(chan struct{}), release: make(chan struct{})}
	g.authClient = gated
	return g, gated
}

// followerJoinWindow is how long a test gives callers started behind an
// in-flight lookup to reach the grant read before the lookup is released.
// Callers that share the lookup cannot be observed waiting, so the test waits
// for the opposite: without sharing, each follower's own read shows up in
// lookups within this window and the test fails.
const followerJoinWindow = 300 * time.Millisecond

// letFollowersJoin waits followerJoinWindow, or until want lookups have
// started (the unshared case), whichever is first.
func letFollowersJoin(gated *gatedGrantRegistry, want int32) {
	deadline := time.Now().Add(followerJoinWindow)
	for time.Now().Before(deadline) && gated.lookups.Load() < want {
		time.Sleep(5 * time.Millisecond)
	}
}

var flightObject = auth.Resource{Domain: auth.DomainCache, Action: auth.ActionWrite, Name: "tokens/x"}

// When an entry is missing under load, every caller waits for one registry
// read instead of each paying the round trips.
func TestGrantCache_concurrentMissesShareOneLookup(t *testing.T) {
	g, gated := gatedGateway(t)
	const callers = 8

	statuses := make(chan int, callers)
	serve := func() {
		r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
		status, _ := serveHopAuthorizing(g, r, flightObject)
		statuses <- status
	}
	go serve()
	<-gated.entered
	for i := 1; i < callers; i++ {
		go serve()
	}
	letFollowersJoin(gated, callers)
	close(gated.release)

	for i := 0; i < callers; i++ {
		if status := <-statuses; status != http.StatusOK {
			t.Errorf("caller status %d, want 200", status)
		}
	}
	if n := gated.lookups.Load(); n != 1 {
		t.Errorf("%d grant lookups for %d concurrent callers, want 1", n, callers)
	}
}

// The shared read does not belong to the request that started it: that caller
// hanging up must not fail the lookup for the callers waiting on it.
func TestGrantCache_aCancelledLeaderDoesNotFailItsFollowers(t *testing.T) {
	g, gated := gatedGateway(t)

	ctx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet).WithContext(ctx)
		serveHopAuthorizing(g, r, flightObject)
	}()
	<-gated.entered

	followerStatus := make(chan int, 1)
	go func() {
		r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
		status, _ := serveHopAuthorizing(g, r, flightObject)
		followerStatus <- status
	}()
	letFollowersJoin(gated, 2)
	cancel()
	close(gated.release)
	<-leaderDone

	if status := <-followerStatus; status != http.StatusOK {
		t.Errorf("follower status %d after the leader hung up, want 200", status)
	}
}
