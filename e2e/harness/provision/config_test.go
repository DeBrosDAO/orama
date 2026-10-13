package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvHetznerToken, "hcloud-secret-value")
	t.Setenv(EnvCFToken, "cf-secret-value")
	t.Setenv(EnvCFZone, testZone)
	t.Setenv(EnvRWAgentBin, "/opt/rw/rw-agent-headless")
	t.Setenv(EnvRWBin, "/opt/rw/rw")
	t.Setenv(EnvRepoRoot, t.TempDir())
	for _, name := range []string{EnvLocation, EnvServerType, EnvPreviousArchive, EnvProbeLocation, EnvRunID,
		EnvWorkDir, EnvArtifactDir, EnvEpochDuration, EnvEpochMinBlocks, EnvRunnerCIDR, EnvInstallPrevious, EnvServerLimit, EnvTTL} {
		t.Setenv(name, "")
	}
	t.Setenv(EnvRunnerCIDR, "198.51.100.4/32")
	t.Setenv(EnvAllowOpenSSH, "")
}

func TestLoadConfigFromEnv_defaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if cfg.Location != DefaultLocation || cfg.ServerType != DefaultServerType || !runIDPattern.MatchString(cfg.RunID) ||
		!strings.HasSuffix(cfg.WorkDir, workDirPrefix+cfg.RunID) || cfg.ArtifactDir != filepath.Join(cfg.WorkDir, "artifacts") {
		t.Fatalf("config %s / %+v", cfg, cfg)
	}
	if strings.Contains(cfg.String(), "secret-value") {
		t.Fatalf("String() prints a token: %s", cfg)
	}
}

func TestLoadConfigFromEnv_missingNamesOnly(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvHetznerToken, "")
	t.Setenv(EnvRWBin, "")
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), EnvHetznerToken) || !strings.Contains(err.Error(), EnvRWBin) {
		t.Fatalf("missing variables: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("the error prints a value: %v", err)
	}
}

func TestLoadConfigFromEnv_overrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvRunID, "abcd1234")
	t.Setenv(EnvLocation, "hel1")
	t.Setenv(EnvPreviousArchive, previousRefPrefix+"v0.200.0")
	cfg, err := LoadConfigFromEnv()
	if err != nil || cfg.RunID != "abcd1234" || cfg.Location != "hel1" {
		t.Fatalf("overrides: %v %+v", err, cfg)
	}
}

func TestValidate_refusals(t *testing.T) {
	e := newTestEnv(t)
	cases := map[string]func(*Config){
		"run id":        func(c *Config) { c.RunID = "Bad_ID" },
		"relative dir":  func(c *Config) { c.WorkDir = "runs/x" },
		"previous ref":  func(c *Config) { c.PreviousArchive = previousRefPrefix + "main;rm -rf /" },
		"dotdot ref":    func(c *Config) { c.PreviousArchive = previousRefPrefix + "a..b" },
		"epoch":         func(c *Config) { c.EpochDuration = "1 minute" },
		"no token":      func(c *Config) { c.CFToken = "" },
		"server limit":  func(c *Config) { c.ServerLimit = 0 },
		"empty image":   func(c *Config) { c.Image = "" },
		"min blocks":    func(c *Config) { c.EpochMinBlocks = "-1" },
		"artifact rel.": func(c *Config) { c.ArtifactDir = "a" },
		"runner cidr":   func(c *Config) { c.RunnerCIDRs = []string{"203.0.113.7"} },
		"no runner":     func(c *Config) { c.RunnerCIDRs = nil },
		"prev no ref":   func(c *Config) { c.InstallPrevious, c.PreviousArchive = true, "/tmp/old.tar.gz" },
		"prev nothing":  func(c *Config) { c.InstallPrevious = true },
	}
	for name, mutate := range cases {
		cfg := e.cfg
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
	if err := e.cfg.Validate(); err != nil {
		t.Fatalf("the test config: %v", err)
	}
}

func TestFindRepoRoot_fromInsideTheCheckout(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "e2e", "go.mod")); err != nil {
		t.Fatalf("root %s: %v", root, err)
	}
}

func TestCredentialsFromEnv_missing(t *testing.T) {
	t.Setenv(EnvHetznerToken, "")
	t.Setenv(EnvCFToken, "x")
	t.Setenv(EnvCFZone, "")
	_, err := credentialsFromEnv()
	if err == nil || err.Error() != "missing required environment variables: "+EnvCFZone+", "+EnvHetznerToken {
		t.Fatalf("credentialsFromEnv: %v", err)
	}
}

// TestLoadConfigFromEnv_runnerCIDRRequired: without E2E_RUNNER_CIDR the run
// is refused with an actionable message, unless open SSH is asked for.
func TestLoadConfigFromEnv_runnerCIDRRequired(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvRunnerCIDR, "")
	t.Setenv(EnvAllowOpenSSH, "")
	if _, err := LoadConfigFromEnv(); err == nil || !strings.Contains(err.Error(), EnvAllowOpenSSH+"=1") {
		t.Fatalf("an unset runner CIDR was accepted: %v", err)
	}
	t.Setenv(EnvAllowOpenSSH, "yes")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("an E2E_ALLOW_OPEN_SSH other than 1 opened SSH")
	}
}

func TestLoadConfigFromEnv_runnerLimitsAndPrevious(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv(EnvRunnerCIDR, "")
	t.Setenv(EnvAllowOpenSSH, allowOpenSSHOn)
	cfg, err := LoadConfigFromEnv()
	if err != nil || strings.Join(cfg.RunnerCIDRs, ",") != anywhereCIDRs || cfg.InstallPrevious ||
		cfg.ServerLimit != defaultServerLimit || cfg.TTL != DefaultTTL {
		t.Fatalf("defaults: %v %+v", err, cfg)
	}
	t.Setenv(EnvRunnerCIDR, " 198.51.100.4/32 , 2001:db8::/64 ")
	t.Setenv(EnvInstallPrevious, installPreviousOn)
	t.Setenv(EnvPreviousArchive, previousRefPrefix+"v0.200.0")
	t.Setenv(EnvServerLimit, "4")
	t.Setenv(EnvTTL, "90m")
	cfg, err = LoadConfigFromEnv()
	if err != nil || strings.Join(cfg.RunnerCIDRs, ",") != "198.51.100.4/32,2001:db8::/64" || !cfg.InstallPrevious ||
		cfg.ServerLimit != 4 || cfg.TTL.String() != "1h30m0s" {
		t.Fatalf("overrides: %v %+v", err, cfg)
	}
	for name, bad := range map[string]string{EnvServerLimit: "0", EnvTTL: "soon"} {
		t.Setenv(name, bad)
		if _, err := LoadConfigFromEnv(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%s: %v", name, bad, err)
		}
		t.Setenv(name, "")
	}
}
