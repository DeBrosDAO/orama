package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
)

// installedFixture is a fixture with chain, public Kubo, provider and archiver
// installed, and the oramad cosmovisor runs in place.
func installedFixture(t *testing.T) *globalFixture {
	t.Helper()
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceArchiver), f.host); err != nil {
		t.Fatal(err)
	}
	layout := cosmovisor.Layout{Home: f.host.ChainHome, Daemon: constants.ChainDaemonName}
	if err := os.MkdirAll(layout.CurrentBinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	oramad, err := os.ReadFile(filepath.Join(f.staged, "oramad"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.CurrentBinDir(), "oramad"), oramad, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *globalFixture) refreshOptions(services ...GlobalService) RefreshOptions {
	return RefreshOptions{Installed: services, StagedDir: f.staged, Manifest: f.manifest, Stale: staleNone}
}

// staleNone says no running process executes a replaced file.
func staleNone(string) (bool, error) { return false, nil }

// staleUnits says the named units run a replaced file, and records every unit it
// was asked about.
func staleUnits(asked *[]string, units ...string) func(string) (bool, error) {
	return func(unit string) (bool, error) {
		*asked = append(*asked, unit)
		return slices.Contains(units, unit), nil
	}
}

var allInstalled = []GlobalService{GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceArchiver}

func TestRefreshGlobal_replacesOnlyWhatTheReleaseChangedAndRestartsOnlyWhoRunsIt(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "orama-global", "binary orama-global v2")
	var asked []string
	opts := f.refreshOptions(allInstalled...)
	opts.Stale = staleUnits(&asked, constants.GlobalProviderUnit, constants.GlobalArchiverUnit)

	res, err := RefreshGlobal(f.host, opts)
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if !slices.Equal(res.Replaced, []string{"orama-global"}) {
		t.Errorf("replaced %v, want only orama-global", res.Replaced)
	}
	if want := []GlobalService{GlobalServiceProvider, GlobalServiceArchiver}; !slices.Equal(res.Restart, want) {
		t.Errorf("restart %v, want the services that run orama-global %v", res.Restart, want)
	}
	got, err := os.ReadFile(filepath.Join(f.host.BinDir, "orama-global"))
	if err != nil || string(got) != "binary orama-global v2" {
		t.Errorf("installed orama-global = %q, %v", got, err)
	}
	for _, unit := range asked {
		if unit == constants.ChainServiceUnit {
			t.Error("the chain was looked at for a restart: it runs oramad from the cosmovisor layout and is never restarted by a refresh")
		}
	}
}

func TestRefreshGlobal_aChangedCLIRestartsNeitherTheChainNorKubo(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "orama", "binary orama v2")

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if !slices.Equal(res.Replaced, []string{"orama"}) || len(res.Restart) != 0 {
		t.Errorf("replaced %v, restart %v: the CLI is only the chain's pre-start check and Kubo's GC job", res.Replaced, res.Restart)
	}
}

func TestRefreshGlobal_aChangedKuboRestartsTheKuboUnit(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "ipfs", "binary ipfs v2")
	var asked []string
	opts := f.refreshOptions(allInstalled...)
	opts.Stale = staleUnits(&asked, constants.GlobalIPFSUnit)

	res, err := RefreshGlobal(f.host, opts)
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if !slices.Equal(res.Restart, []GlobalService{GlobalServiceIPFS}) {
		t.Errorf("restart %v, want the public Kubo", res.Restart)
	}
}

func TestRefreshGlobal_sameReleaseChangesNothing(t *testing.T) {
	f := installedFixture(t)

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if len(res.Replaced) != 0 || len(res.Restart) != 0 {
		t.Errorf("replaced %v and restarted %v for the release already installed", res.Replaced, res.Restart)
	}
	if res.Chain == nil || res.Chain.Differs() {
		t.Errorf("chain = %+v, want the release's oramad to be the one running", res.Chain)
	}
}

