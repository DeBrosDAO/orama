package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const (
	healthUser = "orama"
	healthPass = "0123456789abcdef"
)

// healthyNode serves an rqlite /status and gateway /health. healthy decides
// whether the node reports itself as a follower with a leader.
func healthyNode(t *testing.T, healthy *atomic.Bool) nodehealth.Target {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			if u, p, ok := r.BasicAuth(); !ok || u != healthUser || p != healthPass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			state := "Candidate"
			if healthy.Load() {
				state = "Follower"
			}
			fmt.Fprintf(w, `{"store":{"raft":{"state":%q,"leader_id":"n1","applied_index":5,"commit_index":5}}}`, state)
		case "/health":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	ep, err := rqlite.NewEndpoint(strings.TrimPrefix(srv.URL, "http://"), healthUser, healthPass)
	if err != nil {
		t.Fatal(err)
	}
	return nodehealth.Target{RQLite: ep, GatewayBase: srv.URL}
}

// upgradeFixture is a bin directory holding the old release and a verified
// directory holding the new one, with a stop and start that record calls.
type upgradeFixture struct {
	plan    Plan
	live    string
	log     []string
	healthy atomic.Bool
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	bin, verified := t.TempDir(), t.TempDir()
	f := &upgradeFixture{live: filepath.Join(bin, "orama-node")}
	writeFile(t, f.live, "old release", 0o750)
	next := filepath.Join(verified, "orama-node")
	writeFile(t, next, "new release", 0o644)

	settings := DefaultSettings()
	settings.Mode = ModeAuto
	f.healthy.Store(true)
	f.plan = Plan{
		Settings:      settings,
		Verify:        func() error { f.log = append(f.log, "verify"); return nil },
		Files:         []FileSwap{{Live: f.live, Next: next}},
		Stop:          func() error { f.log = append(f.log, "stop"); return nil },
		Start:         func() error { f.log = append(f.log, "start:"+readFile(t, f.live)); return nil },
		Health:        healthyNode(t, &f.healthy),
		HealthOptions: nodehealth.Options{Budget: time.Millisecond, RequireLeaderKnown: true},
	}
	return f
}

func writeFile(t *testing.T, path, body string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgrade_swapsKeepsThePreviousBinaryAndPassesTheGate(t *testing.T) {
	f := newUpgradeFixture(t)
	rolled, err := Upgrade(context.Background(), f.plan)
	if err != nil || rolled {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	if got := readFile(t, f.live); got != "new release" {
		t.Fatalf("live binary %q", got)
	}
	if got := readFile(t, f.live+PrevSuffix); got != "old release" {
		t.Fatalf("previous binary %q", got)
	}
	info, err := os.Stat(f.live)
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("swapped binary mode %v, %v; want the live binary's 0750", info.Mode(), err)
	}
	if _, err := os.Stat(f.live + NextSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged file left behind: %v", err)
	}
	want := "verify stop start:new release"
	if got := strings.Join(f.log, " "); got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
}

func TestUpgrade_failedGateRollsBackToThePreviousBinary(t *testing.T) {
	f := newUpgradeFixture(t)
	f.plan.Start = func() error {
		f.log = append(f.log, "start:"+readFile(t, f.live))
		f.healthy.Store(readFile(t, f.live) == "old release")
		return nil
	}
	rolled, err := Upgrade(context.Background(), f.plan)
	if err == nil || !rolled {
		t.Fatalf("rolled=%v err=%v, want a rollback", rolled, err)
	}
	if got := readFile(t, f.live); got != "old release" {
		t.Fatalf("live binary after rollback %q", got)
	}
	want := "verify stop start:new release stop start:old release"
	if got := strings.Join(f.log, " "); got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "previous release either") {
		t.Fatalf("the node came back on the old release but was reported unhealthy: %v", err)
	}
}

func TestUpgrade_reportsANodeThatStaysUnhealthyAfterRollback(t *testing.T) {
	f := newUpgradeFixture(t)
	f.healthy.Store(false)
	rolled, err := Upgrade(context.Background(), f.plan)
	if !rolled || err == nil || !strings.Contains(err.Error(), "previous release either") {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	if got := readFile(t, f.live); got != "old release" {
		t.Fatalf("live binary after rollback %q", got)
	}
}

func TestUpgrade_failedStartIsStoppedBeforeTheRollback(t *testing.T) {
	f := newUpgradeFixture(t)
	f.plan.Start = func() error {
		live := readFile(t, f.live)
		f.log = append(f.log, "start:"+live)
		if live == "new release" {
			return errors.New("gateway did not bind")
		}
		return nil
	}
	rolled, err := Upgrade(context.Background(), f.plan)
	if !rolled || err == nil {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	want := "verify stop start:new release stop start:old release"
	if got := strings.Join(f.log, " "); got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
}

func TestUpgrade_unverifiedReleaseChangesNothing(t *testing.T) {
	f := newUpgradeFixture(t)
	f.plan.Verify = func() error { return errors.New("target hash") }
	rolled, err := Upgrade(context.Background(), f.plan)
	if err == nil || rolled {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	if got := readFile(t, f.live); got != "old release" || len(f.log) != 0 {
		t.Fatalf("live %q, calls %v", got, f.log)
	}
}

func TestUpgrade_refusesNotifyAndAValidatorOnAuto(t *testing.T) {
	notify := newUpgradeFixture(t)
	notify.plan.Settings.Mode = ModeNotify
	validator := newUpgradeFixture(t)
	validator.plan.Settings.Role = RoleValidator
	empty := newUpgradeFixture(t)
	empty.plan.Files = nil
	for name, f := range map[string]*upgradeFixture{"notify": notify, "validator": validator, "no files": empty} {
		if _, err := Upgrade(context.Background(), f.plan); err == nil {
			t.Fatalf("%s: upgrade ran", name)
		}
		if len(f.log) != 0 || readFile(t, f.live) != "old release" {
			t.Fatalf("%s: calls %v", name, f.log)
		}
	}
}

func TestFileSwap_newBinaryIsRemovedOnUndo(t *testing.T) {
	dir := t.TempDir()
	next := filepath.Join(dir, "verified")
	writeFile(t, next, "new", 0o644)
	swap := FileSwap{Live: filepath.Join(dir, "turn"), Next: next}
	rolled, err := Apply(swap.Steps(), func() error { return errors.New("unhealthy") })
	if !rolled || err == nil {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	for _, p := range []string{swap.Live, swap.Live + NextSuffix, swap.Live + PrevSuffix} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the rollback of a new binary: %v", p, err)
		}
	}
}

func TestDecide_validatorNeverAuto(t *testing.T) {
	s := DefaultSettings()
	s.Role = RoleValidator
	s.Mode = ModeAuto
	if _, err := Decide(s, healthy(), time.Now(), "1.0.0", Candidate{Version: "1.1.0"}, nil); err == nil {
		t.Fatal("a validator was allowed auto")
	}
	s.Mode = ModeNotify
	d, err := Decide(s, healthy(), time.Now(), "1.0.0", Candidate{Version: "1.1.0", Channel: "stable"}, nil)
	if err != nil || d.Action != ActionNotify {
		t.Fatalf("validator on notify: %+v %v", d, err)
	}
	s.Role = ""
	if _, err := Decide(s, healthy(), time.Now(), "1.0.0", Candidate{Version: "1.1.0"}, nil); err == nil {
		t.Fatal("an empty role was accepted")
	}
}
