package legacylayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// An env file the tree already holds, identical, was staged by a run that
// stopped before deleting the old one: it is only deleted.
func TestRun_unitEnvAlreadyStagedIsOnlyDeleted(t *testing.T) {
	f := newFixture(t)
	f.write(t, "data/namespaces/acme/rqlite.env", "HTTP_ADDR=10.0.0.1:10200\n")
	writeFile(t, unitenv.Path(f.unitEnvDir, "acme", "rqlite"), "HTTP_ADDR=10.0.0.1:10200\n")

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if f.stager.calls() != 0 {
		t.Errorf("an env already staged was staged again: %v", f.stager.unitEnvs)
	}
	assertGone(t, f.path("data/namespaces/acme/rqlite.env"))
}

func TestRun_unitEnvDifferentOnBothLayoutsIsRefused(t *testing.T) {
	f := newFixture(t)
	f.write(t, "data/namespaces/acme/rqlite.env", "HTTP_ADDR=10.0.0.1:10200\n")
	writeFile(t, unitenv.Path(f.unitEnvDir, "acme", "rqlite"), "HTTP_ADDR=10.0.0.1:10300\n")

	err := f.run(t)
	assertNamesBoth(t, err, f.path("data/namespaces/acme/rqlite.env"), unitenv.Path(f.unitEnvDir, "acme", "rqlite"))
	if _, statErr := os.Stat(f.path("data/namespaces/acme/rqlite.env")); statErr != nil {
		t.Error("a refused env file was deleted")
	}
}

// Names are checked as orama-privhelper checks them, before anything moves.
func TestRun_refusesAnEnvFileTheHelperWouldRefuse(t *testing.T) {
	f := newFixture(t)
	f.write(t, "data/namespaces/acme/Gateway.env", "X=1\n")
	f.write(t, "data/namespaces/acme/rqlite.env", "X=1\n")

	if err := f.run(t); err == nil {
		t.Fatal("an env file with an invalid service name must be refused")
	}
	if f.stager.calls() != 0 {
		t.Error("the valid env file was staged although the migration was refused")
	}
}

// The file is read with O_NOFOLLOW: a symlink planted as <svc>.env is not read
// and its target's contents never reach the helper.
func TestRun_refusesASymlinkedEnvFile(t *testing.T) {
	f := newFixture(t)
	f.write(t, "secrets/cluster-secret", "s3cret")
	if err := os.MkdirAll(f.path("data/namespaces/acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.path("secrets/cluster-secret"), f.path("data/namespaces/acme/rqlite.env")); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t); err == nil {
		t.Fatal("a symlinked env file must be refused")
	}
	if f.stager.calls() != 0 {
		t.Errorf("a symlink's target reached the helper: %v", f.stager.unitEnvs)
	}
}

// A helper that refuses leaves the old file in place for the next attempt.
func TestRun_helperFailureKeepsTheOldFile(t *testing.T) {
	f := newFixture(t)
	f.write(t, "data/namespaces/acme/rqlite.env", "X=1\n")
	f.write(t, "deployment-env/orama-deploy-acme-web.env", "PORT=1\n")
	f.stager.err = errHelperDown

	err := f.run(t)
	if !errors.Is(err, errHelperDown) {
		t.Fatalf("the helper's error must surface: %v", err)
	}
	if _, statErr := os.Stat(f.path("data/namespaces/acme/rqlite.env")); statErr != nil {
		t.Error("an env the helper did not store was deleted")
	}
	if _, statErr := os.Stat(f.path("deployment-env/orama-deploy-acme-web.env")); statErr != nil {
		t.Error("a deployment env the helper did not store was deleted")
	}
}

func TestRun_refusesAStrayFileInTheDeploymentEnvDir(t *testing.T) {
	for _, name := range []string{"notes.txt", "orama-deploy-.env", "orama-deploy-a.b.env", "orama-deploy-web.key"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.write(t, "deployment-env/orama-deploy-web.env", "PORT=1\n")
			f.write(t, "deployment-env/"+name, "x")
			if err := f.run(t); err == nil {
				t.Fatalf("%s must be refused", name)
			}
			if f.stager.calls() != 0 {
				t.Error("files were staged although the migration was refused")
			}
		})
	}
}

// An empty deployment-env directory left by an interrupted run is removed.
func TestRun_removesAnEmptyDeploymentEnvDir(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.path("deployment-env"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	assertGone(t, f.path("deployment-env"))
}

// Units that must not get an env file any more — wireguard (root), tor and ntfy
// (their own users), anyone-client (gone), turn (replaced by the shared server,
// and an env file is what starts the old unit) — have their old files deleted.
func TestRun_obsoleteEnvFilesAreDeletedNotStaged(t *testing.T) {
	f := newFixture(t)
	for _, svc := range []string{"wireguard", "tor", "ntfy", "anyone-client", "turn"} {
		f.write(t, "data/namespaces/index/"+svc+".env", "X=1\n")
	}
	f.write(t, "data/namespaces/index/olric.env", "OLRIC_SERVER_CONFIG=/x.yaml\n")

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, svc := range []string{"wireguard", "tor", "ntfy", "anyone-client", "turn"} {
		assertGone(t, f.path("data/namespaces/index/"+svc+".env"))
		if _, staged := f.stager.unitEnvs["index/"+svc]; staged {
			t.Errorf("%s.env was staged; its unit reads no env file", svc)
		}
	}
	if f.stager.unitEnvs["index/olric"] == "" {
		t.Error("olric.env was not staged")
	}
}

// An env file for a service no unit reads it for is not guessed at.
func TestRun_refusesAnEnvFileForAnUnknownService(t *testing.T) {
	f := newFixture(t)
	f.write(t, "data/namespaces/acme/mystery.env", "X=1\n")
	if err := f.run(t); err == nil {
		t.Fatal("an env file for an unknown service must be refused")
	}
}

// The staged set is exactly the units that read an env file from the unit env
// tree. A template that gains or drops EnvironmentFile= must be reflected here,
// or its old env file is either lost or refused.
func TestEnvReadingServices_matchTheUnitTemplates(t *testing.T) {
	templates, err := filepath.Glob(filepath.Join("..", "..", "systemd", "orama-namespace-*@.service"))
	if err != nil || len(templates) == 0 {
		t.Fatalf("no unit templates found: %v", err)
	}
	fromTemplates := map[string]bool{}
	for _, path := range templates {
		svc := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "orama-namespace-"), "@.service")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "/var/lib/orama-unit-env/%i/"+svc+".env") {
			fromTemplates[svc] = true
		}
	}
	for svc := range fromTemplates {
		// A retired unit (turn) keeps its template only so old instances can
		// be stopped; its old env file is deleted on purpose.
		if !envReadingServices[svc] && !obsoleteEnvServices[svc] {
			t.Errorf("orama-namespace-%s@ reads an env file but its old one would be refused", svc)
		}
	}
	for svc := range envReadingServices {
		if !fromTemplates[svc] {
			t.Errorf("%s is staged but no template reads /var/lib/orama-unit-env/%%i/%s.env", svc, svc)
		}
		if obsoleteEnvServices[svc] {
			t.Errorf("%s is both staged and deleted", svc)
		}
	}
}
