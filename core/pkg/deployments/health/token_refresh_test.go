package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

func refreshRows(rows ...deploymentRow) *mockDB {
	return &mockDB{queryFunc: func(dest interface{}, _ string, _ ...interface{}) error {
		*(dest.(*[]deploymentRow)) = rows
		return nil
	}}
}

func refreshChecker(db *mockDB, pm *mockProcessManager) *HealthChecker {
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", pm)
	hc.refreshSpacing = 0
	return hc
}

// Every deployment with an active replica here is given a fresh staged
// credential, so a start systemd makes on its own finds one with life in it.
func TestRefreshTokens_everyLocalDeploymentIsRefreshed(t *testing.T) {
	pm := &mockProcessManager{}
	hc := refreshChecker(refreshRows(
		deploymentRow{ID: "1", Namespace: "acme", Name: "api", Type: "nodejs-backend", Port: 10200},
		deploymentRow{ID: "2", Namespace: "acme", Name: "web", Type: "nextjs", Port: 10201},
	), pm)

	refreshed, failed, err := hc.refreshTokens(context.Background())

	if err != nil || refreshed != 2 || failed != 0 || len(pm.refreshCalls) != 2 || pm.refreshCalls[0] != "acme/api" {
		t.Errorf("refreshed %d, failed %d, err %v, calls %v", refreshed, failed, err, pm.refreshCalls)
	}
}

// One deployment that cannot be refreshed (a deleted or stopped one, a failed
// mint) does not stop the others from being.
func TestRefreshTokens_aFailureDoesNotStopTheOthers(t *testing.T) {
	pm := &mockProcessManager{refreshErrs: map[string]error{
		"gone":    process.ErrDeploymentGone,
		"stopped": process.ErrStopped,
		"down":    errors.New("the registry did not answer"),
	}}
	hc := refreshChecker(refreshRows(
		deploymentRow{ID: "1", Namespace: "acme", Name: "gone", Port: 10200},
		deploymentRow{ID: "2", Namespace: "acme", Name: "stopped", Port: 10201},
		deploymentRow{ID: "3", Namespace: "acme", Name: "down", Port: 10202},
		deploymentRow{ID: "4", Namespace: "acme", Name: "ok", Port: 10203},
	), pm)

	refreshed, failed, err := hc.refreshTokens(context.Background())

	if err != nil || refreshed != 1 || failed != 3 || len(pm.refreshCalls) != 4 {
		t.Errorf("refreshed %d, failed %d, err %v, calls %v", refreshed, failed, err, pm.refreshCalls)
	}
}

func TestRefreshTokens_nothingRunningHereRefreshesNothing(t *testing.T) {
	pm := &mockProcessManager{}
	hc := refreshChecker(refreshRows(), pm)
	if refreshed, failed, err := hc.refreshTokens(context.Background()); err != nil || refreshed != 0 || failed != 0 || len(pm.refreshCalls) != 0 {
		t.Errorf("refreshed %d, failed %d, err %v, calls %v", refreshed, failed, err, pm.refreshCalls)
	}
}

// An unreadable deployment list refreshes nothing and is an error, so the
// first sweep knows to try again.
func TestRefreshTokens_anUnreadableListIsAnError(t *testing.T) {
	pm := &mockProcessManager{}
	hc := refreshChecker(&mockDB{queryFunc: func(interface{}, string, ...interface{}) error { return errors.New("no leader") }}, pm)
	if refreshed, _, err := hc.refreshTokens(context.Background()); err == nil || refreshed != 0 || len(pm.refreshCalls) != 0 {
		t.Errorf("refreshed %d, err %v, calls %v", refreshed, err, pm.refreshCalls)
	}
}

// The boot sweep keeps listing until the registry answers.
func TestRunTokenRefresh_theFirstSweepRetriesTheListing(t *testing.T) {
	pm := &mockProcessManager{}
	lists := 0
	db := &mockDB{queryFunc: func(dest interface{}, _ string, _ ...interface{}) error {
		lists++
		if lists < 3 {
			return errors.New("no leader")
		}
		*(dest.(*[]deploymentRow)) = []deploymentRow{{ID: "1", Namespace: "acme", Name: "api", Port: 10200}}
		return nil
	}}
	hc := refreshChecker(db, pm)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { hc.runTokenRefresh(ctx); close(done) }()

	deadline := time.After(10 * time.Second)
	for {
		pm.mu.Lock()
		n := len(pm.refreshCalls)
		pm.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no refresh after %d listings", lists)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if lists < 3 {
		t.Errorf("listed %d times, want at least 3", lists)
	}
}

// The sweep is paced and stops with its context.
func TestRefreshTokens_stopsWhenItsContextEnds(t *testing.T) {
	pm := &mockProcessManager{}
	hc := refreshChecker(refreshRows(
		deploymentRow{ID: "1", Namespace: "acme", Name: "a", Port: 10200},
		deploymentRow{ID: "2", Namespace: "acme", Name: "b", Port: 10201},
	), pm)
	hc.refreshSpacing = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	refreshed, _, err := hc.refreshTokens(ctx)
	if !errors.Is(err, context.Canceled) || refreshed != 1 {
		t.Errorf("refreshed %d, err %v; want the first refreshed and the sweep cancelled", refreshed, err)
	}
}
