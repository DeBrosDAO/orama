package provision

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
	"golang.org/x/crypto/ssh"
)

// extraNamePattern is an extra's fleet name; it becomes part of a hostname.
var extraNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// extraLimits is what an extra is created under: the server type, and the
// project quota and TTL of the run (E2E_SERVER_LIMIT, E2E_TTL).
type extraLimits struct {
	serverType  string
	serverLimit int
	ttl         time.Duration
}

// AddExtra creates one more server for the run (a fourth node, a restore
// target) in location, labelled with the run, behind its firewall, reachable
// with its SSH key and with a host key generated and pinned before it
// exists. Nothing is installed on it. It is appended to st.Extras; the caller
// saves st.
//
// In a feature process (E2E_BROKER_SOCK set) the runner's broker creates it.
func AddExtra(ctx context.Context, st *fleet.State, name, location string) (fleet.Node, error) {
	b, err := runBroker()
	if err != nil {
		return fleet.Node{}, err
	}
	if b == nil {
		return Direct{}.AddExtra(ctx, st, name, location)
	}
	n, err := b.AddExtra(ctx, name, location)
	if err != nil {
		return fleet.Node{}, err
	}
	st.Extras = append(st.Extras, n)
	return n, nil
}

func addExtra(ctx context.Context, st *fleet.State, name, location string, lim extraLimits, d deps) (fleet.Node, error) {
	if err := checkExtraName(st, name); err != nil {
		return fleet.Node{}, err
	}
	if err := d.cloud.ValidateServerType(ctx, lim.serverType, location, nodeRequirements); err != nil {
		return fleet.Node{}, fmt.Errorf("extra %s: %w", name, err)
	}
	if err := d.cloud.CheckCapacity(ctx, 1, lim.serverLimit); err != nil {
		return fleet.Node{}, fmt.Errorf("extra %s: %w", name, err)
	}
	keyID, fwID, err := runAccess(ctx, st.RunID, d)
	if err != nil {
		return fleet.Node{}, err
	}
	hk, err := newServerHostKey(serverName(st.RunID, name))
	if err != nil {
		return fleet.Node{}, err
	}
	s, err := d.cloud.CreateServer(ctx, hetzner.CreateServerOpts{
		Name: serverName(st.RunID, name), ServerType: lim.serverType, Image: DefaultImage, Location: location,
		SSHKeyID: keyID, FirewallID: fwID, Labels: runLabels(st.RunID, lim.ttl), UserData: hk.userData,
	})
	if err != nil {
		return fleet.Node{}, fmt.Errorf("failed to create extra %s: %w", name, err)
	}
	n, err := bootAndPin(ctx, st, s.ID, hk.pub, d)
	if err != nil {
		// The caller's context may be what ended the boot: delete on one of
		// our own, or the server is left until the label sweep.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		n.Name = name
		return fleet.Node{}, errors.Join(fmt.Errorf("extra %s: %w", name, err), deleteAndForget(cctx, st, n, d))
	}
	n.Name, n.Role, n.SSHUser, n.Location = name, fleet.RoleNode, sshUser, location
	st.Extras = append(st.Extras, n)
	return n, nil
}

func checkExtraName(st *fleet.State, name string) error {
	if !extraNamePattern.MatchString(name) {
		return fmt.Errorf("extra name %q must be a lowercase letter then up to 19 letters, digits or '-'", name)
	}
	if _, _, ok := findMember(st, func(n fleet.Node) bool { return n.Name == name }); ok {
		return fmt.Errorf("%s is already a member of run %s", name, st.RunID)
	}
	return nil
}

// runAccess finds the run's one SSH key and one firewall by label.
func runAccess(ctx context.Context, runID string, d deps) (int64, int64, error) {
	keys, err := d.cloud.ListSSHKeys(ctx, runSelector(runID))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list the SSH keys of run %s: %w", runID, err)
	}
	fws, err := d.cloud.ListFirewalls(ctx, runSelector(runID))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list the firewalls of run %s: %w", runID, err)
	}
	if len(keys) != 1 || len(fws) != 1 {
		return 0, 0, fmt.Errorf("run %s has %d SSH keys and %d firewalls labelled %s, want one of each: is it up?",
			runID, len(keys), len(fws), runSelector(runID))
	}
	return keys[0].ID, fws[0].ID, nil
}

// bootAndPin waits for the server, pins the host key generated for it and
// confirms its sshd presents that key. The node it returns carries the
// server id (and the address, once known) even on failure, for cleanup.
func bootAndPin(ctx context.Context, st *fleet.State, id int64, key ssh.PublicKey, d deps) (fleet.Node, error) {
	n := fleet.Node{ServerID: id}
	wctx, cancel := context.WithTimeout(ctx, d.timing.serverBoot)
	s, err := d.cloud.WaitRunning(wctx, id, d.timing.poll)
	cancel()
	if err != nil {
		return n, fmt.Errorf("server %d did not boot: %w", id, err)
	}
	n.PublicIP = s.IPv4()
	if err := pinAndConfirm(ctx, d, st.KnownHostsFile, n.PublicIP, key); err != nil {
		return n, err
	}
	return n, nil
}

// RemoveExtra deletes the extra called name and drops it from st.Extras and
// the pinned host keys. An extra that is already gone is not an error; one
// missing from st but still labelled with the run in Hetzner is deleted.
//
// In a feature process (E2E_BROKER_SOCK set) the runner's broker deletes it.
func RemoveExtra(ctx context.Context, st *fleet.State, name string) error {
	b, err := runBroker()
	if err != nil {
		return err
	}
	if b != nil {
		return brokerRemoveExtra(ctx, b, st, name)
	}
	return Direct{}.RemoveExtra(ctx, st, name)
}

func removeExtra(ctx context.Context, st *fleet.State, name string, d deps) error {
	for i, n := range st.Extras {
		if n.Name == name {
			if err := deleteAndForget(ctx, st, n, d); err != nil {
				return err
			}
			st.Extras = append(st.Extras[:i], st.Extras[i+1:]...)
			return nil
		}
	}
	servers, err := d.cloud.ListServers(ctx, runSelector(st.RunID))
	if err != nil {
		return fmt.Errorf("failed to list the servers of run %s: %w", st.RunID, err)
	}
	for _, s := range servers {
		if s.Name == serverName(st.RunID, name) {
			return deleteAndForget(ctx, st, fleet.Node{Name: name, PublicIP: s.IPv4(), ServerID: s.ID}, d)
		}
	}
	return nil
}

// deleteAndForget deletes the extra n's server (after checking it is the
// run's server of that name), waits until it is gone, and unpins it.
func deleteAndForget(ctx context.Context, st *fleet.State, n fleet.Node, d deps) error {
	if err := deleteOwnedServer(ctx, d, st.RunID, n.ServerID, serverName(st.RunID, n.Name)); err != nil {
		return err
	}
	if err := waitServerGone(ctx, d, hetzner.Server{ID: n.ServerID, Name: n.Name}); err != nil {
		return err
	}
	if n.PublicIP == "" || st.KnownHostsFile == "" {
		return nil
	}
	return sshx.RemoveKnownHost(st.KnownHostsFile, n.PublicIP)
}

// depsFromEnv wires the real clients from the environment's tokens.
func depsFromEnv() (deps, error) {
	creds, err := credentialsFromEnv()
	if err != nil {
		return deps{}, err
	}
	return realDeps(creds)
}
