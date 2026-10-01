package turn

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestReloadTenants_publishesWhatTheServerNowServes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)
	served := publishTo(t, s)

	next := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"}, TenantConfig{Namespace: "b", AuthSecret: "sb"})
	writeCfg(t, path, next)
	if err := s.reloadTenants(path); err != nil {
		t.Fatal(err)
	}

	st, err := ReadServedTenants(served)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if st.ConfigSHA256 != ConfigDigest(data) {
		t.Errorf("published digest %s is not the loaded config's", st.ConfigSHA256)
	}
	if !sameStringSet(st.Namespaces, []string{"a", "b"}) {
		t.Errorf("published namespaces %v, want a and b", st.Namespaces)
	}
}

// A rejected config leaves the old set live, and must not claim the new one.
func TestReloadTenants_aRejectedConfigIsNotPublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"})
	writeCfg(t, path, start)
	s := reloadServer(t, &start)
	served := publishTo(t, s)
	if err := s.reloadTenants(path); err != nil {
		t.Fatal(err)
	}
	good, _ := ReadServedTenants(served)

	writeCfg(t, path, baseCfg())
	if err := s.reloadTenants(path); err == nil {
		t.Fatal("an empty tenant list was accepted")
	}
	got, _ := ReadServedTenants(served)
	if got.ConfigSHA256 != good.ConfigSHA256 {
		t.Error("a config the server rejected was published as live")
	}
}

// publishTo points the server's status file at a temp path.
func publishTo(t *testing.T, s *Server) string {
	t.Helper()
	s.servedPath = filepath.Join(t.TempDir(), "served-tenants.json")
	return s.servedPath
}

// A file left by an earlier process must not be read as this one's: it names
// the config it served, which may be exactly the one on disk now.
func TestWatchTenantConfig_removesAnEarlierProcessesStatusBeforeTheFirstLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"})
	writeCfg(t, path, start)
	served := filepath.Join(dir, "served-tenants.json")
	if err := os.WriteFile(served, []byte(`{"namespaces":["a"],"config_sha256":"stale","nonce":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := reloadServer(t, &start)
	// An unloadable config: the first load fails, so only the removal can have
	// cleared the stale file.
	if err := os.WriteFile(path, []byte("this: is: not: valid: yaml\n\t\tbroken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.WatchTenantConfig(path, served)
	t.Cleanup(func() { close(s.certStop) })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(served); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("an earlier process's status file survived the start of this one")
}

func TestReadServedTenants_missingFileIsAnError(t *testing.T) {
	if _, err := ReadServedTenants(filepath.Join(t.TempDir(), "turn.yaml")); err == nil {
		t.Fatal("a server that published nothing was read as serving")
	}
}

func observedServer(t *testing.T, cfg *Config) (*Server, *observer.ObservedLogs) {
	t.Helper()
	s := reloadServer(t, cfg)
	core, logs := observer.New(zap.WarnLevel)
	s.logger = zap.New(core)
	return s, logs
}

// The watcher ticks every 2s; re-parsing and rebuilding an unchanged file each
// time is pure waste.
func TestTick_skipsAnUnchangedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"})
	writeCfg(t, path, start)
	s, _ := observedServer(t, &start)

	var st reloadState
	s.tick(path, &st)
	first := s.currentTenants()
	s.tick(path, &st)
	if s.currentTenants() != first {
		t.Fatal("an unchanged config was rebuilt")
	}

	writeCfg(t, path, baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"}, TenantConfig{Namespace: "b", AuthSecret: "sb"}))
	s.tick(path, &st)
	if _, ok := s.tenantSecret("b"); !ok {
		t.Fatal("a changed config was not reloaded")
	}
}

// A same-size rewrite that keeps the old mtime (a coarse clock, or a restored
// timestamp) must still be seen: the content is hashed, not the stat.
func TestTick_reloadsASameSizeSameMtimeRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "s1"})
	writeCfg(t, path, start)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := observedServer(t, &start)
	var st reloadState
	s.tick(path, &st)

	writeCfg(t, path, baseCfg(TenantConfig{Namespace: "a", AuthSecret: "s2"}))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("test setup: size or mtime changed")
	}
	s.tick(path, &st)
	if secret, _ := s.tenantSecret("a"); secret != "s2" {
		t.Fatalf("secret %q, want the rotated s2", secret)
	}
}

func TestTick_warnsOncePerDistinctError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn.yaml")
	start := baseCfg(TenantConfig{Namespace: "a", AuthSecret: "sa"})
	writeCfg(t, path, start)
	s, logs := observedServer(t, &start)

	var st reloadState
	if err := os.WriteFile(path, []byte("this: is: not: valid: yaml\n\t\tbroken"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		s.tick(path, &st)
	}
	if n := logs.Len(); n != 1 {
		t.Fatalf("%d warnings for one persistent error, want 1", n)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.tick(path, &st)
	s.tick(path, &st)
	if n := logs.Len(); n != 2 {
		t.Fatalf("%d warnings after a different error, want 2", n)
	}

	writeCfg(t, path, start)
	s.tick(path, &st)
	if logs.Len() != 2 {
		t.Fatal("a recovery logged a warning")
	}
	if _, ok := s.tenantSecret("a"); !ok {
		t.Fatal("the tenant was lost")
	}
}
