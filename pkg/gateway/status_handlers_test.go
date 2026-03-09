package gateway

import "testing"

func TestAggregateHealthStatus_allHealthy(t *testing.T) {
	checks := map[string]checkResult{
		"rqlite": {Status: "ok"},
		"olric":  {Status: "ok"},
		"ipfs":   {Status: "ok"},
		"libp2p": {Status: "ok"},
		"anyone": {Status: "ok"},
	}
	if got := aggregateHealthStatus(checks); got != "healthy" {
		t.Errorf("expected healthy, got %s", got)
	}
}

func TestAggregateHealthStatus_rqliteError(t *testing.T) {
	checks := map[string]checkResult{
		"rqlite": {Status: "error", Error: "connection refused"},
		"olric":  {Status: "ok"},
		"ipfs":   {Status: "ok"},
	}
	if got := aggregateHealthStatus(checks); got != "unhealthy" {
		t.Errorf("expected unhealthy, got %s", got)
	}
}

func TestAggregateHealthStatus_nonCriticalError(t *testing.T) {
	checks := map[string]checkResult{
		"rqlite": {Status: "ok"},
		"olric":  {Status: "error", Error: "timeout"},
		"ipfs":   {Status: "ok"},
	}
	if got := aggregateHealthStatus(checks); got != "degraded" {
		t.Errorf("expected degraded, got %s", got)
	}
}

func TestAggregateHealthStatus_unavailableIsNotError(t *testing.T) {
	// Key test: "unavailable" services (like Anyone in sandbox) should NOT
	// cause degraded status.
	checks := map[string]checkResult{
		"rqlite": {Status: "ok"},
		"olric":  {Status: "ok"},
		"ipfs":   {Status: "unavailable"},
		"libp2p": {Status: "unavailable"},
		"anyone": {Status: "unavailable"},
	}
	if got := aggregateHealthStatus(checks); got != "healthy" {
		t.Errorf("expected healthy when services are unavailable, got %s", got)
	}
}

func TestAggregateHealthStatus_emptyChecks(t *testing.T) {
	checks := map[string]checkResult{}
	if got := aggregateHealthStatus(checks); got != "healthy" {
		t.Errorf("expected healthy for empty checks, got %s", got)
	}
}

func TestAggregateHealthStatus_rqliteErrorOverridesDegraded(t *testing.T) {
	// rqlite error should take priority over other errors
	checks := map[string]checkResult{
		"rqlite": {Status: "error", Error: "leader not found"},
		"olric":  {Status: "error", Error: "timeout"},
		"anyone": {Status: "error", Error: "not reachable"},
	}
	if got := aggregateHealthStatus(checks); got != "unhealthy" {
		t.Errorf("expected unhealthy (rqlite takes priority), got %s", got)
	}
}
