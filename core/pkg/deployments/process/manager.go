package process

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// Config is what the process manager needs from the gateway that owns it.
type Config struct {
	// Stager stores each deployment's environment file and workload token
	// where its unit reads them. On a node it is HelperStager.
	Stager Stager

	// BaseDomain is the cluster's domain, used to tell a deployment the URL of
	// its own namespace's gateway.
	BaseDomain string

	// Systemctl, when set, runs each systemctl command on a deployment unit
	// in place of orama-privhelper, and the manager drives units on any OS.
	// Nil on a node; set by tests outside this package that need to see a
	// stop refused.
	Systemctl func(args ...string) error
}

// Manager manages deployment processes via systemd (Linux) or direct process spawning (macOS/other)
type Manager struct {
	logger     *zap.Logger
	useSystemd bool
	stager     Stager
	baseDomain string

	// mintWorkloadToken issues the credential a deployment runs with. It is
	// injected rather than imported so this package does not depend on the
	// auth service, and so a test can watch what a deployment is handed.
	mintWorkloadToken WorkloadTokenMinter

	// stagedUntil is when the token last staged for each unit instance expires,
	// as this gateway minted it. The staged file is root-only and cannot be
	// read back, so this is what says whether a restart can run on it.
	stagedMu    sync.Mutex
	stagedUntil map[string]time.Time

	// stopped holds the instances a Stop has taken down and no Start has
	// brought back. A delete stops the unit before it removes the deployment's
	// row, so for that window a refresh or a restart would find a row and mint
	// a principal and a token for a deployment that is on its way out.
	stopped map[string]struct{}

	// refreshToken mints a credential for a deployment that already runs. It
	// writes nothing to the registry. Nil means mintWorkloadToken.
	refreshToken WorkloadTokenMinter

	// systemctl runs one systemctl command on a deployment unit. Nil means
	// runSystemctl, through orama-privhelper; a test sets it to watch the calls.
	systemctl func(args ...string) error

	// query runs a read-only systemctl command and returns its output. Nil
	// means systemctl itself; a test sets it to describe the node's units.
	query func(ctx context.Context, args ...string) ([]byte, error)

	// For non-systemd mode: track running processes
	processes   map[string]*exec.Cmd
	processesMu sync.RWMutex
}

// WorkloadTokenMinter issues the token a deployment runs with. It answers
// ErrDeploymentGone for a deployment that no longer exists.
type WorkloadTokenMinter func(ctx context.Context, namespace, name string) (string, error)

// SetWorkloadTokenRefresher wires the cheaper mint a refresh of a running
// deployment uses: the principal exists after Start, so it need not be
// recorded again, and the caller has just read the deployment's row.
func (m *Manager) SetWorkloadTokenRefresher(mint WorkloadTokenMinter) {
	m.refreshToken = mint
}

// ErrStopped is a mint refused because the deployment has been stopped for a
// delete and not started since.
var ErrStopped = errors.New("the deployment has been stopped")

// ErrDeploymentGone is a mint refused because the deployment's row is gone: a
// restart or a refresh that raced its delete must not give a deleted
// deployment an identity again.
var ErrDeploymentGone = errors.New("the deployment no longer exists")

// WorkloadTokenRestartMargin is how much life a staged token must have left for
// a restart to run on it when a fresh one cannot be minted.
const WorkloadTokenRestartMargin = 5 * time.Minute

// SetWorkloadTokenMinter wires the credential a deployment is started with.
//
// Without it a deployment cannot start at all on systemd: its unit stages the
// credential with LoadCredential= and refuses to run without the file. That is
// deliberate — a deployment with no identity is the permanent key this
// replaces.
func (m *Manager) SetWorkloadTokenMinter(mint WorkloadTokenMinter) {
	m.mintWorkloadToken = mint
}

// NewManager creates a new process manager
func NewManager(logger *zap.Logger, cfg Config) *Manager {
	// Use systemd only on Linux
	useSystemd := runtime.GOOS == "linux" || cfg.Systemctl != nil

	return &Manager{
		logger:     logger,
		useSystemd: useSystemd,
		systemctl:  cfg.Systemctl,
		stager:     cfg.Stager,
		baseDomain: strings.TrimSpace(cfg.BaseDomain),
		processes:  make(map[string]*exec.Cmd),
	}
}

