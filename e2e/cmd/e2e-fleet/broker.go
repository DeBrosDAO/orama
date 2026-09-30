package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// goCacheVars are the Go toolchain locations the runner resolves and hands
// feature packages explicitly: their HOME is an empty directory of the run.
var goCacheVars = []string{"GOPATH", "GOMODCACHE", "GOCACHE"}

// startBroker serves the run's broker in its work dir (beside state.json)
// with the Cloudflare and Hetzner credentials this process holds (sealed:
// the broker child receives them over a pipe). It serves on a context the
// caller's cancellation does not reach: features clean up through it during
// their stop grace; Close ends it.
func startBroker(ctx context.Context, st *fleet.State, statePath string) (*broker.Listener, error) {
	missing := secrets.MissingEnv(config.RequiredRunEnv, secrets.LookupEnv)
	if len(missing) > 0 {
		return nil, fmt.Errorf("the broker needs %s in the runner's environment", strings.Join(missing, ", "))
	}
	cf, err := cloudflare.New(secrets.Getenv(config.EnvCFToken), secrets.Getenv(config.EnvCFZone), "")
	if err != nil {
		return nil, err
	}
	maxServers, err := brokerMaxServers(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	srv := &broker.Server{State: st, DNS: cf, Cloud: provision.Direct{}, MaxServers: maxServers,
		Redact: runRedact(statePath), Logf: stderrLogf}
	return broker.Listen(context.WithoutCancel(ctx), filepath.Dir(statePath), srv, os.LookupEnv)
}

// brokerMaxServers reads E2E_BROKER_MAX_SERVERS (0: the broker's default).
func brokerMaxServers(lookup func(string) (string, bool)) (int, error) {
	v, ok := lookup(broker.EnvMaxServers)
	if !ok || strings.TrimSpace(v) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s=%q must be a positive integer", broker.EnvMaxServers, v)
	}
	return n, nil
}

// runRedact redacts with the run's secrets and its token registry, read
// afresh each time (features keep registering what they mint). A registry
// that cannot be read withholds the text instead of passing it unredacted.
func runRedact(statePath string) func(string) string {
	return func(s string) string {
		red, err := secrets.ForRun(secrets.LookupEnv, statePath)
		if err != nil {
			return secrets.Withheld
		}
		return red.Redact(s)
	}
}

// closeBroker stops the broker, reporting its serving errors.
func closeBroker(l *broker.Listener) error {
	if err := l.Close(); err != nil {
		return fmt.Errorf("broker: %w", err)
	}
	return nil
}

// cmdBrokerServe is the broker child `run` and `test` start: it reads the
// cloud credentials from the pipe on fd 3 (never argv or env), serves the
// broker for the run in E2E_FLEET_STATE, prints "ready <socket>" and serves
// until its stdin closes. The runner's stop signals do not stop it (it is in
// a process group of its own and ignores them): the runner closes its stdin
// once the feature packages, and their cleanups, are done.
func cmdBrokerServe(parent context.Context, args []string) (int, error) {
	if err := parseFlags(newFlagSet(cmdBrokerServeName), args); err != nil {
		return exitUsage, err
	}
	signal.Ignore(stopSignals...)
	f := os.NewFile(brokerSecretsFD, "broker-secrets")
	values, rerr := secrets.ReadSealed(f)
	if err := errors.Join(rerr, f.Close(), secrets.Seal(values)); err != nil {
		return exitFail, fmt.Errorf("broker: failed to receive the credentials on fd %d: %w", brokerSecretsFD, err)
	}
	st, statePath, err := loadState()
	if err != nil {
		return exitFail, err
	}
	l, err := startBroker(parent, st, statePath)
	if err != nil {
		return exitFail, err
	}
	if _, err := fmt.Fprintln(os.Stdout, brokerReadyPrefix+l.Path); err != nil {
		return exitFail, errors.Join(fmt.Errorf("broker: failed to report ready: %w", err), closeBroker(l))
	}
	_, cerr := io.Copy(io.Discard, os.Stdin)
	if cerr != nil {
		cerr = fmt.Errorf("broker: failed to read the runner's stop pipe: %w", cerr)
	}
	return exitOK, errors.Join(cerr, closeBroker(l))
}

// featureBaseEnv is the runner's environment plus the Go cache locations
// (unless already set); stages.FeatureEnv then keeps only the allowlist.
func featureBaseEnv(ctx context.Context) ([]string, error) {
	env := os.Environ()
	var want []string
	for _, name := range goCacheVars {
		if _, ok := os.LookupEnv(name); !ok {
			want = append(want, name)
		}
	}
	if len(want) == 0 {
		return env, nil
	}
	out, err := exec.CommandContext(ctx, "go", append([]string{"env"}, want...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s with `go env`: %w", strings.Join(want, ", "), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(want) {
		return nil, fmt.Errorf("`go env %s` printed %d lines, want %d", strings.Join(want, " "), len(lines), len(want))
	}
	for i, name := range want {
		v := strings.TrimSpace(lines[i])
		if !filepath.IsAbs(v) {
			return nil, fmt.Errorf("`go env %s` is %q, not an absolute path", name, v)
		}
		env = append(env, name+"="+v)
	}
	return env, nil
}

// prefetchModules downloads the e2e module's dependencies into the module
// cache with the runner's own Go settings, so feature packages (which get
// neither GOPROXY nor GOFLAGS unless E2E_ALLOW_GO_ENV=1) build from the
// cache the runner resolved.
func prefetchModules(ctx context.Context, lay layout) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "download")
	cmd.Dir = lay.module
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to download the e2e module's dependencies in %s (`go mod download`): %w: %s",
			lay.module, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// withBroker runs fn with a stage runner whose feature packages reach the
// run's broker child (E2E_BROKER_SOCK) and carry no credential; the broker
// is up for exactly as long as fn runs.
func withBroker(ctx context.Context, lay layout, st *fleet.State, statePath string, fn func(*stages.Runner) error) error {
	base, err := featureBaseEnv(ctx)
	if err != nil {
		return err
	}
	if err := prefetchModules(ctx, lay); err != nil {
		return err
	}
	home, err := featureHome(filepath.Dir(statePath))
	if err != nil {
		return err
	}
	prefix, err := sandboxOf(lay, filepath.Dir(statePath), base)
	if err != nil {
		return err
	}
	r := newStageRunner(lay, statePath, st.ArtifactDir)
	r.BaseEnv, r.ExtraEnv = base, []string{"HOME=" + home}
	r.AfterDestructive, r.Prefix = restoreNodes(st), prefix
	if st.IsStagenet() {
		// No broker: it holds the cloud credentials the stagenet target never
		// uses. A test that needs one fails (harness.Broker, ExtraNode).
		return fn(r)
	}
	b, err := startBrokerChild(ctx, statePath)
	if err != nil {
		return err
	}
	r.ExtraEnv = append(r.ExtraEnv, broker.EnvSock+"="+b.path)
	return errors.Join(fn(r), b.stop())
}
