# The node and the mesh

> **At a glance.**
>
> - **What:** how one machine comes up and stays up. `orama-node` is a convergence supervisor that starts the real daemons as systemd units and retries instead of exiting; `orama-privhelper` is the one root process the unprivileged daemons ask for what needs root; WireGuard is the private mesh everything else rides on, kept equal to a registry table.
> - **Key numbers:** 22 boot components (18 local tier, 4 cluster tier); retry backoff 1 s doubling to 60 s; health polled every 30 s; shutdown grace 10 s inside a 60 s stop timeout; heartbeat 30 s; WireGuard sync 60 s; UDP 51820, MTU 1420, keepalive 25 s; helper allows 32 concurrent connections; gateway-minted invites live at most 1 h.
> - **Code:** `core/pkg/node/boot/supervisor.go:Supervisor`, `core/pkg/node/components.go:clusterBootComponents`, `core/pkg/privhelper/caller.go:Authorize`, `core/pkg/rootfs/rootfs.go:Root`, `core/pkg/node/wireguard_sync.go:reconcileWireGuardPeersWith`, `core/pkg/overlay/alloc.go:Register`.

![The cluster boot graph: local tier and cluster tier](../technical-reference/diagrams/ch04-boot-graph.svg)

## Converge, do not sequence

A node brings up about a dozen daemons, and several cannot start until others are healthy. The first implementation was a straight line: WireGuard, libp2p, storage, RQLite, DNS, gateway, edge. Any error aborted the process, systemd restarted it five seconds later, and the line began again. That is wrong when the fourth step waits on a Raft quorum: a node that boots while its peers are down never reached step five, so it served no HTTPS, no DNS and no tenants, though none of those needs a quorum. 

The supervisor inverts this. Each component declares its dependencies, an idempotent `Reconcile` and an optional `Health` check. The supervisor runs every component whose dependencies are ready and retries failures with backoff instead of exiting. A component is `blocked` (a dependency is not ready), `pending` or `ready`; when a dependency regresses, its dependents are blocked and reconciled again when it recovers.

One component, `rqlite-cluster`, waits for a Raft leader. The cluster tier is exactly the components that transitively depend on it: four of 22. Everything else is local tier and needs only the machine. A node alone in the world still brings up WireGuard, IPFS, its local RQLite replica, CoreDNS, the gateway, Caddy and its tenants. It reports `degraded` instead of `active` and returns to `active` by itself when quorum returns; nothing exited, so nothing needs a restart. Peers count `degraded` as available but verify it with an HTTP probe rather than trusting the claim.

Three properties matter in practice. Reconciles run on one goroutine, so a blocking one stalls everything behind it. Registration order is dependency order, so cycles cannot be expressed. "Ready" means systemd reports the unit active, which proves a process exists, not that it serves. Only a malformed graph, a role contradicting `preferences.yaml`, or a panic ends the process.

Some edges carry reasoning. `wireguard-sync` is local tier although it reads a cluster table: Raft runs over the mesh, and gating mesh repair on a quorum would make fixing the transport depend on the transport. `dns-registration` depends on the gateway and on edge serving, because a `dns_nodes` row saying `active` promises that this node terminates TLS and proxies tenants; ntfy and Tor sit in a separate component so a broken ntfy cannot take a healthy node out of DNS.

## Units

The node does not embed a gateway or exec a database. Install writes one host unit, `orama-node.service`, plus the WireGuard instance; every other daemon runs from an `orama-namespace-<service>@<instance>` template that the supervisor starts. Each unit carries its own sandbox, memory cap and stop timeout.

Every supervised unit has `Restart=always`, `RestartSec=5s` and no start limit, because a rate-limited unit refuses `systemctl start` until someone runs `reset-failed`. Nothing then parks a unit that fails instantly, so the node report flags more than three restarts with under five minutes of uptime.

Ordering is not lifecycle. `Requires=` propagates restarts, so it is reserved for the IPFS controllers; everything else is `Wants=` plus `After=`. Every supervised unit is `PartOf=orama-node.service`, so stopping the node stops them. WireGuard is deliberately not: it is a oneshot with a no-op stop, enabled beside the node, so the mesh is up at boot and a node with a broken supervisor is still reachable. `PartOf` once tore `wg0` down on every `orama node restart`, severing every namespace's Raft and Olric links on the machine.

Stateful services (RQLite, Olric, IPFS, IPFS Cluster, vault, WireGuard) are never restarted as a side effect of a changed input. The same reconcile runs on every node, and restarting voters on several nodes at once is what a rolling procedure exists to prevent.

On SIGTERM a node announces `maintenance` first, waits up to 10 seconds for the supervisor, then asks the index RQLite to hand leadership to a reachable voter if it leads, polling up to 15 seconds for the step-down. A node records itself in the registry through its own host's gateway with an Ed25519 signature over method, path, query, node id, body hash and timestamp, accepted within 60 seconds. The key is generated on first use and enrolled once, authenticated by the libp2p key embedded in the node's peer id, so no machine can enrol a key for a node it is not and a stolen disk is one node's credential, not the fleet's. Every 30 seconds a heartbeat refreshes the row; if Caddy is down for two ticks the node withdraws its A records, never the last of a name.

