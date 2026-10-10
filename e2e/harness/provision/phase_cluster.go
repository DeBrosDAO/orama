package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// cliExitNotFound is the orama CLI's exit status for "nothing there yet"
	// (clierr.CodeNotFound): no nameserver has claimed a slot so far.
	cliExitNotFound = 4
	// cliExitUnavailable is clierr.CodeUnavailable: the gateway or a node
	// could not be reached (an SSH connection that dropped, a node still
	// restarting); the CLI documents it as one a script may retry.
	cliExitUnavailable = 5
	// maxUnavailableInARow bounds consecutive retries of cliExitUnavailable.
	maxUnavailableInARow = 3
	// httpsPort is where a node's Caddy serves TLS.
	httpsPort = "443"
)

// addEnvironment records the run's environment, trusting the staging roots
// for its domain only, and makes it the active one in the test HOME.
func (r *run) addEnvironment(ctx context.Context) error {
	r.st.CAFile = filepath.Join(r.cfg.WorkDir, caFileName)
	if err := writeStagingRoots(r.cfg.RepoRoot, r.st.CAFile); err != nil {
		return err
	}
	desc := "e2e run " + r.cfg.RunID
	if _, err := r.oramaCmd(ctx, "network", "add", r.st.Env, r.st.GatewayURL, desc, "--ca-file", r.st.CAFile); err != nil {
		return err
	}
	_, err := r.oramaCmd(ctx, "network", "use", r.st.Env)
	return err
}

// setupArgs is `orama node setup` for node i: the run key opens the fresh
// server once (--bootstrap-key) to install the wallet's key, and the host key
// pinned at boot is required (--host-key). Genesis creates the cluster with
// staging certificates; a joiner mints its invite on genesis and inherits
// the ACME directory from the join response.
func (r *run) setupArgs(i int) ([]string, error) {
	n := r.st.Nodes[i]
	fp, ok := r.hostKeys[n.PublicIP]
	if !ok {
		return nil, fmt.Errorf("%s (%s) has no pinned host key", n.Name, n.PublicIP)
	}
	args := []string{"node", "setup", "--ip", n.PublicIP, "--user", n.SSHUser, "--env", r.st.Env,
		"--role", n.Role, "--base-domain", r.st.BaseDomain, "--archive", r.installArchive(),
		"--host-key", fp, "--bootstrap-key", r.st.SSHKeyFile}
	if i == 0 {
		return append(args, "--genesis", "--acme-ca", acmeCA), nil
	}
	return append(args, "--join-via", r.st.Nodes[0].SSHUser+"@"+r.st.Nodes[0].PublicIP), nil
}

// installArchive is the archive genesis and the joins install: the previous
// release's with InstallPrevious, HEAD's otherwise.
func (r *run) installArchive() string {
	if r.cfg.InstallPrevious {
		return r.st.PreviousArchivePath
	}
	return r.st.ArchivePath
}

// installCmd runs `orama node setup` with the CLI of the release installed:
// each release's installer is driven by its own CLI.
func (r *run) installCmd(ctx context.Context, args ...string) (string, error) {
	if !r.cfg.InstallPrevious {
		return r.oramaCmd(ctx, args...)
	}
	if r.st.PreviousOramaBin == "" || r.st.PreviousArchivePath == "" {
		return "", fmt.Errorf("%s=%s but no previous CLI and archive were built", EnvInstallPrevious, installPreviousOn)
	}
	return r.runLogged(ctx, command{name: r.st.PreviousOramaBin, args: args, env: r.cliEnv()})
}

func (r *run) installGenesis(ctx context.Context) error {
	args, err := r.setupArgs(0)
	if err != nil {
		return err
	}
	_, err = r.installCmd(ctx, args...)
	return err
}

func (r *run) delegateGenesis(ctx context.Context) error {
	return r.delegate(ctx, 1)
}

// waitGenesisCertificate waits for the certificate each invite pins.
func (r *run) waitGenesisCertificate(ctx context.Context) error {
	wctx, cancel := context.WithTimeout(ctx, r.d.timing.certificate)
	defer cancel()
	addr := net.JoinHostPort(r.st.Nodes[0].PublicIP, httpsPort)
	return r.d.waitCert(wctx, addr, r.st.BaseDomain, r.st.CAFile, r.d.timing.poll)
}

