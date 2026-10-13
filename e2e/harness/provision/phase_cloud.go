package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
	"golang.org/x/crypto/ssh"
)

// preflight checks everything that costs nothing before anything is built or
// paid for: the subdomain, the location and type, the quota, and that the
// run id is unused.
func (r *run) preflight(ctx context.Context) error {
	sub, err := r.d.dns.RunSubdomain(r.cfg.RunID)
	if err != nil {
		return err
	}
	r.st.BaseDomain, r.st.GatewayURL = sub, "https://"+sub
	locations := []string{r.cfg.Location}
	if r.cfg.ProbeLocation != "" && r.cfg.ProbeLocation != r.cfg.Location {
		locations = append(locations, r.cfg.ProbeLocation)
	}
	for _, loc := range locations {
		if err := r.d.cloud.ValidateLocation(ctx, loc); err != nil {
			return err
		}
		if err := r.d.cloud.ValidateServerType(ctx, r.cfg.ServerType, loc, nodeRequirements); err != nil {
			return err
		}
	}
	if err := r.checkRunIDUnused(ctx); err != nil {
		return err
	}
	if err := r.checkSubdomainUnused(ctx, sub); err != nil {
		return err
	}
	return r.d.cloud.CheckCapacity(ctx, serverCount(r.cfg), r.cfg.ServerLimit)
}

// checkRunIDUnused refuses a run id that already labels anything: tearing
// this run down by label would take the other run's resources with it.
func (r *run) checkRunIDUnused(ctx context.Context) error {
	sel := runSelector(r.cfg.RunID)
	servers, err := r.d.cloud.ListServers(ctx, sel)
	if err != nil {
		return fmt.Errorf("failed to check for servers of run %s: %w", r.cfg.RunID, err)
	}
	keys, err := r.d.cloud.ListSSHKeys(ctx, sel)
	if err != nil {
		return fmt.Errorf("failed to check for SSH keys of run %s: %w", r.cfg.RunID, err)
	}
	fws, err := r.d.cloud.ListFirewalls(ctx, sel)
	if err != nil {
		return fmt.Errorf("failed to check for firewalls of run %s: %w", r.cfg.RunID, err)
	}
	if n := len(servers) + len(keys) + len(fws); n > 0 {
		return fmt.Errorf("run id %s already has %d servers, %d SSH keys and %d firewalls labelled %s: "+
			"pick another run id or tear that run down", r.cfg.RunID, len(servers), len(keys), len(fws), sel)
	}
	return nil
}

// checkSubdomainUnused refuses a run whose subdomain already holds records:
// Down deletes everything under it, which would take another run's
// delegation with it.
func (r *run) checkSubdomainUnused(ctx context.Context, sub string) error {
	recs, err := r.d.dns.ListUnder(ctx, sub)
	if err != nil {
		return fmt.Errorf("failed to check for DNS records under %s: %w", sub, err)
	}
	if len(recs) > 0 {
		return fmt.Errorf("run id %s already has %d DNS records under %s (first: %s %s): "+
			"pick another run id or tear that run down", r.cfg.RunID, len(recs), sub, recs[0].Type, recs[0].Name)
	}
	return nil
}

// registerAccess registers the run's SSH key and firewall.
func (r *run) registerAccess(ctx context.Context) error {
	r.ownsCloud = true
	if strings.Join(r.cfg.RunnerCIDRs, ",") == anywhereCIDRs {
		r.log.Infof("the firewall lets SSH in from anywhere (%s); set %s to the runner's address to restrict it", anywhereCIDRs, EnvRunnerCIDR)
	}
	labels := runLabels(r.cfg.RunID, r.cfg.TTL)
	key, err := r.d.cloud.CreateSSHKey(ctx, envName(r.cfg.RunID), r.pubKey, labels)
	if err != nil {
		return err
	}
	r.sshKeyID = key.ID
	fw, err := r.d.cloud.CreateFirewall(ctx, envName(r.cfg.RunID), firewallRules(r.cfg.RunnerCIDRs), labels)
	if err != nil {
		return err
	}
	r.firewallID = fw.ID
	return nil
}