## Privilege

Most daemons run as one user, `orama`, because they share files, and several handle hostile input: tenant WebAssembly, an internet-facing proxy, tenant deployments. The question is what that uid can turn into.

A few actions need root: starting units, opening relay ports, persisting WireGuard peers, reading journals. Sudoers rules with wildcard arguments failed twice: the default `sudo-rs` refuses wildcards so the rules never loaded, and in classic sudo `ufw allow *` granted every firewall rule.

`orama-privhelper` replaces them. It is socket-activated, one instance per connection, so no root daemon holds state. It parses each request against an exact allow-list of eight tools with fixed argument shapes, no shell and no `PATH` lookup. A WireGuard peer, for instance, must be a canonical 32-byte key and one `/32` inside the overlay, and firewall ports are limited to the TURN listeners and one high UDP range.

The uid alone is not authorization, since every daemon is `orama`. The helper reads kernel-supplied peer credentials, then the caller's cgroup, and authorizes by the systemd unit the process runs in, which a process cannot change for itself. A pidfd and a start-time check close the pid-reuse race. Only three callers hold grants: root, the node (the whole allow-list), and the index gateway (a named subset). A tenant gateway gets nothing.

systemd is itself a confused deputy: PID 1 resolves `EnvironmentFile=`, `LoadCredential=` and bind sources as root, by name, following symlinks. If such a path sits in a tree the `orama` user can write, that user can point it at the WireGuard key and read it back through a process it controls. So every path PID 1 reads lives in a tree only root can write, and a test fails the build if a shipped template names one under the orama tree. The mirror problem is root writing into the orama tree during install: a planted symlink would redirect a root write to `/etc/sudoers.d`. The rootfs library walks each component by descriptor with `O_NOFOLLOW`, so none is looked up twice and a symlink anywhere is an error. Install also discards core dumps, sets `ptrace_scope` to 1 and masks swap; node reports flag drift.

The most important limit: most daemons still share the `orama` uid, so one compromised daemon can read a sibling's process environment; only CoreDNS and the SFU run as accounts of their own.

## The mesh

The commitment that no internal traffic crosses the public internet needs a private, authenticated network between machines at different providers. WireGuard supplies it with one UDP port and a Curve25519 key per node. Cryptokey routing proves a packet's inner source address by the key that carried it, so gateway code decides a request is "from a peer" by testing whether its source lies in `10.0.0.0/24`.

![The mesh: who writes wireguard_peers, who applies it to wg0](../technical-reference/diagrams/ch06-overview.svg)

Raft runs over the mesh, so a node that lost its peers can reach no voter and cannot read the table naming its peers; the sync therefore has a read path that works without a quorum. And `/etc/wireguard` belongs to root, because `wg-quick` runs `PostUp` lines as root, so unprivileged processes hand validated peers to the helper.

**Membership is a table.** The desired mesh is the registry's `wireguard_peers` table, written by join, enrolment, a peer endpoint and each node's own boot. One allocator serves them all: it picks the lowest free address from `.2` to `.254` (`.1` is genesis) and inserts with a plain `INSERT`. Two simultaneous joins can both choose `.7`; the loser hits a unique constraint and retries, at most eight times. Earlier allocators used `INSERT OR REPLACE`, so the loser silently deleted the winner's row, and highest-plus-one eventually rolled past `.254` into `10.0.1.x`, outside the subnet the firewall and peer check accept.

**Joining.** An invite is one string carrying the join URL, a 32-byte token and the SHA-256 fingerprint of the minting node's certificate. The joiner pins the fingerprint and refuses to join without one, because the token is a credential for every secret in the cluster; the registry stores only its hash. The join handler orders its work so nothing mutates before the token is spent: validate, check the token is live, refuse identities already claimed, read secrets and peers. A single atomic `UPDATE ... WHERE used_at IS NULL AND expires_at > now` then spends the token, and only after that does the handler clear unfinished rows and register the peer. A failed registration releases the token. The joiner pings the minting node's overlay address to verify the tunnel.

**The 60-second sync.** Each node re-asserts its own row with an upsert on `node_id`, so it cannot take another node's address or key, reads the desired set and reconciles `wg0`. The set comes from the leader-routed read when it answers, marked authoritative; otherwise from the node's own replica, which may only add peers. Removal needs an authoritative, non-empty read, because "read zero peers" and "the cluster has zero peers" look identical, the first is far more likely, and removing every peer severs the mesh beyond repair. The result is persisted to `wg0.conf` through the helper under a file lock, rewriting only `[Peer]` sections. A restarted node that lost its peers gets one additive repair from its own replica after `rqlited` listens and before anything waits for a leader.

Validation repeats at every edge (handler, allocator, table scan, helper), because a newline in a key would append directives to `wg0.conf` on every node.
