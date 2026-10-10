# Cluster state and membership

> **At a glance.**
>
> - **What:** the index RQLite is the cluster registry, one Raft group holding every node, namespace, key, DNS record and deployment row. This chapter covers how it starts without ever splitting, how its voters are chosen and evicted, how a ring of probes turns misses into a verdict, and how level-triggered loops repair what the registry says should exist.
> - **Key numbers:** Raft election 5 s, heartbeat 2 s, apply 30 s, leader lease 2 s; at most 5 voters; membership tick every 2 min; probe every 10 s with 3 s timeout, suspect after 3 misses, dead after 12, dead needs 2 observers within 5 min; a heartbeat younger than 65 s replaces the probe; tenant sweep every 60 s; 76 migrations.
> - **Code:** `core/pkg/namespace/index_bootstrap.go:indexJoinTargets`, `core/pkg/rqlite/identity.go:ResolveRaftIdentity`, `core/pkg/rqlite/eviction.go:evictDeadVoters`, `core/pkg/peerhealth/monitor.go:checkQuorum`, `core/pkg/node/membership/plan.go:BuildPlan`, `core/pkg/namespace/tenant_reconciler.go:reconcileTenantsOnce`.

## The registry

Everything true of the whole cluster lives in one SQLite database replicated by RQLite: which nodes exist, which namespaces they host, which keys are valid, which DNS records to serve. A gateway that cannot reach it cannot authenticate a key, and a node that cannot reach it cannot learn its WireGuard peers.

Four constraints shape its handling. Raft runs over the mesh the registry configures, so start-up splits into a local half that needs no quorum and a cluster half that does. Identity must not follow routing: RQLite names a member by its advertise address unless told otherwise, and where addresses are reassigned and nodes replaced that mints unreachable duplicate voters, so each node records the Raft id it runs under in a file and passes it explicitly (a libp2p peer id on new nodes). A lost disk must not look like a new cluster. And the registry and a tenant's database share one schema, so platform tables must not appear in a tenant database.

That last point is handled by placement. One set of embedded migrations builds both kinds of database; every table is classified as cluster-only or also-in-namespace, and a test fails when a migration creates an unclassified table. A namespace RQLite applies the same migrations with cluster-only statements stripped, tracked in its own table. Migrations take a cluster-wide lock, re-read the applied set inside it and run each migration as one transaction, so N gateways starting together do not each replay data migrations.

### Timing and reads

Every rqlited gets election 5 s, heartbeat 2 s, apply 30 s and leader lease 2 s. rqlite's one-second defaults mistook slow heartbeats on small, hypervisor-throttled VPSes for dead leaders, and namespace clusters elected every few seconds.

Reads default to `weak`: they go to the leader, and a leaderless node errors rather than serve stale rows. `none` reads, answered from the local SQLite in about a millisecond, exist for one job, reading the peer table when no leader exists, plus bounded-staleness serverless queries. A follower serves them only if its last leader contact is within 2 s and its apply gap is at most 50 entries; otherwise the read degrades to `weak`.

## Starting without splitting

rqlited bootstraps a new cluster when it has no Raft state and no join address. The genesis node has neither on a fresh install, and so does a node that lost its disk. The decision (`indexJoinTargets`) separates them with evidence kept outside the Raft directory: a small membership record listing the member addresses last seen.

![Index start decision: bootstrap, restart, rejoin, recover or refuse](../technical-reference/diagrams/ch07-boot-decision.svg)

A node with Raft state and an unchanged address restarts with no join, so a restart never depends on one peer answering; a changed address joins the other recorded members so the leader re-registers it. A node with no state and no record joins its configured address, or with none bootstraps: a fresh genesis. A node with no state but a record has lost its data; it joins the recorded members and, with none to join, refuses to start and names the two ways forward (`orama maint node recover-raft`, or delete the record to bootstrap deliberately). A node found with state and no record gets one written immediately. "Has Raft state" means a completed snapshot or a log entry, not the existence of `raft.db`, which rqlited creates before it joins.

Namespace databases use the same record and rules, so a namespace member that lost its data joins and never elects itself leader of an empty cluster while the others hold the data. A spawner check also refuses to join a target whose data directory belongs to another namespace, because a port collision once put a node into a foreign Raft group.

## Voters and eviction

The voter set is deterministic: all known Raft addresses sorted by numeric IP, first five. Every node computes the same set. Membership changes are made only by the leader, one per tick, and only after the Raft term has been stable for three ticks, since a configuration change issued during an election can be lost. A promotion or demotion is a single join request carrying the voter flag; an earlier remove-then-rejoin left the node outside the configuration for up to a minute. The leader is never demoted.

