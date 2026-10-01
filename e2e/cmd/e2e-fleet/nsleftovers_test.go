package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/nsledger"
)

var (
	leakedAt = time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	quick    = nsledger.Options{Interval: time.Millisecond, Budget: 100 * time.Millisecond}
)

// ledgerOf writes a package ledger at <root>/<sub>/<package>/namespaces.ledger.
func ledgerOf(t *testing.T, root, sub, pkg string, at time.Time, names ...string) string {
	t.Helper()
	dir := filepath.Join(root, sub, pkg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := nsledger.Record(dir, n, at); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, nsledger.FileName)
}

func TestLedgerPaths_everyPackageAndRerunOfEveryRoot(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	want := []string{
		ledgerOf(t, a, "evidence", "stage-05-serverless", leakedAt, "e2e-1"),
		ledgerOf(t, a, "rerun/evidence", "001-serverless", leakedAt, "e2e-2"),
		ledgerOf(t, b, "evidence", "stage-06-webrtc", leakedAt, "e2e-3"),
	}
	if err := os.MkdirAll(filepath.Join(a, "evidence", "stage-07-none"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ledgerPaths([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("ledgers %v, want %v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("ledger %s not found in %v", w, got)
		}
	}
}

func TestLedgerRoots_stagenetTakesEveryStagenetRunOthersOnlyTheirOwn(t *testing.T) {
	arts := t.TempDir()
	for _, d := range []string{"stagenet-20261001-000001", "stagenet-20261001-000002", "devnet-x"} {
		if err := os.MkdirAll(filepath.Join(arts, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stage := &fleet.State{Target: config.TargetStagenet, ArtifactDir: filepath.Join(arts, "stagenet-20261001-000001")}
	roots, err := ledgerRoots(stage)
	if err != nil || len(roots) != 2 || strings.Contains(strings.Join(roots, ","), "devnet") {
		t.Fatalf("stagenet roots %v %v", roots, err)
	}
	own := &fleet.State{ArtifactDir: filepath.Join(arts, "devnet-x")}
	if roots, err := ledgerRoots(own); err != nil || len(roots) != 1 || roots[0] != own.ArtifactDir {
		t.Fatalf("fleet roots %v %v", roots, err)
	}
}

func TestSweepNamespaces_removesOnlyWhatIsOlderThanTheCutoff(t *testing.T) {
	root := t.TempDir()
	old := ledgerOf(t, root, "evidence", "stage-05-a", leakedAt, "e2e-old")
	ledgerOf(t, root, "evidence", "stage-05-b", leakedAt.Add(5*time.Hour), "e2e-new")
	var removed []string
	remove := func(_ context.Context, n string) error { removed = append(removed, n); return nil }
	err := sweepNamespaces(context.Background(), sweepInput{ledgers: mustPaths(t, root), cutoff: leakedAt.Add(time.Hour), remove: remove, opt: quick})
	if err != nil || strings.Join(removed, ",") != "e2e-old" {
		t.Fatalf("removed %v, err %v; want only e2e-old", removed, err)
	}
	if got, _ := nsledger.Pending(old); len(got) != 0 {
		t.Fatalf("the removed namespace stays in its ledger: %+v", got)
	}
}

func mustPaths(t *testing.T, root string) []string {
	t.Helper()
	p, err := ledgerPaths([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSweepNamespaces_listedAddsTheOnesNoLedgerNames(t *testing.T) {
	root := t.TempDir()
	ledgerOf(t, root, "evidence", "stage-05-a", leakedAt, "e2e-ledgered")
	var removed []string
	remove := func(_ context.Context, n string) error { removed = append(removed, n); return nil }
	listed := func(context.Context) ([]string, error) { return []string{"e2e-orphan"}, nil }
	err := sweepNamespaces(context.Background(), sweepInput{ledgers: mustPaths(t, root), cutoff: leakedAt.Add(time.Hour), remove: remove, listed: listed, opt: quick})
	if err != nil || strings.Join(removed, ",") != "e2e-ledgered,e2e-orphan" {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	removed = nil
	if err := sweepNamespaces(context.Background(), sweepInput{cutoff: leakedAt, remove: remove, opt: quick}); err != nil || removed != nil {
		t.Fatalf("without --listed nothing else is touched: %v %v", removed, err)
	}
}

func TestSweepNamespaces_reportsWhatItCouldNotRemove(t *testing.T) {
	root := t.TempDir()
	ledgerOf(t, root, "evidence", "stage-05-a", leakedAt, "e2e-stuck", "e2e-fine")
	remove := func(_ context.Context, n string) error {
		if n == "e2e-stuck" {
			return errors.New("HTTP 500")
		}
		return nil
	}
	err := sweepNamespaces(context.Background(), sweepInput{ledgers: mustPaths(t, root), cutoff: leakedAt.Add(time.Hour), remove: remove, opt: quick})
	if err == nil || !strings.Contains(err.Error(), "e2e-stuck may be leaked") {
		t.Fatalf("err %v", err)
	}
}
