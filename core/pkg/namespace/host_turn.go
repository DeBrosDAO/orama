package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// hostTURNConfigPath is where the shared TURN server reads its config, matching
// the path baked into orama-turn.service. Host-level (alongside node.yaml), not
// under namespaces/ — the process belongs to the host, not to any one namespace.
//
// A var, not a const, only so tests can redirect it into a temp dir; nothing in
// production reassigns it.
var hostTURNConfigPath = "/opt/orama/.orama/configs/turn.yaml"

// hostTURNTenant is one namespace's contribution to the shared TURN config,
// paired with the listener ports it was allocated. Those ports really are
// host-wide — the allocator pins every TURN block to TURNDefaultPort/TURNSPort —
// so a disagreement is a bug, not a case to merge.
//
// The per-namespace RELAY range is deliberately NOT carried here. The allocator
// hands each namespace the next free 800-port block on the host (49152-49951,
// then 49952-50751, …), so two tenants never share one — but a single process
// has a single relay range. The shared server therefore relays from the
// host-wide range, and the per-namespace blocks stay what they always were
// underneath: a record of which namespaces hold TURN on which node.
type hostTURNTenant struct {
	tenant     turn.TenantConfig
	listenPort int
	tlsPort    int
}

// ReconcileHostTURN brings the shared, host-level TURN server in line with the
// TURN allocations this node currently holds across ALL its namespaces.
//
// This is the lifecycle half of bugboard #283. TURN binds 3478/5349, which are
// exclusive per host, so the old model — one unit per namespace — meant the
// second namespace on any node crash-looped on bind and got no relay at all.
// One process now serves every tenant, authenticating each against its own
// secret (the credential already carries the namespace).
//
// Tenant changes are written to the config file and picked up by the running
// server WITHOUT a restart. That is deliberate and load-bearing: a restart drops
// every tenant's active relays, so restarting whenever any namespace enables or
// disables WebRTC would turn a routine per-namespace operation into a host-wide
// call drop. The unit is only started (when it should run and does not) or
// stopped (when this node holds no TURN allocation at all).
//
// Returns the namespaces this host is actually configured to relay for, so
// callers can advertise DNS for exactly those. Holding an allocation is NOT the
// same thing: a namespace with no shared secret is dropped from the config, and
// a failed config write leaves the server serving the previous set — advertising
// on the allocation alone would point clients at a relay that rejects them.
func (cm *ClusterManager) ReconcileHostTURN(ctx context.Context) []string {
	if cm.systemdSpawner == nil || cm.systemdSpawner.systemdMgr == nil || cm.localNodeID == "" {
		return nil
	}

	tenants, err := cm.desiredHostTURNTenants(ctx)
	if err != nil {
		// Positive evidence only: an unreadable control plane must never be read
		// as "this node serves nobody". Stopping TURN on a transient rqlite blip
		// would drop every relay on the host, and nothing would restart them
		// until the next sweep — the same failure shape as
		// stopUnallocatedWebRTCServices guards against.
		cm.logger.Warn("Host TURN reconcile skipped: could not determine allocations",
			zap.Error(err))
		return nil
	}

	if len(tenants) == 0 {
		cm.stopHostTURNAndLegacyUnits(ctx)
		return nil
	}

	changed, err := cm.writeHostTURNConfig(ctx, tenants)
	if err != nil {
		cm.logger.Warn("Failed to write shared TURN config", zap.Error(err))
		return nil
	}

	running, aerr := cm.systemdSpawner.systemdMgr.IsHostTURNActive()
	if aerr != nil {
		cm.logger.Warn("Could not determine shared TURN service state", zap.Error(aerr))
		return nil
	}
	// Open the relay ports before starting. The root-level firewall phase only
	// runs at install/upgrade, so a node that GAINS a TURN allocation between
	// upgrades would otherwise relay behind a closed UFW — clients reach ICE
	// "checking" and never connect (bugboard #846). Now that host occupancy no
	// longer blocks an allocation, gaining one mid-life is the common case.
	cm.openTURNRelayPorts()

	if !running {
		// Retire the pre-#283 per-namespace units FIRST. They hold 3478/5349, so
		// starting the shared server while one is up fails with "address already
		// in use" — and if that failure returned early, the legacy unit would
		// never be stopped and the migration could never complete on that node.
		// The order costs a brief TURN gap on the upgrade tick, which is the
		// unavoidable price of moving the process; leaving it stuck is not.
		cm.stopLegacyPerNamespaceTURN(ctx)

		if serr := cm.systemdSpawner.systemdMgr.StartHostTURN(); serr != nil {
			// Error, not Warn: the legacy units were just stopped, so this node is
			// now relaying for nobody. The most likely cause is a missing sudoers
			// grant for orama-turn.service, which produces "command not allowed"
			// and is silent apart from this line.
			cm.logger.Error("Shared TURN service failed to start; this node is relaying for NO namespace. If the cause is 'command not allowed', the sudoers grant for orama-turn.service is missing on this host.",
				zap.Strings("tenants", tenantNames(tenants)), zap.Error(serr))
			return nil
		}
		cm.logger.Info("Started shared TURN service (bugboard #283)",
			zap.Strings("tenants", tenantNames(tenants)))
		return tenantNames(tenants)
	}

	if changed {
		// The file changed but the process keeps running: it re-reads its tenant
		// list on its own. Logged so the tenant set is visible in the journal.
		cm.logger.Info("Shared TURN tenant set updated (no restart — a restart would drop every tenant's relays)",
			zap.Strings("tenants", tenantNames(tenants)))
	}
	// Catch any legacy unit that came back (a node restart re-runs the old
	// restore path until it is fully upgraded). Idempotent.
	cm.stopLegacyPerNamespaceTURN(ctx)
	return tenantNames(tenants)
}

