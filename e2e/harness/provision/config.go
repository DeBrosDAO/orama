// Package provision builds, and tears down, the real cluster one e2e run
// tests: three Hetzner servers installed with the orama CLI under test, a
// Cloudflare delegation of e2e-<id>.<zone>, Let's Encrypt staging
// certificates, a throwaway RootWallet agent as the operator, and chain
// validators co-hosted on the same machines. Every resource carries the run
// id (Hetzner label e2e-run, DNS name e2e-<id>), so Down and Sweep find it
// without any local state.
package provision

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// Defaults for what the environment may leave out.
const (
	DefaultLocation   = "nbg1"
	DefaultServerType = "cx23"
	DefaultImage      = "ubuntu-24.04"
	// DefaultTTL is the e2e-ttl label: the orphan sweep deletes anything older.
	DefaultTTL = 6 * time.Hour
	// DefaultEpochDuration and DefaultEpochMinBlocks shorten chain epochs.
	DefaultEpochDuration  = "60s"
	DefaultEpochMinBlocks = "5"
	// runIDLength is the length of a generated run id.
	runIDLength = 8
	// runIDAlphabet is what a generated run id is made of.
	runIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	// workDirPrefix names the default work directory under the temp dir.
	workDirPrefix = "orama-e2e-"
)

// Environment variable names LoadConfigFromEnv reads.
const (
	EnvHetznerToken    = "HCLOUD_TOKEN"
	EnvCFToken         = "CF_API_TOKEN"
	EnvCFZone          = "CF_ZONE"
	EnvRWAgentBin      = "E2E_RW_AGENT_BIN"
	EnvRWBin           = "E2E_RW_BIN"
	EnvLocation        = "E2E_LOCATION"
	EnvServerType      = "E2E_SERVER_TYPE"
	EnvPreviousArchive = "E2E_PREVIOUS_ARCHIVE"
	EnvProbeLocation   = "E2E_PROBE_LOCATION"
	EnvRunID           = "E2E_RUN_ID"
	EnvWorkDir         = "E2E_WORK_DIR"
	EnvArtifactDir     = "E2E_ARTIFACT_DIR"
	EnvRepoRoot        = "E2E_REPO_ROOT"
	EnvEpochDuration   = "E2E_EPOCH_DURATION"
	EnvEpochMinBlocks  = "E2E_EPOCH_MIN_BLOCKS"
	EnvRunnerCIDR      = "E2E_RUNNER_CIDR"
	EnvAllowOpenSSH    = "E2E_ALLOW_OPEN_SSH"
	EnvInstallPrevious = "E2E_INSTALL_PREVIOUS"
	EnvServerLimit     = "E2E_SERVER_LIMIT"
	EnvTTL             = "E2E_TTL"
)

// installPreviousOn is the E2E_INSTALL_PREVIOUS value that asks Up to install
// the previous release.
const installPreviousOn = "1"

// previousRefPrefix marks E2E_PREVIOUS_ARCHIVE as a git ref to build.
const previousRefPrefix = "ref:"

var (
	runIDPattern  = regexp.MustCompile(`^[a-z0-9]{4,16}$`)
	gitRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	durationPat   = regexp.MustCompile(`^[0-9]+(h|m|s)$`)
	integerPat    = regexp.MustCompile(`^[0-9]+$`)
)

// Config is one run. Tokens are tagged json:"-" and String leaves them out:
// nothing in this package prints them.
type Config struct {
	RunID       string
	ArtifactDir string
	WorkDir     string
	RepoRoot    string

	Location   string
	ServerType string
	Image      string
	// ProbeLocation, when set, adds a probe server there (another vantage point).
	ProbeLocation string

	HetznerToken string `json:"-"`
	CFToken      string `json:"-"`
	CFZone       string

	RWAgentBin string
	RWBin      string

	// PreviousArchive is a signed archive file, or ref:<git ref> to build the
	// previous release (and its CLI) from this repository with the run wallet.
	PreviousArchive string

	// InstallPrevious installs the previous release (archive and CLI) on the
	// cluster instead of HEAD; UpgradeToHead then rolls it forward.
	InstallPrevious bool

	// RunnerCIDRs are the only sources the firewall lets reach SSH.
	RunnerCIDRs []string

	// ServerLimit is the project's server quota; TTL is the e2e-ttl label.
	ServerLimit int
	TTL         time.Duration

	EpochDuration  string
	EpochMinBlocks string
}

// String describes the run without any secret.
func (c Config) String() string {
	return fmt.Sprintf("run %s: %s %s in %s, zone %s, work %s", c.RunID, c.ServerType, c.Image, c.Location, c.CFZone, c.WorkDir)
}

