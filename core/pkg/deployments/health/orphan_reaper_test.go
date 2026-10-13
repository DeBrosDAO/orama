package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

type fakeUnits struct {
	units    []process.RuntimeUnit
	listErr  error
	stopErr  error
	stopped  []string
	state    []string // what StateInstances lists
	purged   []string
	building []string // instances with an active build or clean unit
}

func (f *fakeUnits) ActiveBuildInstances(context.Context) ([]string, error) { return f.building, nil }

func (f *fakeUnits) StateInstances() ([]string, error) { return f.state, nil }

func (f *fakeUnits) PurgeOrphanState(instance string) error {
	f.purged = append(f.purged, instance)
	return nil
}

func (f *fakeUnits) ListRuntimeUnits(context.Context) ([]process.RuntimeUnit, error) {
	return f.units, f.listErr
}

func (f *fakeUnits) StopOrphan(_ process.Runtime, instance string) error {
	f.stopped = append(f.stopped, instance)
	return f.stopErr
}

func oldUnit(runtime process.Runtime, instance string) process.RuntimeUnit {
	return process.RuntimeUnit{
		Unit: "orama-deploy-" + string(runtime) + "@" + instance + ".service", Runtime: runtime,
		Instance: instance, Since: time.Now().Add(-2 * orphanMinAge),
	}
}

func registryWith(rows ...deploymentKey) *mockDB {
	return &mockDB{queryFunc: func(dest interface{}, _ string, _ ...interface{}) error {
		*dest.(*[]deploymentKey) = rows
		return nil
	}}
}

func newReaper(db *mockDB, units *fakeUnits) *HealthChecker {
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
	hc.SetOrphanReaper(units, "")
	return hc
}

var liveRow = deploymentKey{Namespace: "acme", Name: "web"}

func TestReapOrphanUnits_stoppedOnSecondSweep(t *testing.T) {
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app"), oldUnit(process.RuntimeNode, "acme-web")}}
	hc := newReaper(registryWith(liveRow), units)

	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v on the first sighting", units.stopped)
	}
	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(units.stopped) != 1 || units.stopped[0] != "gone-app" {
		t.Fatalf("stopped %v, want only [gone-app]", units.stopped)
	}
}

func TestReapOrphanUnits_dottedNameMatchesItsRow(t *testing.T) {
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeGo, "acme-my-app")}}
	hc := newReaper(registryWith(deploymentKey{Namespace: "acme", Name: "my.app"}), units)
	for i := 0; i < 3; i++ {
		_ = hc.reapOrphanUnits(context.Background())
	}
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v, a unit with a row", units.stopped)
	}
}

func TestReapOrphanUnits_unreadableRegistryStopsNothingAndResets(t *testing.T) {
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app")}}
	db := registryWith(liveRow)
	hc := newReaper(db, units)
	_ = hc.reapOrphanUnits(context.Background()) // first sighting

	db.queryFunc = func(interface{}, string, ...interface{}) error { return errors.New("rqlite down") }
	if err := hc.reapOrphanUnits(context.Background()); err == nil {
		t.Fatal("an unreadable registry was not reported")
	}
	db.queryFunc = registryWith(liveRow).queryFunc
	_ = hc.reapOrphanUnits(context.Background()) // counts as a first sighting again
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v across a registry failure", units.stopped)
	}
}

func TestReapOrphanUnits_emptyRegistryStopsNothing(t *testing.T) {
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app")}}
	hc := newReaper(registryWith(), units)
	for i := 0; i < 3; i++ {
		if err := hc.reapOrphanUnits(context.Background()); err == nil {
			t.Fatal("an empty registry was not reported")
		}
	}
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v with an empty registry", units.stopped)
	}
}

func TestReapOrphanUnits_youngUnitSkipped(t *testing.T) {
	young := oldUnit(process.RuntimeNode, "new-app")
	young.Since = time.Now().Add(-orphanMinAge / 2)
	units := &fakeUnits{units: []process.RuntimeUnit{young}}
	hc := newReaper(registryWith(liveRow), units)
	for i := 0; i < 3; i++ {
		_ = hc.reapOrphanUnits(context.Background())
	}
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v, a unit younger than the minimum age", units.stopped)
	}
}