// desiredHostTURNTenants collects every namespace on this node that holds a TURN
// allocation and has WebRTC configured, as tenants of the shared server.
func (cm *ClusterManager) desiredHostTURNTenants(ctx context.Context) ([]hostTURNTenant, error) {
	pattern := filepath.Join(cm.baseDataDir, "*", "cluster-state.json")
	matches, gerr := filepath.Glob(pattern)
	if gerr != nil {
		return nil, fmt.Errorf("glob namespace states: %w", gerr)
	}

	var out []hostTURNTenant
	for _, path := range matches {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		state, lerr := loadLocalState(path)
		if lerr != nil || state == nil || state.ClusterID == "" {
			continue
		}
		// Same staleness guard as the WebRTC sweep: a surviving state file from a
		// deprovisioned namespace names a cluster ID that no longer exists, and a
		// lookup against it returns "no rows" cleanly — which would silently drop
		// a live tenant from the config.
		cluster, cerr := cm.GetClusterByNamespace(ctx, state.NamespaceName)
		if cerr != nil || cluster == nil || cluster.ID != state.ClusterID {
			continue
		}

		blk, berr := cm.webrtcPortAllocator.GetTURNPorts(ctx, state.ClusterID, cm.localNodeID)
		if berr != nil {
			return nil, fmt.Errorf("read TURN allocation for %s: %w", state.NamespaceName, berr)
		}
		if !turnPortBlockSpawnable(blk) {
			continue // no allocation here, or an incomplete one
		}

		webrtcCfg, werr := cm.GetWebRTCConfig(ctx, state.NamespaceName)
		if werr != nil {
			return nil, fmt.Errorf("read WebRTC config for %s: %w", state.NamespaceName, werr)
		}
		if webrtcCfg == nil || webrtcCfg.TURNSharedSecret == "" {
			// An allocation with no secret cannot authenticate anyone. Skipping
			// keeps the tenant out rather than writing a config the server would
			// reject wholesale, taking the other tenants down with it.
			cm.logger.Warn("Skipping TURN tenant with no shared secret",
				zap.String("namespace", state.NamespaceName))
			continue
		}

		t := hostTURNTenant{
			tenant:     turn.TenantConfig{Namespace: state.NamespaceName, AuthSecret: webrtcCfg.TURNSharedSecret},
			listenPort: blk.TURNListenPort,
			tlsPort:    blk.TURNTLSPort,
		}

		if stealth := cm.stealthDomainFor(state.NamespaceName, webrtcCfg); stealth != "" {
			certPath, keyPath, serr := cm.systemdSpawner.resolveStealthCert(stealth, cm.baseDomain)
			if serr != nil {
				// Stealth never falls back to a self-signed cert: a cert clients
				// reject is indistinguishable from being blocked. Serve the tenant
				// without stealth rather than failing the whole host's config.
				cm.logger.Warn("Stealth TURNS cert unavailable; tenant served without stealth",
					zap.String("namespace", state.NamespaceName), zap.Error(serr))
			} else {
				t.tenant.StealthDomain = stealth
				t.tenant.TLSStealthCertPath = certPath
				t.tenant.TLSStealthKeyPath = keyPath
			}
		}

		out = append(out, t)
	}

	// Deterministic order so an unchanged tenant set produces an identical file
	// and does not look like a change on every sweep.
	sort.Slice(out, func(i, j int) bool { return out[i].tenant.Namespace < out[j].tenant.Namespace })
	return out, nil
}