// gatewayURL is the URL of the deployment's own namespace gateway, handed to
// the app as ORAMA_GATEWAY_URL.
//
// A deployment that wants to use the platform it runs on had nothing to go on:
// no gateway address, no namespace name, no credential. Every app that talked
// to Orama therefore baked an address and a permanent key into its own image.
func (m *Manager) gatewayURL(namespace string) string {
	if m.baseDomain == "" || namespace == "" {
		return ""
	}
	return fmt.Sprintf("https://ns-%s.%s", namespace, m.baseDomain)
}

// deploymentEnv is the full environment of one deployment: what the tenant set,
// with the platform's own variables applied last so they cannot be displaced.
func (m *Manager) deploymentEnv(deployment *deployments.Deployment, serviceName string) map[string]string {
	// A deployment with no runtime is served rather than run, so it has no
	// entry point and the template that would use one is never instantiated.
	_, entryPoint, _ := RuntimeFor(deployment)
	return mergeEnv(deployment.Environment, platformEnv(
		deployment.Namespace,
		serviceName,
		m.gatewayURL(deployment.Namespace),
		entryPoint,
		deployment.Port,
	))
}

// Stager stores a deployment's environment file and workload token where its
// unit's EnvironmentFile= and LoadCredential= read them.
//
// They used to be files the gateway wrote into a directory it owns. systemd
// reads both as PID 1 and follows symlinks, so a compromised gateway could
// point one at any root-readable file and read it through the deployment. The
// files now live in a root-only directory written by orama-privhelper
// (pkg/deploysecrets), and the gateway hands over contents, never paths.
//
// AllowPort writes the one port the instance's runtime unit may bind: the
// templates deny every bind, and the port differs per deployment
// (pkg/privhelper deploybind.go). BuildUser gives the instance's dependency
// install a user of its own. PurgeState removes the state and cache directories
// systemd keeps for the instance; StateInstances lists the instances that have
// any. Clear removes the rest.
type Stager interface {
	SetEnv(instance, contents string) error
	SetToken(instance, token string) error
	AllowPort(instance string, runtime Runtime, port int) error
	BuildUser(instance string) error
	PurgeState(instance string) error
	StateInstances() ([]string, error)
	Clear(instance string) error
}

// HelperStager is the Stager on a node: orama-privhelper.
type HelperStager struct{}

func (HelperStager) SetEnv(instance, contents string) error {
	return privhelper.SetDeploymentEnv(instance, contents)
}

func (HelperStager) SetToken(instance, token string) error {
	return privhelper.SetDeploymentToken(instance, token)
}

func (HelperStager) AllowPort(instance string, runtime Runtime, port int) error {
	return privhelper.AllowDeploymentPort(instance, string(runtime), port)
}

func (HelperStager) BuildUser(instance string) error {
	return privhelper.AllowDeploymentBuildUser(instance)
}

func (HelperStager) PurgeState(instance string) error { return privhelper.PurgeDeployment(instance) }

func (HelperStager) StateInstances() ([]string, error) { return privhelper.DeploymentStateInstances() }

func (HelperStager) Clear(instance string) error { return privhelper.ClearDeployment(instance) }

// unitInstance is the unit instance (%i) for a service name.
func unitInstance(serviceName string) string {
	return strings.TrimPrefix(serviceName, UnitPrefix)
}

// writeWorkloadToken mints the deployment's credential and stores it where the
// unit's LoadCredential= will find it.
//
// A failure here fails the deploy. The unit refuses to start without the file,
// and a deployment started with no identity is the permanent-key situation this
// replaces: it would work, and nothing it did would be attributable.
func (m *Manager) writeWorkloadToken(ctx context.Context, deployment *deployments.Deployment, serviceName string) error {
	return m.stageWorkloadToken(ctx, deployment, serviceName, m.mintWorkloadToken)
}

// stageWorkloadToken is writeWorkloadToken with the minter to use.
func (m *Manager) stageWorkloadToken(ctx context.Context, deployment *deployments.Deployment, serviceName string, mint WorkloadTokenMinter) error {
	if m.isStopped(unitInstance(serviceName)) {
		return fmt.Errorf("no credential for %s: %w", serviceName, ErrStopped)
	}
	if m.stager == nil {
		return fmt.Errorf("no deployment stager is configured, so the credential of %s has nowhere to go", serviceName)
	}
	if mint == nil {
		return fmt.Errorf("this gateway cannot mint a workload token, so %s would run with no identity", serviceName)
	}

	token, err := mint(ctx, deployment.Namespace, deployment.Name)
	if err != nil {
		return fmt.Errorf("mint the credential for %s: %w", serviceName, err)
	}
	if err := m.stager.SetToken(unitInstance(serviceName), token); err != nil {
		return fmt.Errorf("store the credential for %s: %w", serviceName, err)
	}
	m.recordStaged(unitInstance(serviceName), token)
	return nil
}

