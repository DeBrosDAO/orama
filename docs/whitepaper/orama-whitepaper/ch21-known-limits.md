# Known limits

> **At a glance.**
>
> - **What:** the 28 most consequential gaps among the 619 the deep reference records, chosen across the whole book and grouped by theme. Each is something missing, incomplete or wrong in the code today.
> - **Key numbers:** 8 security, 6 data-loss and consistency, 6 consensus and upgrade, 5 payment correctness, 3 scaling and delivery gaps.
> - **Code:** every entry names the file and identifier that holds the gap.

Every item below describes the code as it is. Several are marked as bugs in their source chapter. None is a plan. They are chosen because the consequence reaches a tenant, an operator or the money, not because the code is untidy.

## Security and trust boundaries

- **An admin key in an operator-owned namespace is a cluster-operator credential.** The operator handlers resolve a key to the wallet that owns its namespace and test that wallet against the operator list. Any namespace `admin` can mint such a key without limiting its scope. A tenant role then escalates to minting cluster invites, rotating secrets and the signing key, and exporting or overwriting the registry. Marked high severity. `core/pkg/gateway/handlers/operator/handler.go:resolveWalletFromAPIKey`.
- **One database credential opens everything.** The host's single all-permission RQLite credential is written into every tenant gateway's configuration, and the same auth file is installed in every namespace RQLite. CoreDNS, the one process that parses unauthenticated packets on a public port, holds it too. Code execution in one tenant gateway or in the resolver reaches the registry with write access. `core/pkg/namespace/systemd_spawner.go:gatewayYAMLFor`.
- **Tenant gateways hold the cluster secret.** Every namespace gateway reads it, so it can derive the coordination and hop keys, spawn or tear down any namespace and assert any identity. A flaw in one tenant's gateway becomes cluster-wide. The same secret is the IPFS Cluster key and the WireGuard peer bearer, and nothing automates its rotation. `core/systemd/orama-namespace-gateway@.service`.
- **The vault's read and delete paths do not validate secret names.** Only the write path checks. A traversing name lets identity A read and delete identity B's secret, and `..` addresses the whole store. Exposure is limited to callers that can reach the guardian on the overlay. `vault/src/server/handler_secrets.zig:handleGet`.
- **The cache and the node-local IPFS gateway are open to anything on the overlay or the host.** Olric's client port and memberlist are unauthenticated and unencrypted, so any overlay process can read or write any namespace's cache. Kubo's read-only HTTP gateway skips ownership, quota and audit for any process that knows a CID. `core/pkg/olric/client.go:clientConfig`, `core/pkg/install/installers/ipfs.go:configureAddresses`.
- **TURN relays to any peer address.** The server installs no permission handler, so a holder of any namespace's credential can send UDP from a relay port to the host's loopback and overlay addresses. `core/pkg/turn/server.go:NewServer`.
- **Refresh-token replay does not revoke the session.** If a thief refreshes first, the legitimate client is refused as the replayer, and the thief's chain continues until the owner signs in again. `core/pkg/gateway/auth/service.go:RefreshToken`.
- **The encryption root sits in plaintext beside the data it protects and is never forgotten.** It is replicated by Raft and appears in every registry snapshot. A second rotation overwrites the previous slot, so any row the first walk missed becomes unreadable. `core/pkg/secrets/root.go:Rotate`.

## Data loss and consistency

- **A SQLite create can delete an existing database's file.** A registry read failure is treated as "does not exist". When the row insert then fails on the uniqueness constraint, the handler removes the file. An RQLite outage, or two concurrent creates of one name, destroys a live database. `core/pkg/gateway/handlers/sqlite/create_handler.go:CreateDatabase`.
- **SQLite databases have one copy and no restore.** A lost home node makes its databases unreachable, with no reassignment and no route that restores a file from a backup CID. `core/pkg/gateway/routes.go`.
- **The index registry has no off-cluster backup.** If every copy of the index database is lost, nothing in the codebase restores it. `core/cmd/orama/internal/cmd/namespacecmd/backup.go`.
- **A failed ownership write strands an uploaded object.** The upload still answers 200 and pins the bytes, but the owner cannot download, pin or unpin them. `core/pkg/gateway/handlers/storage/upload_handler.go:UploadHandler`.
- **The vault never repairs or re-shares.** The repair and re-sharing code is tested and unreferenced. A guardian that joins holds nothing for existing secrets, and one that leaves takes its shares, so durability decays with fleet churn. `vault/src/peer/repair.zig`.
- **Registry transactions are not atomic.** The `client.Tx` helper sits on a driver whose begin, commit and rollback are no-ops, so a failure part-way leaves earlier statements committed. `core/pkg/serverless/registry_versions.go`.

