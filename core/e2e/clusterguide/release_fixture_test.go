package clusterguide

import (
	"context"
	"strings"
	"testing"
)

func releaseFixture() *Fixture {
	fx := testFixture()
	fx.Archive = ""
	fx.Release, fx.ReleaseRepo, fx.ReleaseRoot = "0.3.2", "https://releases.run.test.dev/tuf", "/keys/root.json"
	return fx
}

func TestExecute_aReleaseFixtureInstallsTheRelease(t *testing.T) {
	ex := happyExec()
	if _, err := (Runner{Exec: ex, Fx: releaseFixture()}).Execute(context.Background(), guideCommands(t), Plan()); err != nil {
		t.Fatal(err)
	}
	genesis := ex.lines()[0]
	for _, want := range []string{"--release 0.3.2", "--release-repo https://releases.run.test.dev/tuf", "--release-root /keys/root.json"} {
		if !strings.Contains(genesis, want) {
			t.Errorf("the genesis command lacks %q: %s", want, genesis)
		}
	}
	if strings.Contains(genesis, "--archive") || strings.Contains(genesis, "example") {
		t.Errorf("the genesis command is not the fixture's: %s", genesis)
	}
}

func TestExecute_anArchiveFixtureDropsTheReleaseFlags(t *testing.T) {
	ex := happyExec()
	if _, err := (Runner{Exec: ex, Fx: testFixture()}).Execute(context.Background(), guideCommands(t), Plan()); err != nil {
		t.Fatal(err)
	}
	for _, line := range ex.lines() {
		if !strings.HasPrefix(line, "orama node setup") {
			continue
		}
		if strings.Contains(line, "--release") || !strings.Contains(line, "--archive /build/orama.tar.gz") {
			t.Errorf("a setup command for an archive fixture: %s", line)
		}
	}
}

func TestFixtureValidate_theThingToInstall(t *testing.T) {
	if err := releaseFixture().Validate(); err != nil {
		t.Fatalf("a release fixture: %v", err)
	}
	for name, edit := range map[string]func(*Fixture){
		"both":          func(f *Fixture) { f.Archive = "/build/orama.tar.gz" },
		"no repository": func(f *Fixture) { f.ReleaseRepo = "" },
		"no root":       func(f *Fixture) { f.ReleaseRoot = "" },
		"no version":    func(f *Fixture) { f.Release = "" },
	} {
		f := releaseFixture()
		edit(f)
		if err := f.Validate(); err == nil {
			t.Errorf("%s: fixture accepted", name)
		}
	}
}

func TestBind_anExampleReleaseValueIsNeverSentToAMachine(t *testing.T) {
	fx := testFixture()
	fx.Archive = ""
	// No archive and no release: the example repository would reach setup.
	_, err := fx.Bind(Command{Section: SectionInstall, Argv: []string{"orama", "node", "setup", "--release-repo", guideReleaseRepo}})
	if err == nil {
		t.Fatal("an example release repository was bound to nothing and accepted")
	}
}