// recordStaged remembers when the token just staged for instance expires. A
// token whose expiry cannot be read is recorded as expired, so nothing runs on
// a credential nobody can vouch for.
func (m *Manager) recordStaged(instance, token string) {
	until, _ := tokenExpiry(token)
	m.stagedMu.Lock()
	defer m.stagedMu.Unlock()
	if m.stagedUntil == nil {
		m.stagedUntil = map[string]time.Time{}
	}
	m.stagedUntil[instance] = until
}

// stagedTokenUsable reports whether the token staged for instance still has
// WorkloadTokenRestartMargin of life left.
func (m *Manager) stagedTokenUsable(instance string) bool {
	m.stagedMu.Lock()
	defer m.stagedMu.Unlock()
	until, ok := m.stagedUntil[instance]
	return ok && time.Until(until) > WorkloadTokenRestartMargin
}

// tokenExpiry reads the exp claim of a JWT without verifying it: the manager
// minted the token itself and only needs to know how long it lasts.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// RefreshToken stages a fresh credential for a running deployment without
// restarting it. The unit reads its credential at every start, and systemd
// restarts a crashed unit (Restart=always) or starts it at boot without the
// gateway, so the staged token must always have life left or the app would
// start with one that has expired and could not renew it.
//
// A unit that is not running is left alone (the health checker's restart mints
// its own), and so is one a delete has stopped.
func (m *Manager) RefreshToken(ctx context.Context, deployment *deployments.Deployment) error {
	if !m.useSystemd {
		return nil
	}
	unit, err := m.unitName(deployment)
	if errors.Is(err, ErrServedNotRun) {
		return nil
	}
	if err != nil {
		return err
	}
	serviceName := m.getServiceName(deployment)
	if m.isStopped(unitInstance(serviceName)) {
		return fmt.Errorf("no credential for %s: %w", serviceName, ErrStopped)
	}
	out, err := m.querySystemctl(ctx, "show", unit, "--property="+propActiveState)
	if err != nil {
		return fmt.Errorf("read the state of %s before refreshing its credential: %w", unit, err)
	}
	switch parseSystemctlShow(string(out))[propActiveState] {
	case "active", "activating", "reloading":
	default:
		return nil
	}
	mint := m.refreshToken
	if mint == nil {
		mint = m.mintWorkloadToken
	}
	return m.stageWorkloadToken(ctx, deployment, serviceName, mint)
}

// markStopped and clearStopped record whether Stop has taken instance down.
func (m *Manager) markStopped(instance string) {
	m.stagedMu.Lock()
	defer m.stagedMu.Unlock()
	if m.stopped == nil {
		m.stopped = map[string]struct{}{}
	}
	m.stopped[instance] = struct{}{}
}

func (m *Manager) clearStopped(instance string) {
	m.stagedMu.Lock()
	defer m.stagedMu.Unlock()
	delete(m.stopped, instance)
}

func (m *Manager) isStopped(instance string) bool {
	m.stagedMu.Lock()
	defer m.stagedMu.Unlock()
	_, ok := m.stopped[instance]
	return ok
}

// writeEnvFile stores the deployment's environment where only root can read it.
//
// systemd reads EnvironmentFile= as PID 1, before it drops to the deployment's
// own user, so the file never has to be readable by the deployment itself —
// and it must not be, because it holds the tenant's secrets and the deployment
// is the tenant's code.
func (m *Manager) writeEnvFile(deployment *deployments.Deployment, serviceName string) error {
	if m.stager == nil {
		return fmt.Errorf("no deployment stager is configured, so the environment of %s has nowhere safe to go", serviceName)
	}
	contents, err := deployments.RenderEnvFile(m.deploymentEnv(deployment, serviceName))
	if err != nil {
		return fmt.Errorf("failed to render the environment of %s: %w", serviceName, err)
	}
	if err := m.stager.SetEnv(unitInstance(serviceName), contents); err != nil {
		return fmt.Errorf("store the environment of %s: %w", serviceName, err)
	}
	return nil
}

