package install

import (
	"os"
	"path/filepath"
	"slices"
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
	return RefreshOptions{Installed: services, StagedDir: f.staged, Manifest: f.manifest}
}

var allInstalled = []GlobalService{GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceArchiver}

func TestRefreshGlobal_replacesOnlyWhatTheReleaseChangedAndRestartsOnlyWhoRunsIt(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "orama-global", "binary orama-global v2")

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
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

	res, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))
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

func TestRefreshGlobal_aBinaryNotInTheManifestIsRefusedAndNothingIsReplaced(t *testing.T) {
	f := installedFixture(t)
	f.restage(t, "orama-global", "binary orama-global v2")
	if err := os.WriteFile(filepath.Join(f.staged, "orama"), []byte("tampered after the manifest"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := RefreshGlobal(f.host, f.refreshOptions(allInstalled...))

	if err == nil || !strings.Contains(err.Error(), "not the file the release shipped") {
		t.Fatalf("err = %v, want the tampered binary refused", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.host.BinDir, "orama"))
	if string(got) != "binary orama" {
		t.Errorf("installed orama = %q: a binary that is not the release's was installed", got)
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
