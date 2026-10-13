package globalcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// refreshFixture is a node with some global services installed, whose systemd answers from a table
// and whose calls (systemctl and the migrate command) are recorded in order.
type refreshFixture struct {
	t       *testing.T
	states  map[install.GlobalService]string
	calls   []string
	migrate func() ([]byte, error)
	life    globalnode.Lifecycle
	host    install.GlobalHost
	out     bytes.Buffer
}

func newRefreshFixture(t *testing.T, states map[install.GlobalService]string) *refreshFixture {
	t.Helper()
	f := &refreshFixture{t: t, states: states}
	dir := t.TempDir()
	for s := range states {
		if err := os.WriteFile(filepath.Join(dir, install.GlobalServiceUnit(s)), []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.migrate = func() ([]byte, error) { return []byte("  indexer: the index is current; kept"), nil }
	f.life = globalnode.Lifecycle{
		UnitDir:        dir,
		WaitChainRPC:   func(context.Context) error { return nil },
		CheckSignFloor: func() error { return nil },
		Out:            &f.out,
		Systemctl:      f.systemctl,
	}
	f.host = install.GlobalHost{BinDir: "/usr/lib/orama-global/bin", Run: func(name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, "run "+name+" "+strings.Join(args, " "))
		return f.migrate()
	}}
	return f
}

func (f *refreshFixture) systemctl(args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	unit := args[len(args)-1]
	for s := range f.states {
		if install.GlobalServiceUnit(s) != unit {
			continue
		}
		switch args[0] {
		case "is-active":
			if f.states[s] != "active" {
				return []byte(f.states[s] + "\n"), errors.New("exit status 3")
			}
			return []byte("active\n"), nil
		case "start":
			f.states[s] = "active"
		case "stop":
			f.states[s] = "inactive"
		}
	}
	return nil, nil
}

func (f *refreshFixture) refresh(restart ...install.GlobalService) error {
	f.t.Helper()
	installed, err := f.life.Installed()
	if err != nil {
		f.t.Fatal(err)
	}
	return restartRefreshed(context.Background(), &f.out, f.life, f.host, installed, restart)
}

// mutating are the calls that change something, in order: systemctl stop and start, and the migrate command.
func (f *refreshFixture) mutating() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "stop ") || strings.HasPrefix(c, "start ") || strings.HasPrefix(c, "run ") {
			out = append(out, strings.ReplaceAll(c, ".service", ""))
		}
	}
	return out
}

func wantCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

const migrateCall = "run runuser -u orama-indexer -- /usr/lib/orama-global/bin/orama-global indexer migrate --home " + constants.GlobalIndexerHome

func TestRestartRefreshed_aReplacedIndexerIsStoppedMigratedAndStarted(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceIndexer: "active",
	})

	if err := f.refresh(install.GlobalServiceIndexer); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, f.mutating(), "stop orama-global-indexer", migrateCall, "start orama-global-indexer")
	if !strings.Contains(f.out.String(), "the index is current; kept") {
		t.Errorf("what the migration did is not shown:\n%s", f.out.String())
	}
}

func TestRestartRefreshed_otherServicesAreStoppedWithItAndStartedAfterTheMigration(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceProvider: "active", install.GlobalServiceIndexer: "active",
	})

	if err := f.refresh(install.GlobalServiceProvider, install.GlobalServiceIndexer); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, f.mutating(),
		"stop orama-global-indexer", "stop orama-global-provider", migrateCall,
		"start orama-global-provider", "start orama-global-indexer")
}

func TestRestartRefreshed_anIndexerThatCrashLoopsOnAnOldIndexIsPutRight(t *testing.T) {
	for _, state := range []string{"activating", "failed"} {
		f := newRefreshFixture(t, map[install.GlobalService]string{
			install.GlobalServiceChain: "active", install.GlobalServiceIndexer: state,
		})

		if err := f.refresh(); err != nil {
			t.Fatalf("%s: %v", state, err)
		}

		wantCalls(t, f.mutating(), "stop orama-global-indexer", migrateCall, "start orama-global-indexer")
	}
}

func TestRestartRefreshed_anIndexerStoppedOnPurposeStaysStopped(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceIndexer: "inactive",
	})

	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, f.mutating(), migrateCall)
}

func TestRestartRefreshed_aRunningIndexerOnTheCurrentBinaryIsLeftAlone(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceIndexer: "active",
	})

	if err := f.refresh(); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, f.mutating())
	if !strings.Contains(f.out.String(), "no running service needs a restart") {
		t.Errorf("output %q", f.out.String())
	}
}

func TestRestartRefreshed_withoutAnIndexerOnlyTheReplacedServicesRestart(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceProvider: "active",
	})

	if err := f.refresh(install.GlobalServiceProvider); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, f.mutating(), "stop orama-global-provider", "start orama-global-provider")
	f2 := newRefreshFixture(t, map[install.GlobalService]string{install.GlobalServiceChain: "active", install.GlobalServiceProvider: "active"})
	if err := f2.refresh(); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, f2.mutating())
}

func TestRestartRefreshed_aFailedMigrationLeavesTheIndexerStoppedAndSaysHowToStartIt(t *testing.T) {
	f := newRefreshFixture(t, map[install.GlobalService]string{
		install.GlobalServiceChain: "active", install.GlobalServiceIndexer: "active",
	})
	f.migrate = func() ([]byte, error) {
		return []byte("the index in /x has layout version 4"), errors.New("exit status 1")
	}

	err := f.refresh(install.GlobalServiceIndexer)

	if err == nil {
		t.Fatal("a migration that failed was reported as done")
	}
	for _, want := range []string{"layout version 4", "orama global start indexer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	wantCalls(t, f.mutating(), "stop orama-global-indexer", migrateCall)
}