// allowPort lets the deployment's unit bind its own port, the PORT its
// environment file names, and no other.
//
// The templates allowed every TCP and UDP port, so a tenant could bind a
// platform port on 127.0.0.1 while its service restarted — the index gateway
// Caddy sends public traffic to, the chain RPC the node report reads — or
// another tenant's. A port outside the deployment range is refused here, with
// the deployment named, before the helper refuses it.
func (m *Manager) allowPort(deployment *deployments.Deployment, serviceName string) error {
	if m.stager == nil {
		return fmt.Errorf("no deployment stager is configured, so %s cannot be allowed its port", serviceName)
	}
	runtime, _, err := RuntimeFor(deployment)
	if err != nil {
		return fmt.Errorf("allow %s its port: %w", serviceName, err)
	}
	if deployment.Port < deployments.UserMinPort || deployment.Port > deployments.MaxPort {
		return fmt.Errorf("%s was allocated port %d, outside the deployment range %d-%d",
			serviceName, deployment.Port, deployments.UserMinPort, deployments.MaxPort)
	}
	if err := m.stager.AllowPort(unitInstance(serviceName), runtime, deployment.Port); err != nil {
		return fmt.Errorf("allow %s to bind port %d: %w", serviceName, deployment.Port, err)
	}
	return nil
}

// removeSecrets deletes a stopped deployment's environment and credential.
// Leaving them behind leaves the tenant's secrets on the node after the
// deployment is gone.
func (m *Manager) removeSecrets(serviceName string) error {
	if m.stager == nil {
		return nil
	}
	if err := m.stager.Clear(unitInstance(serviceName)); err != nil {
		return fmt.Errorf("remove the environment and credential of %s: %w", serviceName, err)
	}
	m.stagedMu.Lock()
	delete(m.stagedUntil, unitInstance(serviceName))
	m.stagedMu.Unlock()
	return nil
}

// purgeState removes the state and cache directories of a stopped instance.
func (m *Manager) purgeState(instance string) error {
	if m.stager == nil {
		return nil
	}
	return m.stager.PurgeState(instance)
}

// Start starts a deployment process
func (m *Manager) Start(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Starting deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("namespace", deployment.Namespace),
		zap.String("service", serviceName),
		zap.Bool("systemd", m.useSystemd),
	)

	if !m.useSystemd {
		return m.startDirect(ctx, deployment, workDir)
	}

	unit, err := m.unitName(deployment)
	if err != nil {
		return err
	}
	// A start is a new deployment of the instance, whatever stopped it before.
	m.clearStopped(unitInstance(serviceName))

	// The environment file is the only thing the gateway writes. The unit is a
	// template installed at install time, because a gateway that is not root
	// cannot write into /etc — and must not be root, which is the whole point
	// of the hardened gateway unit.
	if err := m.writeEnvFile(deployment, serviceName); err != nil {
		return err
	}
	if err := m.allowPort(deployment, serviceName); err != nil {
		return err
	}
	if err := m.writeWorkloadToken(ctx, deployment, serviceName); err != nil {
		return err
	}

	// Limits the tenant asked for. The template carries the defaults, so this
	// runs only when they differ — it is the one call here that writes a
	// root-owned file, and systemd writes it on our behalf.
	if err := m.applyResourceLimits(deployment, unit); err != nil {
		return err
	}

	if err := m.systemdEnable(unit); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}

	if err := m.systemdStart(unit); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	m.logger.Info("Deployment process started",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	return nil
}

