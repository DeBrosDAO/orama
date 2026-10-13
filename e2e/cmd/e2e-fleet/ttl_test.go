package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// TestRunTTL_coversThePlan: the e2e-ttl label is at least the stage plan's
// worst case (the old fixed 6h was far below it), and a shorter E2E_TTL is
// refused.
func TestRunTTL_coversThePlan(t *testing.T) {
	lay, err := findLayout()
	if err != nil {
		t.Fatal(err)
	}
	steps, err := planStages(lay)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(provision.EnvTTL, "")
	ttl, err := runTTL(lay, func(string) (string, bool) { return "", false })
	if err != nil || ttl < stages.WorstCase(steps) || ttl <= provision.DefaultTTL {
		t.Fatalf("ttl %s (worst case %s) err %v", ttl, stages.WorstCase(steps), err)
	}
	if os.Getenv(provision.EnvTTL) != ttl.String() {
		t.Fatalf("E2E_TTL for the broker child is %q", os.Getenv(provision.EnvTTL))
	}
	short := func(string) (string, bool) { return "1h", true }
	if _, err := runTTL(lay, short); err == nil || !strings.Contains(err.Error(), "shorter than the stage plan") {
		t.Fatalf("a TTL below the plan was accepted: %v", err)
	}
	long := (ttl + time.Hour).String()
	if got, err := runTTL(lay, func(string) (string, bool) { return long, true }); err != nil || got.String() != long {
		t.Fatalf("a longer E2E_TTL was not kept: %s %v", got, err)
	}
}