func TestRefreshGlobal_aBinaryNotInTheManifestIsRefusedBeforeAnythingIsReplaced(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "ipfs", "binary ipfs v2")
	f.restage(t, "orama", "binary orama v2")
	// orama-global sorts after both: a refresh that wrote as it went would have
	// replaced ipfs and orama by the time it reached this one.
	if err := os.WriteFile(filepath.Join(f.staged, "orama-global"), []byte("tampered after the manifest"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))

	if err == nil || !strings.Contains(err.Error(), "not the file the release shipped") {
		t.Fatalf("err = %v, want the tampered binary refused", err)
	}
	for name, want := range map[string]string{"ipfs": "binary ipfs", "orama": "binary orama", "orama-global": "binary orama-global"} {
		if got, _ := os.ReadFile(filepath.Join(f.host.BinDir, name)); string(got) != want {
			t.Errorf("installed %s = %q, want it untouched (%q) after a refusal", name, got, want)
		}
	}
}

func TestRefreshGlobal_aServiceStillRunningAReplacedFileIsRestartedEvenWhenNothingIsReplacedNow(t *testing.T) {
	f := installedFixture(t)
	var asked []string
	opts := f.refreshOptions(allInstalled...)
	opts.Stale = staleUnits(&asked, constants.GlobalProviderUnit)

	res, err := RefreshGlobal(f.host, opts)
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if len(res.Replaced) != 0 || !slices.Equal(res.Restart, []GlobalService{GlobalServiceProvider}) {
		t.Errorf("replaced %v, restart %v: a refresh interrupted after it replaced a binary must still restart the service that runs it the next time", res.Replaced, res.Restart)
	}
}

func TestRefreshGlobal_theOnionServiceIsJudgedByItsTxGateNotByTor(t *testing.T) {
	f := installedFixture(t)
	var asked []string
	opts := f.refreshOptions(GlobalServiceOnion)
	opts.Stale = staleUnits(&asked, constants.GlobalTxGateUnit)

	res, err := RefreshGlobal(f.host, opts)
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if !slices.Equal(asked, []string{constants.GlobalTxGateUnit}) {
		t.Errorf("asked %v, want only the tx gate: the onion unit runs the distro's tor, and the gate is what runs the orama CLI", asked)
	}
	if !slices.Equal(res.Restart, []GlobalService{GlobalServiceOnion}) {
		t.Errorf("restart %v, want the onion service (its restart takes the gate with it)", res.Restart)
	}
}

func TestRefreshGlobal_aServiceThatIsNotRunningIsNotStarted(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "orama-global", "binary orama-global v2")

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if len(res.Restart) != 0 {
		t.Errorf("restart %v: a service an operator stopped starts on the new file when they start it, not because of a refresh", res.Restart)
	}
}

func TestRefreshGlobal_aFailedProcessCheckIsAnError(t *testing.T) {
	f := installedFixture(t)
	opts := f.refreshOptions(allInstalled...)
	opts.Stale = func(string) (bool, error) { return false, errors.New("systemctl unavailable") }

	if _, err := RefreshGlobal(f.host, opts); err == nil || !strings.Contains(err.Error(), "systemctl unavailable") {
		t.Fatalf("err = %v, want the failed check reported, not read as a service that needs no restart", err)
	}
}

func fakeRun(answer string, err error) commandRunner {
	return func(string, ...string) ([]byte, error) { return []byte(answer), err }
}

func TestRunningStale_aUnitThatIsNotRunningIsNotStale(t *testing.T) {
	if stale, err := RunningStale(fakeRun("0\n", nil), "x.service"); err != nil || stale {
		t.Fatalf("stale = %v, err = %v", stale, err)
	}
}

func TestRunningStale_aProcessOnItsOwnFileIsNotStale(t *testing.T) {
	if stale, err := RunningStale(fakeRun(strconv.Itoa(os.Getpid())+"\n", nil), "x.service"); err != nil || stale {
		t.Fatalf("stale = %v, err = %v: this test's own executable has not been replaced", stale, err)
	}
}

func TestRunningStale_aReplacedExecutableIsStale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc/<pid>/exe names a replaced file only on Linux")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "svc")
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(copyPath, "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run a copy of sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := os.Rename(copyPath, copyPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(copyPath + ".old"); err != nil {
		t.Fatal(err)
	}

	stale, err := RunningStale(fakeRun(strconv.Itoa(cmd.Process.Pid)+"\n", nil), "x.service")

	if err != nil || !stale {
		t.Fatalf("stale = %v, err = %v: the process runs a file that no longer exists", stale, err)
	}
}