// startDirect starts a process directly without systemd (for macOS/local dev)
func (m *Manager) startDirect(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	serviceName := m.getServiceName(deployment)
	startCmd := m.getStartCommand(deployment, workDir)

	// Parse command
	parts := strings.Fields(startCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty start command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir

	// Set environment. Same set as the unit gets, so a deployment behaves the
	// same way whether it is run by systemd or spawned directly.
	cmd.Env = append(os.Environ(), sortedEnv(m.deploymentEnv(deployment, serviceName))...)

	// Create log file for output
	logDir := filepath.Join(os.Getenv("HOME"), ".orama", "logs", "deployments")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, serviceName+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		m.logger.Warn("Failed to create log file", zap.Error(err))
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// Start process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Track process
	m.processesMu.Lock()
	m.processes[serviceName] = cmd
	m.processesMu.Unlock()

	// Monitor process in background
	go func() {
		err := cmd.Wait()
		m.processesMu.Lock()
		delete(m.processes, serviceName)
		m.processesMu.Unlock()
		if err != nil {
			m.logger.Warn("Process exited with error",
				zap.String("service", serviceName),
				zap.Error(err),
			)
		}
		if logFile != nil {
			logFile.Close()
		}
	}()

	m.logger.Info("Deployment process started (direct)",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
		zap.Int("pid", cmd.Process.Pid),
	)

	return nil
}

// Stop stops a deployment process
func (m *Manager) Stop(ctx context.Context, deployment *deployments.Deployment) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Stopping deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	if !m.useSystemd {
		return m.stopDirect(serviceName)
	}

	unit, err := m.unitName(deployment)
	if errors.Is(err, ErrServedNotRun) {
		// A served deployment (static, Next.js export) has no unit to stop;
		// deleting one must not fail on it.
		return nil
	}
	if err != nil {
		return err
	}

	// Every step is attempted and every failure returned. They used to be
	// logged and Stop answered nil, so a delete whose stop the privileged
	// helper refused freed the deployment's port while its unit kept running
	// on it, and the next deployment given the port crash-looped behind it.
	var errs []error
	// Marked before the stop and kept after it: from here until a Start, no
	// credential is minted for this instance. A stop that failed leaves the
	// unit running, so it is not marked.
	m.markStopped(unitInstance(serviceName))
	stopErr := m.systemdStop(unit)
	if stopErr != nil {
		m.clearStopped(unitInstance(serviceName))
		errs = append(errs, fmt.Errorf("stop %s: %w", unit, stopErr))
	}
	if err := m.systemdDisable(unit); err != nil {
		errs = append(errs, fmt.Errorf("disable %s: %w", unit, err))
	}

	// There is no unit file to remove: the unit is a template instance, and
	// the instance stops existing when nothing references it.

	// What the build installed goes too, or it outlives the deployment — but
	// only once the app is stopped: pulling its dependencies from under a
	// process that is still running breaks it without stopping it. This comes
	// before the secrets and drop-ins are cleared: the removal runs as the
	// instance's build user, whose drop-in Clear deletes.
	if stopErr == nil && UsesBuildOutput(deployment.Type) {
		if err := m.ClearDependencies(deployment.Namespace, deployment.Name); err != nil {
			errs = append(errs, fmt.Errorf("clear the installed dependencies of %s: %w", unit, err))
		}
	}

	// The environment file holds the tenant's secrets. Leaving it behind
	// leaves them on the node after the deployment is gone.
	if err := m.removeSecrets(serviceName); err != nil {
		errs = append(errs, fmt.Errorf("remove the secrets of %s, which are still on disk: %w", unit, err))
	}

	// The tenant's own data — the state and cache directories systemd keeps
	// for the unit — goes once the unit is stopped.
	if stopErr == nil {
		if err := m.purgeState(unitInstance(serviceName)); err != nil {
			errs = append(errs, fmt.Errorf("remove the state and cache directories of %s, which are still on disk: %w", unit, err))
		}
	}

	return errors.Join(errs...)
}

// unitName is the template instance that runs a deployment.
func (m *Manager) unitName(deployment *deployments.Deployment) (string, error) {
	runtime, _, err := RuntimeFor(deployment)
	if err != nil {
		return "", err
	}
	return UnitName(runtime, deployment.Namespace, deployment.Name), nil
}

// applyResourceLimits sets the memory and CPU a deployment asked for.
//
// `systemctl set-property` writes the drop-in as root on our behalf, which is
// what keeps the gateway out of /etc. Only the values that differ from the
// template's defaults are sent, so a deployment that asked for nothing costs
// no call at all.
func (m *Manager) applyResourceLimits(deployment *deployments.Deployment, unit string) error {
	var props []string
	if mb := deployment.MemoryLimitMB; mb > 0 && mb != deployments.DefaultMemoryLimitMB {
		props = append(props, fmt.Sprintf("MemoryMax=%dM", mb))
	}
	if cpu := deployment.CPULimitPercent; cpu > 0 && cpu != deployments.DefaultCPULimitPercent {
		props = append(props, fmt.Sprintf("CPUQuota=%d%%", cpu))
	}
	if len(props) == 0 {
		return nil
	}
	args := append([]string{"set-property", unit}, props...)
	if err := m.runSystemctl(args...); err != nil {
		return fmt.Errorf("failed to apply the resource limits of %s: %w", unit, err)
	}
	return nil
}