// joinNodes joins the other nodes one at a time, each with an invite minted
// just before its install, and publishes its nameserver slot.
func (r *run) joinNodes(ctx context.Context) error {
	for i := 1; i < len(r.st.Nodes); i++ {
		args, err := r.setupArgs(i)
		if err != nil {
			return err
		}
		if _, err := r.installCmd(ctx, args...); err != nil {
			return fmt.Errorf("%s did not join: %w", r.st.Nodes[i].Name, err)
		}
		if err := r.delegate(ctx, i+1); err != nil {
			return err
		}
	}
	return nil
}

// delegation is one entry of `orama node dns delegation --json`.
type delegation struct {
	Domain      string                  `json:"domain"`
	Nameservers []cloudflare.Nameserver `json:"nameservers"`
}

// delegate polls the cluster's claimed slots until the first joined nodes
// all hold one, then writes the NS and glue records. Only two CLI answers are
// waited out: exit 4 (no slot claimed yet), until the delegation deadline,
// and exit 5 (a node or the gateway unreachable), at most
// maxUnavailableInARow times in a row. Anything else, a usage or auth error
// included, fails at once with the CLI's output.
func (r *run) delegate(ctx context.Context, joined int) error {
	wctx, cancel := context.WithTimeout(ctx, r.d.timing.delegation)
	defer cancel()
	ticker := time.NewTicker(r.d.timing.poll)
	defer ticker.Stop()
	unavailable := 0
	for {
		nss, done, err := r.readSlots(wctx, joined)
		if exitCode(err) == cliExitUnavailable && unavailable < maxUnavailableInARow && wctx.Err() == nil {
			unavailable++
			r.log.Infof("reading the nameserver slots: unavailable (%d of %d retries): %v", unavailable, maxUnavailableInARow, err)
		} else if err != nil {
			return fmt.Errorf("failed to read the nameserver slots of %s: %w", r.st.BaseDomain, err)
		} else {
			unavailable = 0
		}
		if done {
			r.log.Infof("delegating %s to %d nameservers", r.st.BaseDomain, len(nss))
			return r.d.dns.Delegate(ctx, r.st.BaseDomain, nss)
		}
		select {
		case <-wctx.Done():
			return fmt.Errorf("the first %d nodes did not all claim a nameserver slot for %s in %s", joined, r.st.BaseDomain, r.d.timing.delegation)
		case <-ticker.C:
		}
	}
}

// readSlots reads the slots once. done is true when each of the first
// joined nodes holds one; a slot at an address outside the fleet fails.
func (r *run) readSlots(ctx context.Context, joined int) ([]cloudflare.Nameserver, bool, error) {
	out, err := r.runPolled(ctx, "delegation", "node", "dns", "delegation", "--env", r.st.Env, "--json")
	if exitCode(err) == cliExitNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var ds []delegation
	if err := json.Unmarshal([]byte(out), &ds); err != nil {
		return nil, false, fmt.Errorf("failed to parse `orama node dns delegation --json`: %w", err)
	}
	var nss []cloudflare.Nameserver
	for _, d := range ds {
		if d.Domain == r.st.BaseDomain {
			nss = d.Nameservers
		}
	}
	held := map[string]bool{}
	for _, ns := range nss {
		if !fleetIP(r.st.Nodes, ns.IP) {
			return nil, false, fmt.Errorf("slot %s of %s is at %s, which is not a node of this run", ns.Hostname, r.st.BaseDomain, ns.IP)
		}
		held[ns.IP] = true
	}
	for _, n := range r.st.Nodes[:joined] {
		if !held[n.PublicIP] {
			return nil, false, nil
		}
	}
	return nss, true, nil
}

func fleetIP(nodes []fleet.Node, ip string) bool {
	for _, n := range nodes {
		if n.PublicIP == ip {
			return true
		}
	}
	return false
}

// runPolled runs the CLI with a fixed log per label, for commands repeated
// while waiting.
func (r *run) runPolled(ctx context.Context, label string, args ...string) (string, error) {
	c := command{name: r.st.OramaBin, args: args, env: r.cliEnv(),
		log: filepath.Join(r.cfg.ArtifactDir, "provision-poll-"+sanitize(label)+".log"), redact: r.red.Redact}
	return r.d.cmd.Run(ctx, c)
}