func TestRunningStale_badAnswersAreErrors(t *testing.T) {
	if _, err := RunningStale(fakeRun("", errors.New("no systemctl")), "x.service"); err == nil {
		t.Error("a failed systemctl was read as not stale")
	}
	for _, out := range []string{"", "abc\n", "-3\n"} {
		if _, err := RunningStale(fakeRun(out, nil), "x.service"); err == nil {
			t.Errorf("main pid %q was accepted", out)
		}
	}
}

func TestRefreshGlobal_missingManifestIsAnError(t *testing.T) {
	f := installedFixture(t)
	opts := f.refreshOptions(allInstalled...)
	opts.Manifest = filepath.Join(t.TempDir(), "manifest.json")

	if _, err := RefreshGlobal(f.host, opts); err == nil || !strings.Contains(err.Error(), "release manifest") {
		t.Fatalf("err = %v, want the missing manifest named", err)
	}
}

func TestRefreshGlobal_reportsANewOramadWithoutTouchingTheRunningOne(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "oramad", "binary oramad v2 pinning "+f.verifierSum)

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if res.Chain == nil || !res.Chain.Differs() {
		t.Fatalf("chain = %+v, want the new oramad reported", res.Chain)
	}
	layout := cosmovisor.Layout{Home: f.host.ChainHome, Daemon: constants.ChainDaemonName}
	running, _ := os.ReadFile(filepath.Join(layout.CurrentBinDir(), "oramad"))
	if !strings.HasPrefix(string(running), "binary oramad pinning") {
		t.Errorf("the running oramad is %q: a refresh must never replace the chain binary", running)
	}
	if len(res.Replaced) != 0 {
		t.Errorf("replaced %v: oramad is not a refreshed binary", res.Replaced)
	}
}

func TestRefreshGlobal_withoutTheChainThereIsNoChainState(t *testing.T) {
	f := installedFixture(t)

	res, err := RefreshGlobal(f.host, f.refreshOptions(GlobalServiceProvider))
	if err != nil {
		t.Fatalf("RefreshGlobal: %v", err)
	}

	if res.Chain != nil {
		t.Errorf("chain = %+v, want none on a node without the chain", res.Chain)
	}
}

func TestRefreshGlobal_chainWithoutCosmovisorLayoutIsAnError(t *testing.T) {
	f := installedFixture(t)
	layout := cosmovisor.Layout{Home: f.host.ChainHome, Daemon: constants.ChainDaemonName}
	if err := os.RemoveAll(layout.CurrentBinDir()); err != nil {
		t.Fatal(err)
	}

	if _, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...)); err == nil || !strings.Contains(err.Error(), "oramad cosmovisor runs") {
		t.Fatalf("err = %v, want the missing running oramad named", err)
	}
}

func TestStageChainUpgrade_refusesAnOramadThatDoesNotPinItsVerifier(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "oramad", "binary oramad v2 that pins nothing")

	_, err := StageChainUpgrade(f.host, f.refreshOptions(allInstalled...), "v2")

	if err == nil || !strings.Contains(err.Error(), "does not pin") {
		t.Fatalf("err = %v, want the unpinned verifier refused before anything is staged", err)
	}
}

func TestStageChainUpgrade_refusesAnOramadThatIsNotTheManifests(t *testing.T) {
	f := installedFixture(t)
	if err := os.WriteFile(filepath.Join(f.staged, "oramad"), []byte("tampered pinning "+f.verifierSum), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := StageChainUpgrade(f.host, f.refreshOptions(allInstalled...), "v2")

	if err == nil || !strings.Contains(err.Error(), "not the file the release shipped") {
		t.Fatalf("err = %v, want the tampered oramad refused", err)
	}
}

func TestChainColocated(t *testing.T) {
	dir := t.TempDir()
	if ChainColocated(dir) {
		t.Error("a unit directory without the namespace unit is not co-located")
	}
	if err := os.WriteFile(filepath.Join(dir, "orama-global-netns.service"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !ChainColocated(dir) {
		t.Error("a unit directory with the namespace unit is co-located")
	}
}