// stopDirect stops a directly spawned process
func (m *Manager) stopDirect(serviceName string) error {
	m.processesMu.Lock()
	defer m.processesMu.Unlock()

	cmd, exists := m.processes[serviceName]
	if !exists || cmd.Process == nil {
		return nil // Already stopped
	}

	// Send SIGTERM
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		// Try SIGKILL if SIGTERM fails
		cmd.Process.Kill()
	}

	return nil
}

// Restart restarts a deployment process
func (m *Manager) Restart(ctx context.Context, deployment *deployments.Deployment) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Restarting deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	if !m.useSystemd {
		// For direct mode, stop and start
		m.stopDirect(serviceName)
		// Note: Would need workDir to restart, which we don't have here
		// For now, just log a warning
		m.logger.Warn("Restart not fully supported in direct mode")
		return nil
	}

	unit, err := m.unitName(deployment)
	if err != nil {
		return err
	}
	// The unit reads its credential at every start, so a restart that left the
	// staged file alone would start the app on the token minted at its last
	// Start: one that may have expired (the app renews with the token it holds,
	// so an expired one is an identity it cannot get back), and one holding the
	// grant of that day, which a redeploy is documented to replace at once.
	if err := m.writeWorkloadToken(ctx, deployment, serviceName); err != nil {
		// A deployment that is gone or stopped is never restarted. For any other failure
		// (the registry is down) a restart may still run on the staged token
		// while it has life left: refusing would leave a crashed app down for
		// as long as the registry is, over a credential that works.
		if errors.Is(err, ErrDeploymentGone) || errors.Is(err, ErrStopped) || !m.stagedTokenUsable(unitInstance(serviceName)) {
			return err
		}
		m.logger.Warn("Could not mint a fresh credential; restarting on the one already staged",
			zap.String("service", serviceName), zap.Error(err))
	}
	return m.systemdRestart(unit)
}

// Reconfigure rewrites the deployment's environment and restarts it.
//
// Restart alone was not enough while the variables lived in the unit, which was
// written once at Start and never touched again: `systemctl restart`
// re-executed the same unit with the same Environment= lines, so a change made
// in the database took effect only on the next deploy. They live in a file the
// unit reads at every start now, so rewriting it and restarting is the whole
// operation.
func (m *Manager) Reconfigure(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	if !m.useSystemd {
		// Direct mode holds the environment in the process it spawned, so the
		// only way to change it is to spawn a new one.
		m.stopDirect(m.getServiceName(deployment))
		return m.startDirect(ctx, deployment, workDir)
	}

	unit, err := m.unitName(deployment)
	if err != nil {
		return err
	}
	if err := m.writeEnvFile(deployment, m.getServiceName(deployment)); err != nil {
		return fmt.Errorf("failed to rewrite the environment of %s: %w", unit, err)
	}
	if err := m.allowPort(deployment, m.getServiceName(deployment)); err != nil {
		return err
	}
	// A restart re-reads the credential, so it is minted fresh here too: a
	// deployment that has been running for a day should not restart holding a
	// token issued a day ago.
	if err := m.writeWorkloadToken(ctx, deployment, m.getServiceName(deployment)); err != nil {
		return err
	}
	if err := m.applyResourceLimits(deployment, unit); err != nil {
		return err
	}
	if err := m.systemdRestart(unit); err != nil {
		return fmt.Errorf("failed to restart service: %w", err)
	}

	m.logger.Info("Deployment process reconfigured",
		zap.String("deployment", deployment.Name),
		zap.String("service", m.getServiceName(deployment)),
	)
	return nil
}

// Status gets the status of a deployment process
func (m *Manager) Status(ctx context.Context, deployment *deployments.Deployment) (string, error) {
	serviceName := m.getServiceName(deployment)

	if !m.useSystemd {
		m.processesMu.RLock()
		_, exists := m.processes[serviceName]
		m.processesMu.RUnlock()
		if exists {
			return "active", nil
		}
		return "inactive", nil
	}

	unit, err := m.unitName(deployment)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", unit)
	output, err := cmd.Output()
	if err != nil {
		return "unknown", err
	}

	return strings.TrimSpace(string(output)), nil
}