// writeHostTURNConfig renders the shared config and writes it atomically,
// reporting whether the contents actually changed.
//
// Requires a non-empty tenants slice — the listener settings are read from the
// first entry. ReconcileHostTURN handles the empty case before calling (an empty
// set means "stop TURN here", not "write a config that serves nobody").
//
// The file carries every tenant's HMAC secret for this host, so it is written
// 0600. The per-namespace configs this replaces were 0644, which exposed a
// namespace's TURN secret to any local user.
func (cm *ClusterManager) writeHostTURNConfig(ctx context.Context, tenants []hostTURNTenant) (bool, error) {
	publicIP, perr := cm.getLocalNodePublicIP(ctx)
	if perr != nil || publicIP == "" {
		// An empty public_ip hard-fails the TURN server and systemd then
		// crash-loops it forever (bugboard #846) — refuse to write it.
		return false, fmt.Errorf("public IP unresolved: %w", perr)
	}

	listen, tls := tenants[0].listenPort, tenants[0].tlsPort
	for _, t := range tenants[1:] {
		if t.listenPort != listen || t.tlsPort != tls {
			// The allocator pins these to the well-known ports for every block, so
			// a disagreement means it produced something a single shared server
			// cannot honour — surface it instead of silently picking one.
			return false, fmt.Errorf("tenants on this host disagree on TURN listener ports: %s wants %d/%d, %s wants %d/%d",
				tenants[0].tenant.Namespace, listen, tls,
				t.tenant.Namespace, t.listenPort, t.tlsPort)
		}
	}

	cfg := turn.Config{
		ListenAddr: fmt.Sprintf("0.0.0.0:%d", listen),
		PublicIP:   publicIP,
		Realm:      cm.baseDomain,
		// The host-wide relay range, NOT any one tenant's 800-port block: one
		// process has one range, and every tenant relays from it. This is also
		// exactly the range the root-level firewall phase opens, so the two can
		// never drift into a server relaying on ports UFW drops.
		RelayPortStart: TURNRelayPortRangeStart,
		RelayPortEnd:   TURNRelayPortRangeEnd,
	}
	for _, t := range tenants {
		cfg.Tenants = append(cfg.Tenants, t.tenant)
	}

	// TURNS uses the zone wildcard cert, which covers every tenant's
	// turn-<ns>.<base> host AND every tenant's cdn-<hash>.<base> stealth host,
	// so one cert serves the whole shared listener.
	//
	// Self-signed is NOT accepted here (allowSelfSigned=false), unlike the
	// per-namespace spawn this replaces. A shared listener answers stealth
	// hostnames too, and a cert clients reject is indistinguishable from being
	// censored — the one outcome the stealth design forbids. Worse, a tenant
	// whose own stealth cert failed to load falls back to this primary cert by
	// SNI, so a self-signed primary would quietly poison exactly the endpoint
	// that must not fail. Disabling TURNS instead leaves clients on plain TURN
	// (3478), which works.
	if tls > 0 {
		certPath, keyPath, cerr := cm.systemdSpawner.resolveTURNSCert(
			"", "", cm.baseDomain, publicIP, filepath.Dir(hostTURNConfigPath), false)
		if cerr != nil {
			cm.logger.Warn("No CA-valid wildcard cert for the shared TURN server; TURNS disabled (clients use plain TURN on 3478). Stealth endpoints on this host will not serve until the wildcard exists.",
				zap.String("base_domain", cm.baseDomain), zap.Error(cerr))
		} else {
			cfg.TURNSListenAddr = fmt.Sprintf("0.0.0.0:%d", tls)
			cfg.TLSCertPath = certPath
			cfg.TLSKeyPath = keyPath
		}
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		return false, fmt.Errorf("refusing to write an invalid shared TURN config: %v", errs[0])
	}

	data, merr := yaml.Marshal(cfg)
	if merr != nil {
		return false, fmt.Errorf("marshal shared TURN config: %w", merr)
	}

	if existing, rerr := os.ReadFile(hostTURNConfigPath); rerr == nil && string(existing) == string(data) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(hostTURNConfigPath), 0755); err != nil {
		return false, fmt.Errorf("create shared TURN config dir: %w", err)
	}
	if err := writeConfigAtomic(hostTURNConfigPath, data, 0600); err != nil {
		return false, fmt.Errorf("write shared TURN config: %w", err)
	}
	return true, nil
}