func TestReapOrphanUnits_capPerSweep(t *testing.T) {
	units := &fakeUnits{}
	for _, n := range []string{"a-one", "a-two", "a-three", "a-four", "a-five"} {
		units.units = append(units.units, oldUnit(process.RuntimeNode, n))
	}
	hc := newReaper(registryWith(liveRow), units)

	_ = hc.reapOrphanUnits(context.Background())
	_ = hc.reapOrphanUnits(context.Background())
	if len(units.stopped) != maxOrphansPerSweep {
		t.Fatalf("stopped %d in one sweep, want %d", len(units.stopped), maxOrphansPerSweep)
	}
	units.units = units.units[maxOrphansPerSweep:]
	_ = hc.reapOrphanUnits(context.Background())
	if len(units.stopped) != 2*maxOrphansPerSweep {
		t.Fatalf("the carried candidates were not taken on the next sweep: %v", units.stopped)
	}
}

func TestReapOrphanUnits_stopErrorSurfaces(t *testing.T) {
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app")}, stopErr: errors.New("helper refused")}
	hc := newReaper(registryWith(liveRow), units)
	_ = hc.reapOrphanUnits(context.Background())
	err := hc.reapOrphanUnits(context.Background())
	if err == nil || !strings.Contains(err.Error(), "helper refused") {
		t.Fatalf("stop error not surfaced: %v", err)
	}
}

func TestReapOrphanUnits_listErrorSurfacesWithoutStopping(t *testing.T) {
	units := &fakeUnits{listErr: errors.New("systemctl failed")}
	hc := newReaper(registryWith(liveRow), units)
	if err := hc.reapOrphanUnits(context.Background()); err == nil {
		t.Fatal("a listing failure was swallowed")
	}
}

func TestReapOrphanUnits_crashLoopStoppedAfterBeingACandidateLongEnough(t *testing.T) {
	clock := time.Now()
	young := oldUnit(process.RuntimeNode, "gone-app")
	units := &fakeUnits{units: []process.RuntimeUnit{young}}
	hc := newReaper(registryWith(liveRow), units)
	hc.now = func() time.Time { return clock }

	for i := 0; i < 3; i++ {
		units.units[0].Since = clock.Add(-time.Second) // restarted a second ago, every time
		_ = hc.reapOrphanUnits(context.Background())
		clock = clock.Add(orphanMinAge / 3)
	}
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v before it had been a candidate for the minimum age", units.stopped)
	}
	units.units[0].Since = clock.Add(-time.Second)
	_ = hc.reapOrphanUnits(context.Background())
	if len(units.stopped) != 1 {
		t.Fatalf("a crash-looping orphan was never stopped: %v", units.stopped)
	}
}

func TestReapOrphanUnits_unitThatGainsARowIsNeverStopped(t *testing.T) {
	clock := time.Now()
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "new-app")}}
	units.units[0].Since = clock
	db := registryWith(liveRow)
	hc := newReaper(db, units)
	hc.now = func() time.Time { return clock }

	_ = hc.reapOrphanUnits(context.Background())
	clock = clock.Add(orphanMinAge / 2)
	db.queryFunc = registryWith(liveRow, deploymentKey{Namespace: "new", Name: "app"}).queryFunc
	_ = hc.reapOrphanUnits(context.Background())
	// The row goes away again: the sighting must start over, not resume.
	db.queryFunc = registryWith(liveRow).queryFunc
	clock = clock.Add(orphanMinAge / 2)
	units.units[0].Since = clock
	_ = hc.reapOrphanUnits(context.Background())
	if len(units.stopped) != 0 {
		t.Fatalf("stopped %v, a unit that had a row", units.stopped)
	}
}