// GetLogs retrieves logs for a deployment
func (m *Manager) GetLogs(ctx context.Context, deployment *deployments.Deployment, lines int, follow bool) ([]byte, error) {
	serviceName := m.getServiceName(deployment)

	if !m.useSystemd {
		// Read from log file in direct mode
		logFile := filepath.Join(os.Getenv("HOME"), ".orama", "logs", "deployments", serviceName+".log")
		data, err := os.ReadFile(logFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read log file: %w", err)
		}
		// Return last N lines if specified
		if lines > 0 {
			logLines := strings.Split(string(data), "\n")
			if len(logLines) > lines {
				logLines = logLines[len(logLines)-lines:]
			}
			return []byte(strings.Join(logLines, "\n")), nil
		}
		return data, nil
	}

	unit, err := m.unitName(deployment)
	if err != nil {
		return nil, err
	}
	args := []string{"-u", unit, "--no-pager"}
	if lines > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", lines))
	}
	if follow {
		args = append(args, "-f")
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	return cmd.Output()
}

// getStartCommand determines the start command for a deployment
func (m *Manager) getStartCommand(deployment *deployments.Deployment, workDir string) string {
	// For systemd (Linux), use full paths. For direct mode, use PATH resolution.
	nodePath := "node"
	npmPath := "npm"
	if m.useSystemd {
		nodePath = "/usr/bin/node"
		npmPath = "/usr/bin/npm"
	}

	switch deployment.Type {
	case deployments.DeploymentTypeNextJS:
		// CLI tarballs the standalone output directly, so server.js is at the root
		return nodePath + " server.js"
	case deployments.DeploymentTypeNodeJSBackend:
		// Check if ENTRY_POINT is set in environment
		if entryPoint, ok := deployment.Environment["ENTRY_POINT"]; ok {
			if entryPoint == "npm:start" {
				return npmPath + " start"
			}
			return nodePath + " " + entryPoint
		}
		return nodePath + " index.js"
	case deployments.DeploymentTypeGoBackend:
		return filepath.Join(workDir, "app")
	default:
		return "echo 'Unknown deployment type'"
	}
}

// getServiceName generates a systemd service name
func (m *Manager) getServiceName(deployment *deployments.Deployment) string {
	// Sanitize namespace and name for service name
	namespace := strings.ReplaceAll(deployment.Namespace, ".", "-")
	name := strings.ReplaceAll(deployment.Name, ".", "-")
	return fmt.Sprintf("orama-deploy-%s-%s", namespace, name)
}

// systemd helper methods.
//
// These called systemctl directly, which only works as root, and the gateway
// runs as the orama user under NoNewPrivileges. systemd.Systemctl is the call
// the rest of the node makes: systemctl itself when this process is root, and
// otherwise `orama-privhelper call`, which hands the request over the helper's
// socket to a root service that allows exactly SystemctlVerbs on
// orama-deploy-* units (pkg/privhelper). No privilege is gained in this
// process, so it works under NoNewPrivileges; sudo is not involved.
func (m *Manager) systemdEnable(serviceName string) error {
	return m.runSystemctl("enable", serviceName)
}

func (m *Manager) systemdDisable(serviceName string) error {
	return m.runSystemctl("disable", serviceName)
}

func (m *Manager) systemdStart(serviceName string) error {
	return m.runSystemctl("start", serviceName)
}

func (m *Manager) systemdStop(serviceName string) error {
	return m.runSystemctl("stop", serviceName)
}

func (m *Manager) systemdRestart(serviceName string) error {
	return m.runSystemctl("restart", serviceName)
}

// runSystemctl runs systemctl through the manager's seam.
func (m *Manager) runSystemctl(args ...string) error {
	if m.systemctl != nil {
		return m.systemctl(args...)
	}
	return runSystemctl(args...)
}