Without eviction a machine deleted without ceremony stays a voter forever, and with three voters the second such event is permanent quorum loss. Eviction (`evictDeadVoters`) therefore requires four independent signals, because each alone has a failure mode where a healthy node looks dead: Raft reports the voter unreachable; this leader saw it unreachable on ten consecutive ticks (about 20 minutes); peer discovery no longer knows its address; and at least two distinct observers recorded it `dead` in the last 30 minutes with no later recovery. A quorum-arithmetic check must also pass, and a tombstone is written before the removal so orphan recovery does not re-add the node. Before a leader stops, it transfers leadership to a reachable voter; the CLI's planned removals use the same quorum check.

## Failure detection

A restart must change nothing, a partition from one peer must not evict a healthy node, and a deleted machine must eventually be forgotten everywhere, because a dead voter counts toward quorum and a stale WireGuard peer is re-applied to every survivor every minute.

A full mesh of probes is quadratic, so each node watches a small deterministic subset. The ring is the `dns_nodes` rows heard from in the last 15 minutes, sorted by id; each node probes the next three and is observed by its three predecessors. Observations are combined through the registry, not gossip.

Every 10 seconds a node probes each neighbour's ping route over the mesh (3 s timeout). A heartbeat younger than 65 seconds counts as healthy without a request: a heartbeat is a Raft write, so a fresh one proves the node was alive and could reach a quorum. Three misses make a peer `suspect`, twelve (about two minutes) `dead`, and a healthy probe writes `recovered`. For a node's first five minutes nothing can be declared dead, so a fleet-wide restart does not condemn itself. Only transitions reach the database, as rows in `node_health_events`.

A suspect verdict disables the node's public IP in its namespaces' DNS records (never the last active record, enforced inside the `UPDATE` itself). A dead verdict needs quorum: two distinct observers within five minutes (`checkQuorum`), after which the lowest-id observer marks the node offline, fails its deployment replicas and replaces it in each namespace cluster. Because the quorum check runs once, as each observer writes its own verdict, if the lowest-id observer declares first no node ever calls recovery; the sweep below is the guarantee.

## Reconciliation

Edge-triggered repair works only if the event is delivered once to a process that survives long enough to act. So the code uses edges as accelerators and levels as the guarantee, every loop safe to run on every node at once: a guarded `UPDATE` whose row count says who won, a lease on a row, or a deterministic election among one namespace's members.

The membership reconciler runs on every node but acts only on the Raft leader, every 60 seconds. It builds a plan from evidence (`BuildPlan`, a pure function) and deletes the residue of a departed machine from `dns_nodes`, `wireguard_peers`, node credentials and system DNS records. Deletion needs positive proof of departure and any single sign of life vetoes it: discovery still sees the peer, or it was seen within 30 minutes. A node that merely stopped answering is missing, not gone; only a Raft tombstone makes it departed. A WireGuard row never confirmed and older than 30 minutes is the residue of a failed join. Confirmed rows with no matching node are reported, never deleted, since deleting the mesh entry of a machine that may still run would sever it. Within a node's deletion the credential is revoked first, so a crash leaves the safe half done and a departed node cannot resurrect itself into DNS.

![One sweep: eight steps in three groups](../technical-reference/diagrams/ch10-sweep.svg)

The tenant sweep (`reconcileTenantsOnce`) runs every 60 seconds on every node, first at once, in eight steps: restore missing namespace services, rewrite drifted configs, report RQLite liveness on the cluster status, tear down orphans, replay owed teardowns, resume abandoned deletes, fail abandoned provisioning, and prune departed members from the namespace Raft. Only the last elects a coordinator, the lowest id among live members.

Creation is cheap to undo and destruction is not, so the destructive steps refuse any registry read they cannot fully trust. An orphan (state on a node the registry does not assign to it) is torn down only after two consecutive sweeps, never when the registry read failed or is empty, never when the registry disowns every tenant on a node holding two or more (the signature of a restored old snapshot), at most two per pass. A stop a node did not confirm is recorded as an owed teardown, which keeps the port block reserved and the name uncreatable until it completes, because freeing a port under a live process once put a new namespace into a foreign Raft group.

When a node is confirmed dead, replacement selects a new node by load, allocates its port block, removes the dead member from the namespace Raft first, then spawns RQLite, Olric and the gateway there, swapping DNS. If the node is already live again it is reinstated instead. The most important limit of detection: the dead-voter gates can fail to hold together, since discovery forgets a peer only after two hours while the corroborating evidence expires after 30 minutes, so a voter lost without ceremony usually needs `orama node remove`.