// TestReapOrphanUnits_removesTheStoppedOrphansFiles: a later deployment given
// the same instance must not start on the orphan's stale code; a unit that
// could not be stopped keeps its files, which it is still running from.
func TestReapOrphanUnits_removesTheStoppedOrphansFiles(t *testing.T) {
	base := t.TempDir()
	for _, inst := range []string{"gone-app", "acme-web"} {
		if err := os.MkdirAll(filepath.Join(base, inst, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app"), oldUnit(process.RuntimeNode, "acme-web")}}
	hc := NewHealthChecker(registryWith(liveRow), zap.NewNop(), "node-1", nil)
	hc.SetOrphanReaper(units, base)
	for i := 0; i < 2; i++ {
		if err := hc.reapOrphanUnits(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "gone-app")); !os.IsNotExist(err) {
		t.Errorf("the orphan's directory is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "acme-web", "app")); err != nil {
		t.Errorf("a live deployment's files were touched: %v", err)
	}
}

func TestReapOrphanUnits_aFailedStopKeepsTheFiles(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "gone-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	units := &fakeUnits{units: []process.RuntimeUnit{oldUnit(process.RuntimeNode, "gone-app")}, stopErr: errors.New("refused")}
	hc := NewHealthChecker(registryWith(liveRow), zap.NewNop(), "node-1", nil)
	hc.SetOrphanReaper(units, base)
	_ = hc.reapOrphanUnits(context.Background())
	if err := hc.reapOrphanUnits(context.Background()); err == nil {
		t.Fatal("a refused stop was not reported")
	}
	if _, err := os.Stat(filepath.Join(base, "gone-app")); err != nil {
		t.Errorf("the files of a unit still running were removed: %v", err)
	}
}

func TestRemoveOrphanFiles_refusesAnInstanceThatLeavesTheBase(t *testing.T) {
	hc := NewHealthChecker(registryWith(liveRow), zap.NewNop(), "node-1", nil)
	hc.SetOrphanReaper(&fakeUnits{}, t.TempDir())
	for _, inst := range []string{"../x", "a/b", ""} {
		if err := hc.removeOrphanFiles(inst); err == nil {
			t.Errorf("instance %q was accepted", inst)
		}
	}
}

// A directory left by a deployment that no longer exists is removed on the
// second sweep, after orphanMinAge; one with a row, or with a unit, never.
func TestReapOrphanState_removesOnlyWhatNoDeploymentOwns(t *testing.T) {
	units := &fakeUnits{
		units: []process.RuntimeUnit{{Unit: "orama-deploy-go@still-running.service", Runtime: process.RuntimeGo, Instance: "still-running", Since: time.Now()}},
		state: []string{"acme-web", "gone-app", "still-running"},
	}
	hc := newReaper(registryWith(liveRow), units)
	now := time.Now()
	hc.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if err := hc.reapOrphanUnits(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(units.purged) != 0 {
		t.Fatalf("purged %v before the minimum age", units.purged)
	}
	now = now.Add(orphanMinAge + time.Minute)
	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(units.purged) != 1 || units.purged[0] != "gone-app" {
		t.Fatalf("purged %v, want only gone-app", units.purged)
	}
}

func TestReapOrphanState_aDirectoryNotSeenTwiceIsKept(t *testing.T) {
	units := &fakeUnits{state: []string{"gone-app"}}
	hc := newReaper(registryWith(liveRow), units)
	now := time.Now()
	hc.now = func() time.Time { return now }
	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	units.state = nil // gone from the listing, then back: the clock starts over
	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	units.state = []string{"gone-app"}
	if err := hc.reapOrphanUnits(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(units.purged) != 0 {
		t.Errorf("purged %v", units.purged)
	}
}

// A unit listing that failed is not a listing: the directory of an instance
// whose unit could not be seen is left alone, however long it has been a
// candidate.
func TestReapOrphanState_aFailedUnitListingLeavesStateAlone(t *testing.T) {
	units := &fakeUnits{state: []string{"gone-app"}, listErr: errors.New("systemctl timed out")}
	hc := newReaper(registryWith(liveRow), units)
	now := time.Now()
	hc.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		_ = hc.reapOrphanUnits(context.Background())
		now = now.Add(orphanMinAge + time.Minute)
	}
	if len(units.purged) != 0 {
		t.Fatalf("purged %v after a failed unit listing", units.purged)
	}
}

// A registry that cannot be read resets the state candidates too, so the clock
// of a directory starts over once the registry is back.
func TestReapOrphanState_unreadableRegistryResetsTheStateCandidates(t *testing.T) {
	units := &fakeUnits{state: []string{"gone-app"}}
	db := registryWith(liveRow)
	hc := newReaper(db, units)
	now := time.Now()
	hc.now = func() time.Time { return now }
	_ = hc.reapOrphanUnits(context.Background()) // first sighting
	db.queryFunc = func(interface{}, string, ...interface{}) error { return errors.New("rqlite down") }
	_ = hc.reapOrphanUnits(context.Background())
	db.queryFunc = registryWith(liveRow).queryFunc
	now = now.Add(orphanMinAge + time.Minute)
	_ = hc.reapOrphanUnits(context.Background()) // a first sighting again
	if len(units.purged) != 0 {
		t.Fatalf("purged %v across a registry failure", units.purged)
	}
}

// A first deploy builds before its row exists: its build cache is not an
// orphan's while the build or clean unit runs.
func TestReapOrphanState_anActiveBuildUnitKeepsTheCache(t *testing.T) {
	units := &fakeUnits{state: []string{"first-deploy", "gone-app"}, building: []string{"first-deploy"}}
	hc := newReaper(registryWith(liveRow), units)
	now := time.Now()
	hc.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if err := hc.reapOrphanUnits(context.Background()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(orphanMinAge + time.Minute)
	}
	if len(units.purged) != 1 || units.purged[0] != "gone-app" {
		t.Fatalf("purged %v, want only gone-app", units.purged)
	}
}