// runSystemctl reports what systemctl said, not just that it failed.
func runSystemctl(args ...string) error {
	cmd := systemd.Systemctl(args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// WaitForHealthy waits for a deployment to become healthy
func (m *Manager) WaitForHealthy(ctx context.Context, deployment *deployments.Deployment, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		status, err := m.Status(ctx, deployment)
		if err == nil && status == "active" {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			// Continue checking
		}
	}

	return fmt.Errorf("deployment did not become healthy within %v", timeout)
}

// DeploymentStats holds on-demand resource usage for a deployment process
type DeploymentStats struct {
	PID        int     `json:"pid"`
	CPUPercent float64 `json:"cpu_percent"`
	MemoryRSS  int64   `json:"memory_rss_bytes"`
	DiskBytes  int64   `json:"disk_bytes"`
	UptimeSecs float64 `json:"uptime_seconds"`
}

// GetStats returns on-demand resource usage stats for a deployment.
// deployPath is the directory on disk for disk usage calculation.
func (m *Manager) GetStats(ctx context.Context, deployment *deployments.Deployment, deployPath string) (*DeploymentStats, error) {
	stats := &DeploymentStats{}

	// Disk usage (works on all platforms)
	if deployPath != "" {
		stats.DiskBytes = dirSize(deployPath)
	}

	if !m.useSystemd {
		// Direct mode (macOS) — only disk, no /proc
		serviceName := m.getServiceName(deployment)
		m.processesMu.RLock()
		if cmd, exists := m.processes[serviceName]; exists && cmd.Process != nil {
			stats.PID = cmd.Process.Pid
		}
		m.processesMu.RUnlock()
		return stats, nil
	}

	// Systemd mode (Linux) — get PID, CPU, RAM, uptime
	unit, err := m.unitName(deployment)
	if err != nil {
		return stats, err
	}
	cmd := exec.CommandContext(ctx, "systemctl", "show", unit,
		"--property=MainPID,ActiveEnterTimestamp")
	output, err := cmd.Output()
	if err != nil {
		return stats, fmt.Errorf("systemctl show failed: %w", err)
	}

	props := parseSystemctlShow(string(output))
	pid, _ := strconv.Atoi(props["MainPID"])
	stats.PID = pid

	if pid <= 0 {
		return stats, nil // Process not running
	}

	// Uptime from ActiveEnterTimestamp
	if ts := props["ActiveEnterTimestamp"]; ts != "" {
		// Format: "Mon 2026-01-29 10:00:00 UTC"
		if t, err := parseSystemdTimestamp(ts); err == nil {
			stats.UptimeSecs = time.Since(t).Seconds()
		}
	}

	// Memory RSS from /proc/[pid]/status
	stats.MemoryRSS = readProcMemoryRSS(pid)

	// CPU % — sample /proc/[pid]/stat twice with 1s gap
	stats.CPUPercent = sampleCPUPercent(pid)

	return stats, nil
}

// parseSystemctlShow parses "Key=Value\n" output into a map
func parseSystemctlShow(output string) map[string]string {
	props := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			props[line[:idx]] = strings.TrimSpace(line[idx+1:])
		}
	}
	return props
}

// parseSystemdTimestamp parses systemd timestamp like "Mon 2026-01-29 10:00:00 UTC"
func parseSystemdTimestamp(ts string) (time.Time, error) {
	// Try common systemd formats
	for _, layout := range []string{
		"Mon 2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 MST",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp: %s", ts)
}

// readProcMemoryRSS reads VmRSS from /proc/[pid]/status (Linux only)
func readProcMemoryRSS(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb * 1024 // Convert KB to bytes
			}
		}
	}
	return 0
}

// sampleCPUPercent reads /proc/[pid]/stat twice with a 1s gap to compute CPU %
func sampleCPUPercent(pid int) float64 {
	readCPUTicks := func() (utime, stime int64, ok bool) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, 0, false
		}
		// Fields after the comm (in parens): state(3), ppid(4), ... utime(14), stime(15)
		// Find closing paren to skip comm field which may contain spaces
		closeParen := strings.LastIndexByte(string(data), ')')
		if closeParen < 0 {
			return 0, 0, false
		}
		fields := strings.Fields(string(data)[closeParen+2:])
		if len(fields) < 13 {
			return 0, 0, false
		}
		u, _ := strconv.ParseInt(fields[11], 10, 64) // utime is field 14, index 11 after paren
		s, _ := strconv.ParseInt(fields[12], 10, 64) // stime is field 15, index 12 after paren
		return u, s, true
	}

	u1, s1, ok1 := readCPUTicks()
	if !ok1 {
		return 0
	}
	time.Sleep(1 * time.Second)
	u2, s2, ok2 := readCPUTicks()
	if !ok2 {
		return 0
	}

	// Clock ticks per second (usually 100 on Linux)
	clkTck := 100.0
	totalDelta := float64((u2 + s2) - (u1 + s1))
	cpuPct := (totalDelta / clkTck) * 100.0

	return cpuPct
}

// dirSize calculates total size of a directory
func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