// LoadConfigFromEnv reads the run's configuration. A missing required
// variable is reported by name only; no value is ever printed.
func LoadConfigFromEnv() (Config, error) {
	var missing []string
	get := func(name string) string {
		v := strings.TrimSpace(secrets.Getenv(name))
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	cfg := Config{
		HetznerToken: get(EnvHetznerToken), CFToken: get(EnvCFToken), CFZone: get(EnvCFZone),
		RWAgentBin: get(EnvRWAgentBin), RWBin: get(EnvRWBin),
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	cfg.Location = envOr(EnvLocation, DefaultLocation)
	cfg.ServerType = envOr(EnvServerType, DefaultServerType)
	cfg.PreviousArchive = os.Getenv(EnvPreviousArchive)
	cfg.ProbeLocation = os.Getenv(EnvProbeLocation)
	cfg.EpochDuration = envOr(EnvEpochDuration, DefaultEpochDuration)
	cfg.EpochMinBlocks = envOr(EnvEpochMinBlocks, DefaultEpochMinBlocks)
	cfg.InstallPrevious = os.Getenv(EnvInstallPrevious) == installPreviousOn
	var err error
	if cfg.RunnerCIDRs, err = runnerCIDRs(); err != nil {
		return Config{}, err
	}
	return fillDefaults(cfg)
}

// fillDefaults completes ids, directories and limits.
func fillDefaults(cfg Config) (Config, error) {
	var err error
	cfg.Image = DefaultImage
	if cfg.ServerLimit, cfg.TTL, err = limitsFromEnv(); err != nil {
		return Config{}, err
	}
	if cfg.RunID = os.Getenv(EnvRunID); cfg.RunID == "" {
		if cfg.RunID, err = newRunID(); err != nil {
			return Config{}, err
		}
	}
	if cfg.RepoRoot = os.Getenv(EnvRepoRoot); cfg.RepoRoot == "" {
		if cfg.RepoRoot, err = findRepoRoot(); err != nil {
			return Config{}, err
		}
	}
	cfg.WorkDir = envOr(EnvWorkDir, filepath.Join(os.TempDir(), workDirPrefix+cfg.RunID))
	cfg.ArtifactDir = envOr(EnvArtifactDir, filepath.Join(cfg.WorkDir, "artifacts"))
	return cfg, cfg.Validate()
}

// Validate refuses a configuration Up cannot run safely.
func (c Config) Validate() error {
	var errs []error
	if !runIDPattern.MatchString(c.RunID) {
		errs = append(errs, fmt.Errorf("run id %q must be 4-16 lowercase letters or digits", c.RunID))
	}
	for name, dir := range map[string]string{"work dir": c.WorkDir, "artifact dir": c.ArtifactDir, "repo root": c.RepoRoot} {
		if !filepath.IsAbs(dir) {
			errs = append(errs, fmt.Errorf("%s %q must be an absolute path", name, dir))
		}
	}
	if c.Location == "" || c.ServerType == "" || c.Image == "" {
		errs = append(errs, errors.New("location, server type and image must be set"))
	}
	if c.HetznerToken == "" || c.CFToken == "" || c.CFZone == "" || c.RWAgentBin == "" || c.RWBin == "" {
		errs = append(errs, fmt.Errorf("%s, %s, %s, %s and %s must be set", EnvHetznerToken, EnvCFToken, EnvCFZone, EnvRWAgentBin, EnvRWBin))
	}
	if ref, ok := strings.CutPrefix(c.PreviousArchive, previousRefPrefix); ok && (!gitRefPattern.MatchString(ref) || strings.Contains(ref, "..")) {
		errs = append(errs, fmt.Errorf("%s ref %q is not a plain git ref", EnvPreviousArchive, ref))
	}
	if !durationPat.MatchString(c.EpochDuration) || !integerPat.MatchString(c.EpochMinBlocks) {
		errs = append(errs, fmt.Errorf("epoch duration %q must look like 60s and min blocks %q a plain integer", c.EpochDuration, c.EpochMinBlocks))
	}
	if c.ServerLimit <= 0 || c.TTL <= 0 {
		errs = append(errs, errors.New("server limit and TTL must be positive"))
	}
	if c.InstallPrevious && !strings.HasPrefix(c.PreviousArchive, previousRefPrefix) {
		errs = append(errs, fmt.Errorf("%s=%s installs the previous release with its own CLI, which only %s=%s<git ref> builds",
			EnvInstallPrevious, installPreviousOn, EnvPreviousArchive, previousRefPrefix))
	}
	return errors.Join(append(errs, checkCIDRs(c.RunnerCIDRs))...)
}

// limitsFromEnv is the server quota and the resource TTL, from
// E2E_SERVER_LIMIT and E2E_TTL or their defaults. Up and AddExtra both read
// them here, so an extra obeys the limits its run was created under.
func limitsFromEnv() (int, time.Duration, error) {
	limit, err := strconv.Atoi(envOr(EnvServerLimit, strconv.Itoa(defaultServerLimit)))
	if err != nil || limit <= 0 {
		return 0, 0, fmt.Errorf("%s must be a positive integer", EnvServerLimit)
	}
	ttl, err := time.ParseDuration(envOr(EnvTTL, DefaultTTL.String()))
	if err != nil || ttl <= 0 {
		return 0, 0, fmt.Errorf("%s must be a positive Go duration such as 6h", EnvTTL)
	}
	return limit, ttl, nil
}

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// newRunID is a random run id.
func newRunID() (string, error) {
	buf := make([]byte, runIDLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate a run id: %w", err)
	}
	for i, b := range buf {
		buf[i] = runIDAlphabet[int(b)%len(runIDAlphabet)]
	}
	return string(buf), nil
}

// findRepoRoot walks up from the working directory to the checkout holding
// both core/ and e2e/.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to read the working directory: %w", err)
	}
	for {
		if isFile(filepath.Join(dir, "core", "go.mod")) && isFile(filepath.Join(dir, "e2e", "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no checkout with core/go.mod and e2e/go.mod above the working directory; set %s", EnvRepoRoot)
		}
		dir = parent
	}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