// createServers creates the nodes (and the probe), then waits for each to
// run. Each one is in the state as soon as it exists.
func (r *run) createServers(ctx context.Context) error {
	for i := 0; i < nodeCount; i++ {
		n, err := r.createServer(ctx, nodeSuffix(i), nodeName(i), fleet.RoleNameserver, r.cfg.Location)
		if err != nil {
			return err
		}
		r.st.Nodes = append(r.st.Nodes, n)
	}
	if r.cfg.ProbeLocation != "" {
		n, err := r.createServer(ctx, probeName, probeName, fleet.RoleNode, r.cfg.ProbeLocation)
		if err != nil {
			return err
		}
		r.st.Probes = append(r.st.Probes, n)
	}
	if err := r.checkpoint(); err != nil {
		return err
	}
	return r.waitServers(ctx)
}

// createServer generates the server's host key, pins it, and only then
// creates the server with the key in its user data.
func (r *run) createServer(ctx context.Context, suffix, name, role, location string) (fleet.Node, error) {
	hk, err := newServerHostKey(serverName(r.cfg.RunID, suffix))
	if err != nil {
		return fleet.Node{}, err
	}
	r.serverKeys[name] = hk.pub
	s, err := r.d.cloud.CreateServer(ctx, hetzner.CreateServerOpts{
		Name: serverName(r.cfg.RunID, suffix), ServerType: r.cfg.ServerType, Image: r.cfg.Image,
		Location: location, SSHKeyID: r.sshKeyID, FirewallID: r.firewallID,
		Labels: runLabels(r.cfg.RunID, r.cfg.TTL), UserData: hk.userData,
	})
	if err != nil {
		return fleet.Node{}, err
	}
	r.log.Infof("created %s (id %d) in %s", s.Name, s.ID, location)
	return fleet.Node{Name: name, Role: role, SSHUser: sshUser, ServerID: s.ID, Location: location}, nil
}

// waitServers fills in each server's public IP once it runs.
func (r *run) waitServers(ctx context.Context) error {
	for _, nodes := range [][]fleet.Node{r.st.Nodes, r.st.Probes} {
		for i := range nodes {
			wctx, cancel := context.WithTimeout(ctx, r.d.timing.serverBoot)
			s, err := r.d.cloud.WaitRunning(wctx, nodes[i].ServerID, r.d.timing.poll)
			cancel()
			if err != nil {
				return fmt.Errorf("%s did not boot: %w", nodes[i].Name, err)
			}
			nodes[i].PublicIP = s.IPv4()
			r.log.Infof("%s is running at %s", nodes[i].Name, nodes[i].PublicIP)
		}
	}
	return nil
}

// pinHostKeys writes each server's generated host key into known_hosts
// under its address, then confirms its sshd serves exactly that key, before
// any command, archive or invite is sent to it.
func (r *run) pinHostKeys(ctx context.Context) error {
	for _, n := range append(append([]fleet.Node{}, r.st.Nodes...), r.st.Probes...) {
		key, ok := r.serverKeys[n.Name]
		if !ok {
			return fmt.Errorf("%s (%s) has no generated host key", n.Name, n.PublicIP)
		}
		if err := pinAndConfirm(ctx, r.d, r.st.KnownHostsFile, n.PublicIP, key); err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
		r.hostKeys[n.PublicIP] = sshx.Fingerprint(key)
		r.log.Infof("pinned %s host key %s", n.Name, r.hostKeys[n.PublicIP])
	}
	return nil
}

// pinAndConfirm pins key for host in knownHosts and waits until host's sshd
// presents it.
func pinAndConfirm(ctx context.Context, d deps, knownHosts, host string, key ssh.PublicKey) error {
	if err := sshx.AppendKnownHost(knownHosts, host, key); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, d.timing.sshReady)
	defer cancel()
	if err := d.remote.ConfirmHostKey(cctx, host, key, d.timing.poll); err != nil {
		return fmt.Errorf("failed to confirm the host key of %s: %w", host, err)
	}
	return nil
}
