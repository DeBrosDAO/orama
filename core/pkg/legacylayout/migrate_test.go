package legacylayout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun_movesEveryOldPathIntoTheCurrentLayout(t *testing.T) {
	f := newFixture(t)
	f.seedOldLayout(t)

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for rel, want := range map[string]string{
		"data/namespaces/index/gateway/jwt-signing-key.pem": "rsa",
		"data/namespaces/index/gateway/jwt-eddsa-key.pem":   "ed",
		"data/sqlite/acme/app.db":                           "db",
		"data/deployments/acme-web/app":                     "bin",
		"data/turn/turn.yaml":                               "turn",
	} {
		if got := mustRead(t, f.path(rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	for _, rel := range []string{"secrets/jwt-signing-key.pem", "secrets/jwt-eddsa-key.pem", "sqlite", "deployments",
		"configs/turn.yaml", "data/namespaces/acme/rqlite.env", "data/namespaces/acme/gateway.env", "deployment-env"} {
		assertGone(t, f.path(rel))
	}
	if f.stager.unitEnvs["acme/rqlite"] != "HTTP_ADDR=10.0.0.1:10200\n" || f.stager.unitEnvs["acme/gateway"] != "GATEWAY_CONFIG=/x.yaml\n" {
		t.Errorf("unit envs staged: %v", f.stager.unitEnvs)
	}
	if f.stager.envs["acme-web"] != "PORT=10500\n" || f.stager.tokens["acme-web"] != "tok" {
		t.Errorf("deployment files staged: env %v token %v", f.stager.envs, f.stager.tokens)
	}
	// Renamed out of a writable secrets/: no copy marker is left behind.
	assertGone(t, CopiedKeysMarker(f.oramaDir))
}

// Every path is checked before any is moved, so a refusal leaves the node on
// the layout it had.
func TestRun_refusesWhenBothLayoutsHoldAPath(t *testing.T) {
	for _, tc := range []struct{ name, newRel, oldTop, newTop string }{
		{"signing key", "data/namespaces/index/gateway/jwt-signing-key.pem", "secrets/jwt-signing-key.pem", "data/namespaces/index/gateway/jwt-signing-key.pem"},
		{"sqlite", "data/sqlite/acme/app.db", "sqlite", "data/sqlite"},
		{"deployments", "data/deployments/acme-web/app", "deployments/acme/web", "data/deployments/acme-web"},
		{"turn config", "data/turn/turn.yaml", "configs/turn.yaml", "data/turn/turn.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.seedOldLayout(t)
			f.write(t, tc.newRel, "new")

			assertNamesBoth(t, f.run(t), f.path(tc.oldTop), f.path(tc.newTop))
			for _, rel := range []string{"secrets/jwt-eddsa-key.pem", "sqlite/acme/app.db", "data/namespaces/acme/rqlite.env", "deployment-env/orama-deploy-acme-web.env"} {
				if _, err := os.Stat(f.path(rel)); err != nil {
					t.Errorf("%s was changed by a refused migration: %v", rel, err)
				}
			}
			if f.stager.calls() != 0 {
				t.Errorf("a refused migration staged %d files", f.stager.calls())
			}
		})
	}
}

func TestRun_nothingOnTheOldLayoutIsANoOp(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, rel := range []string{"data/namespaces", "data/sqlite", "data/deployments", "data/turn"} {
		assertGone(t, f.path(rel))
	}
	if f.stager.calls() != 0 {
		t.Errorf("staged %d files with nothing to migrate", f.stager.calls())
	}
}

func TestRun_isIdempotent(t *testing.T) {
	f := newFixture(t)
	f.seedOldLayout(t)
	if err := f.run(t); err != nil {
		t.Fatalf("first run: %v", err)
	}
	f.stager = newFakeStager()
	if err := f.run(t); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := mustRead(t, f.path("data/namespaces/index/gateway/jwt-eddsa-key.pem")); got != "ed" {
		t.Errorf("a second run disturbed the moved key: %q", got)
	}
	if f.stager.calls() != 0 {
		t.Errorf("a second run staged %d files again", f.stager.calls())
	}
}

// Only what the old layout wrote is moved: a symlink where a tree or the TURN
// config should be is refused rather than carried into data/.
func TestRun_refusesASymlinkWhereTheOldLayoutWroteATree(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, f.path("sqlite")); err != nil {
		t.Fatal(err)
	}
	err := f.run(t)
	if err == nil {
		t.Fatal("a symlinked sqlite/ must be refused")
	}
	if _, statErr := os.Lstat(filepath.Join(f.oramaDir, "data", "sqlite")); !os.IsNotExist(statErr) {
		t.Error("the symlink was moved into data/")
	}
}

func TestHasTURN_readsTheOldLayout(t *testing.T) {
	f := newFixture(t)
	if ok, err := HasTURN(f.oramaDir); err != nil || ok {
		t.Fatalf("no TURN on the old layout: %v, %v", ok, err)
	}
	f.write(t, "data/namespaces/gw-only/gateway.env", "X=1\n")
	if ok, err := HasTURN(f.oramaDir); err != nil || ok {
		t.Fatalf("a gateway-only namespace is not TURN: %v, %v", ok, err)
	}
	f.write(t, "data/namespaces/anchat/turn.env", "TURN_CONFIG=/x.yaml\n")
	if ok, err := HasTURN(f.oramaDir); err != nil || !ok {
		t.Fatalf("a per-namespace turn.env is TURN: %v, %v", ok, err)
	}

	g := newFixture(t)
	g.write(t, "configs/turn.yaml", "turn")
	if ok, err := HasTURN(g.oramaDir); err != nil || !ok {
		t.Fatalf("the shared config at configs/turn.yaml is TURN: %v, %v", ok, err)
	}
}