## Consensus, upgrade and release

- **Dead-voter eviction cannot complete for a node that stays dead.** The two evidence windows, discovery's 2 hours and the 30-minute dead-row window, do not overlap, so a deleted voter stays in the Raft configuration and counts toward quorum. `core/pkg/rqlite/eviction.go:evictDeadVoters`.
- **The rolling-upgrade gate watches only the index.** It never reads a tenant namespace's RQLite, Olric or gateway. Two overlapping restarts across nodes can take a three-member tenant cluster below quorum. `core/pkg/rollout/probe.go:WaitReady`.
- **One- and two-voter clusters cannot be rolled, and no override exists.** The refusal text suggests a flag that does something else. `core/cmd/orama/internal/production/lifecycle/quorum.go:evaluateQuorumSafety`.
- **No release gate runs the fleet suite, and a dirty tree is signed as its clean commit.** Documents say `release.sh` refuses a commit without a green fleet report, but it has no such check, and a build from uncommitted changes carries a commit that does not describe it. `core/cmd/orama/version.go:buildInfo`.
- **Chain tests are in no automated gate.** `chain/` is a separate module that the root test target and CI do not run, so the chain side of the shared test vectors is unchecked by automation. `.github/workflows/ci.yml`.
- **A joiner starts on the genesis binary.** A snapshot taken after a chain upgrade needs that upgrade's binary, the release metadata lists no upgrade heights, and `orama setup` can only end its sync wait with the chain's log. `core/cmd/orama/internal/setup/phase_global.go:waitSynced`.
- **Governed upgrade heights are absolute.** A proposal fixes a block height 60 days ahead and fails at execution if the height has passed. `chain/x/houses/keeper/proposal.go:checkUpgradeHeight`.

## Payment and proof correctness

- **Storage challenges are predictable.** The leaf index derives only from public values, so a provider can compute every future challenge and keep only those leaves. `chain/x/storage/types/assignment.go:LeafChallengeSeed`.
- **Storage pay follows the sample, not the holdings.** A node holding 100 replicas is challenged on about 8 per epoch, so its income per replica falls as it grows. `chain/x/storage/keeper/challenge.go:openNodeChallenges`.
- **Archive attestation is agreement, not verification.** The chain cannot compare a Merkle root with past block hashes, so three colluding ARCHIVER operators can pin a false history, and no other tuple can win afterwards. A single range that never decides also stalls pruning for every later range. `chain/x/archive/keeper/attest.go:attestCandidate`, `chain/x/archive/types/genesis.go:ContiguousArchivedHeight`.
- **Relay pay needs no bond and stops if one reporter is missing.** Registration and settlement never check a RELAY bond. Quorum equals the number of initial authorities, so one absent reporter mints nothing for the epoch, and the ceiling is not carried forward. `chain/x/relay/keeper/register.go`, `chain/x/relay/keeper/settle.go:SettleEpoch`.
- **The shielded pool's two verifiers share one codebase.** Both call upstream `orchard`, so a logic bug in its circuit passes both. The turnstile and the 24-hour cap bound the loss but do not prevent it. `chain/x/shielded/verify/orchardproc/orchardproc.go`.

## Scaling and delivery

- **Per-block table scans grow with all history.** Accept-window expiry, deal expiry and probation expiry walk every slot, deal and node each block, and deals are never deleted. `chain/x/storage/keeper/assign.go:expireAcceptWindows`.
- **The registry has one writer and a fixed voter cap.** Every registry write is a Raft entry in one group, nodes beyond five add read replicas and not write capacity, and five voters tolerate two simultaneous failures however large the fleet grows.
- **Pub/sub messages near 1 MiB fail silently or kill a stream.** A message of 786,427 bytes or more ends the delivery stream for every subscriber on that topic on each gateway that receives it, and messages near 1 MiB reach only the publishing node while the publisher gets a 200. `core/pkg/gateway/handlers/pubsub/publish_handler.go:MaxPerMessageBytes`.

Two advertised features also do not work end to end. Stealth TURN routing finds no routes since TURN became one shared server per host (`core/pkg/sniproxy/discovery.go`), and custom domains are neither routed nor given a certificate (`core/pkg/gateway/middleware.go`).

The full list of 619 gaps lives in the deep technical reference and in bugboard task 3238.