// stopHostTURNAndLegacyUnits tears TURN down on a node that holds no allocation.
func (cm *ClusterManager) stopHostTURNAndLegacyUnits(ctx context.Context) {
	running, err := cm.systemdSpawner.systemdMgr.IsHostTURNActive()
	if err == nil && running {
		if serr := cm.systemdSpawner.systemdMgr.StopHostTURN(); serr != nil {
			cm.logger.Warn("Failed to stop shared TURN service", zap.Error(serr))
			return
		}
		cm.logger.Info("Stopped shared TURN service: this node holds no TURN allocation")
	}

	// Remove the config too. It holds every tenant's HMAC secret, and leaving it
	// behind on a node that no longer relays is the same stale-secret-on-disk
	// problem the legacy 0644 cleanup exists to fix. It also keeps hostRunsTURN()
	// true forever, so the firewall phase would go on holding the relay range
	// open on a node with no TURN at all.
	if rerr := os.Remove(hostTURNConfigPath); rerr != nil && !os.IsNotExist(rerr) {
		cm.logger.Warn("Failed to remove the shared TURN config on a node that no longer relays; it still holds every tenant's HMAC secret",
			zap.String("path", hostTURNConfigPath), zap.Error(rerr))
	}

	cm.stopLegacyPerNamespaceTURN(ctx)
}

// stopLegacyPerNamespaceTURN retires the pre-#283 orama-namespace-turn@<ns>
// units, whose job the shared server has taken over.
//
// Without this the old unit keeps holding 3478/5349 and the shared server can
// never bind — the upgrade would look successful and leave TURN exactly as
// broken as before. Idempotent: stopping an already-stopped or never-installed
// unit is not an error, so this is safe to run on every sweep forever.
func (cm *ClusterManager) stopLegacyPerNamespaceTURN(ctx context.Context) {
	matches, err := filepath.Glob(filepath.Join(cm.baseDataDir, "*", "cluster-state.json"))
	if err != nil {
		return
	}
	for _, path := range matches {
		if ctx.Err() != nil {
			return
		}
		state, lerr := loadLocalState(path)
		if lerr != nil || state == nil || state.NamespaceName == "" {
			continue
		}
		ns := state.NamespaceName

		// Stop and disable UNCONDITIONALLY, without consulting IsServiceActive.
		//
		// That check reports a crash-looping unit as NOT active — and after the
		// config migration the legacy units crash-loop by construction, because
		// their configs are gone. Gating on "active" therefore skips exactly the
		// units that most need retiring, leaving them restarting forever. Both
		// calls are idempotent on an already-stopped or never-installed unit.
		//
		// Disabling matters as much as stopping: without it the unit returns on
		// the next boot and starts crash-looping again.
		if serr := cm.systemdSpawner.systemdMgr.StopService(ns, systemd.ServiceTypeTURN); serr != nil {
			cm.logger.Warn("Failed to stop legacy per-namespace TURN unit",
				zap.String("namespace", ns), zap.Error(serr))
		}
		if derr := cm.systemdSpawner.systemdMgr.DisableService(ns, systemd.ServiceTypeTURN); derr != nil {
			cm.logger.Warn("Failed to disable legacy per-namespace TURN unit; it will return on the next boot",
				zap.String("namespace", ns), zap.Error(derr))
		}

		// Deleting the legacy config is NOT conditional on having just stopped the
		// unit. On a node whose legacy TURN was already down, the unit check above
		// finds nothing — and the 0644 secret file would sit there forever, which
		// is the whole exposure this removes. filepath.Dir(path) is the directory
		// the glob actually matched, so the delete can never be steered by the
		// namespace name recorded inside the state file.
		cm.removeLegacyTURNConfig(ns, filepath.Dir(path))
	}
}

