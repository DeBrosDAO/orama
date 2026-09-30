package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// EvalCluster is a single-node eval cluster beside the run (docs/EVAL.md).
type EvalCluster = fleet.Cluster

// Names of an eval cluster's parts.
const (
	// evalServerPrefix names its server as a run member: cluster-<name>.
	evalServerPrefix = "cluster-"
	// evalCAPrefix names its CA file beside the run's: ca-<name>.pem.
	evalCAPrefix = "ca-"
	evalCASuffix = ".pem"
	caFileMode   = 0o600
)

// evalNamePattern is an eval cluster's name: the label of its subdomain.
var evalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)

// AddEvalCluster installs a single-node eval cluster on a new server of the
// run: the subdomain e2e-<run>-<name>.<zone> delegated to it, genesis with
// the real CLI (`orama node setup --genesis --role nameserver`, Let's Encrypt
// staging), an `orama env` entry <that subdomain's first label> with its own
// CA file in the run's CLI HOME, and the certificate waited for. On failure
// everything it made is removed. In a feature process (E2E_BROKER_SOCK set)
// the runner's broker installs it.
func AddEvalCluster(ctx context.Context, st *fleet.State, name string) (EvalCluster, error) {
	b, err := runBroker()
	if err != nil {
		return EvalCluster{}, err
	}
	if b != nil {
		return b.AddCluster(ctx, name)
	}
	return Direct{}.AddCluster(ctx, st, name)
}

// RemoveEvalCluster removes the eval cluster called name: its DNS records,
// its server, its environment and its CA file. Parts already gone are not an
// error.
func RemoveEvalCluster(ctx context.Context, st *fleet.State, name string) error {
	b, err := runBroker()
	if err != nil {
		return err
	}
	if b != nil {
		return b.RemoveCluster(ctx, name)
	}
	return Direct{}.RemoveCluster(ctx, st, name)
}

// evalLayout is where the eval cluster called name lives.
func evalLayout(st *fleet.State, name string, d deps) (EvalCluster, error) {
	if !evalNamePattern.MatchString(name) {
		return EvalCluster{}, fmt.Errorf("eval cluster name %q must be a lowercase letter then up to 11 letters or digits", name)
	}
	if st.CAFile == "" || st.Home == "" || st.OramaBin == "" {
		return EvalCluster{}, fmt.Errorf("run %s has no CA file, CLI HOME or CLI yet: is it up?", st.RunID)
	}
	sub, err := d.dns.ClusterSubdomain(st.RunID, name)
	if err != nil {
		return EvalCluster{}, err
	}
	env, _, _ := strings.Cut(sub, ".")
	return EvalCluster{Name: name, Env: env, BaseDomain: sub, GatewayURL: "https://" + sub,
		CAFile: filepath.Join(filepath.Dir(st.CAFile), evalCAPrefix+name+evalCASuffix)}, nil
}

func addEvalCluster(ctx context.Context, st *fleet.State, name string, lim extraLimits, log Logger, d deps) (EvalCluster, error) {
	cl, err := evalLayout(st, name, d)
	if err != nil {
		return EvalCluster{}, err
	}
	left, err := d.dns.ListUnder(ctx, cl.BaseDomain)
	if err != nil {
		return EvalCluster{}, fmt.Errorf("failed to check %s for DNS records before installing eval cluster %s: %w", cl.BaseDomain, name, err)
	}
	if len(left) > 0 {
		return EvalCluster{}, fmt.Errorf("%s already has %d DNS records (first: %s %s): remove eval cluster %s first",
			cl.BaseDomain, len(left), left[0].Type, left[0].Name, name)
	}
	n, err := addExtra(ctx, st, evalServerPrefix+name, runLocation(st), lim, d)
	if err != nil {
		return EvalCluster{}, err
	}
	n.Role = fleet.RoleNameserver
	cl.Node = n
	if cl.HostKey, err = fleet.HostKeyFingerprint(st, n); err == nil {
		err = installEval(ctx, evalRun(st, cl, log, d))
	}
	if err != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		return EvalCluster{}, errors.Join(fmt.Errorf("eval cluster %s: %w", name, err), removeEvalCluster(cctx, st, name, log, d))
	}
	log.Infof("eval cluster %s is up: %s on %s", name, cl.GatewayURL, n.PublicIP)
	return cl, nil
}

// evalRun is a provisioning run over the eval cluster alone: its one node,
// its environment, the run's CLI, HOME, agent, key and known_hosts.
func evalRun(st *fleet.State, cl EvalCluster, log Logger, d deps) *run {
	est := &fleet.State{RunID: st.RunID, Env: cl.Env, BaseDomain: cl.BaseDomain, GatewayURL: cl.GatewayURL,
		CAFile: cl.CAFile, Nodes: []fleet.Node{cl.Node}, OramaBin: st.OramaBin, Home: st.Home,
		ArchivePath: st.ArchivePath, RWSock: st.RWSock, OperatorAddress: st.OperatorAddress,
		SSHKeyFile: st.SSHKeyFile, KnownHostsFile: st.KnownHostsFile, ArtifactDir: st.ArtifactDir}
	cfg := Config{RunID: st.RunID, WorkDir: filepath.Dir(st.CAFile), ArtifactDir: st.ArtifactDir}
	return &run{cfg: cfg, log: log, d: d, st: est, agent: &agentRun{Dir: st.Home, Sock: st.RWSock},
		hostKeys: map[string]string{cl.Node.PublicIP: cl.HostKey}}
}

// installEval writes the CA file and the environment, installs genesis,
// delegates the subdomain and waits for the certificate. The environment is
// added, never made the current one: other tests share the HOME.
func installEval(ctx context.Context, r *run) error {
	roots, err := os.ReadFile(filepath.Join(r.cfg.WorkDir, caFileName))
	if err != nil {
		return fmt.Errorf("failed to read the run's staging roots: %w", err)
	}
	if err := os.WriteFile(r.st.CAFile, roots, caFileMode); err != nil {
		return fmt.Errorf("failed to write the eval cluster's CA file %s: %w", r.st.CAFile, err)
	}
	desc := "e2e eval cluster of run " + r.cfg.RunID
	if _, err := r.oramaCmd(ctx, "env", "add", r.st.Env, r.st.GatewayURL, desc, "--ca-file", r.st.CAFile); err != nil {
		return err
	}
	if err := r.installGenesis(ctx); err != nil {
		return err
	}
	if err := r.delegate(ctx, 1); err != nil {
		return err
	}
	return r.waitGenesisCertificate(ctx)
}

// removeEvalCluster removes every part of the eval cluster called name,
// each attempted even when another fails.
func removeEvalCluster(ctx context.Context, st *fleet.State, name string, log Logger, d deps) error {
	cl, err := evalLayout(st, name, d)
	if err != nil {
		return err
	}
	var errs []error
	if _, err := d.dns.DeleteUnder(ctx, cl.BaseDomain); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete the records of %s: %w", cl.BaseDomain, err))
	}
	errs = append(errs, removeExtra(ctx, st, evalServerPrefix+name, d))
	r := evalRun(st, cl, log, d)
	// `orama env remove` of an absent environment succeeds.
	if _, err := r.oramaCmd(ctx, "env", "remove", cl.Env); err != nil {
		errs = append(errs, fmt.Errorf("failed to remove environment %s: %w", cl.Env, err))
	}
	if err := os.Remove(cl.CAFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("failed to remove %s: %w", cl.CAFile, err))
	}
	return errors.Join(errs...)
}