// removeLegacyTURNConfig deletes the retired per-namespace TURN config.
//
// Those files were written 0644 and contain the namespace's HMAC secret, so any
// local user could read them and mint valid TURN credentials for that namespace.
// The shared config is 0600 — but stopping the old unit without deleting its
// config would leave exactly the exposure the tighter mode exists to close, on
// precisely the hosts this migration touches.
func (cm *ClusterManager) removeLegacyTURNConfig(namespace, namespaceDir string) {
	// turn.env is what marks the unit "provisioned": both the upgrade's rolling
	// restart and hostRunsTURN() enumerate namespaces by the presence of this
	// file. Leaving it behind makes the upgrade restart a configless unit into a
	// permanent crash-loop, so the migration is not complete without removing it.
	envPath := filepath.Join(namespaceDir, "turn.env")
	if eerr := os.Remove(envPath); eerr != nil && !os.IsNotExist(eerr) {
		cm.logger.Warn("Failed to remove the legacy TURN env file; the upgrade will keep restarting a configless unit",
			zap.String("namespace", namespace), zap.String("path", envPath), zap.Error(eerr))
	}

	path := filepath.Join(namespaceDir, "configs",
		fmt.Sprintf("turn-%s.yaml", cm.localNodeID))
	err := os.Remove(path)
	switch {
	case err == nil:
		cm.logger.Info("Removed the legacy 0644 per-namespace TURN config",
			zap.String("namespace", namespace), zap.String("path", path))
	case os.IsNotExist(err):
		// Already gone — the common case on every sweep after the first, and on
		// nodes that never ran a per-namespace TURN. Silent by design: this runs
		// once a minute per namespace forever.
	default:
		cm.logger.Warn("Failed to remove the legacy TURN config; it is mode 0644 and still exposes this namespace's HMAC secret to any local user",
			zap.String("namespace", namespace), zap.String("path", path), zap.Error(err))
	}
}

// tenantNames lists the namespaces in a tenant set, for logging.
func tenantNames(tenants []hostTURNTenant) []string {
	out := make([]string, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, t.tenant.Namespace)
	}
	return out
}

// openTURNRelayPorts (re)opens the host-wide TURN relay range in UFW.
//
// Idempotent — `ufw allow` on an existing rule is a no-op — so it is safe on
// every reconcile. It opens the same host-wide range the shared server relays
// from and that the root-level firewall phase uses, so the two cannot drift into
// a server relaying on ports UFW drops.
//
// orama-node runs unprivileged; the sudoers drop-in grants exactly the `ufw`
// verbs this needs.
func (cm *ClusterManager) openTURNRelayPorts() {
	fw := production.NewFirewallProvisioner(production.FirewallConfig{})
	if err := fw.AddWebRTCRules(TURNRelayPortRangeStart, TURNRelayPortRangeEnd); err != nil {
		// Not fatal: on a node whose firewall phase already opened these, TURN
		// works regardless. Loud because the failure mode when they are NOT open
		// is invisible — calls reach ICE "checking" and simply never connect.
		cm.logger.Warn("Failed to open TURN relay ports; if they are not already open, relays will accept no traffic",
			zap.Int("relay_start", TURNRelayPortRangeStart),
			zap.Int("relay_end", TURNRelayPortRangeEnd),
			zap.Error(err))
	}
}
