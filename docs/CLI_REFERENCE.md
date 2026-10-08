<!--
Generated from the cobra command tree by core/cmd/orama/reference_test.go.
Do not edit by hand: run `make -C core docs`.
-->

# CLI reference

Every command the `orama` binary defines, with its flags. Generated from the
command tree, so it cannot drift from the code: a test fails when this file and
the tree disagree.

Who uses this binary, and what does not exist (no dashboard, no Orama MCP), is
[CLIENT_SURFACE.md](CLIENT_SURFACE.md). Task-shaped documentation lives
elsewhere — [deploying apps](DEPLOYMENT_GUIDE.md), [building and rolling
out](DEV_DEPLOY.md), [functions](SERVERLESS.md). This page is the index.

## Commands

- [`orama app`](#orama-app) — Manage deployed applications
  - [`orama app delete`](#orama-app-delete) — Delete a deployment
  - [`orama app env`](#orama-app-env) — Manage an app's environment variables
    - [`orama app env list`](#orama-app-env-list) — List an app's environment variable names
    - [`orama app env set`](#orama-app-env-set) — Set environment variables and restart the app
    - [`orama app env unset`](#orama-app-env-unset) — Remove environment variables and restart the app
  - [`orama app get`](#orama-app-get) — Get deployment details
  - [`orama app grants`](#orama-app-grants) — Say what a deployed app may do, as itself
    - [`orama app grants list`](#orama-app-grants-list) — Show what deployments in this namespace may do
    - [`orama app grants set`](#orama-app-grants-set) — Grant a deployment a role
  - [`orama app list`](#orama-app-list) — List all deployments
  - [`orama app logs`](#orama-app-logs) — Stream deployment logs
  - [`orama app rollback`](#orama-app-rollback) — Rollback a deployment to a previous version
  - [`orama app stats`](#orama-app-stats) — Show resource usage for a deployment
- [`orama audit`](#orama-audit) — Read this namespace's audit trail
- [`orama auth`](#orama-auth) — Authentication management
  - [`orama auth approve`](#orama-auth-approve) — Approve a login waiting on another machine
  - [`orama auth list`](#orama-auth-list) — List all stored credentials
  - [`orama auth login`](#orama-auth-login) — Sign in, here or from another machine
  - [`orama auth logout`](#orama-auth-logout) — End this session on the gateway and clear it here
  - [`orama auth sessions`](#orama-auth-sessions) — Which machines are signed in as this wallet
    - [`orama auth sessions revoke`](#orama-auth-sessions-revoke) — End one session, or every one
  - [`orama auth status`](#orama-auth-status) — Show what is stored on this machine, without asking the gateway
  - [`orama auth switch`](#orama-auth-switch) — Switch between stored credentials
  - [`orama auth whoami`](#orama-auth-whoami) — Ask the gateway who this credential is and what it may do
- [`orama build`](#orama-build) — Build pre-compiled binary archive for deployment
- [`orama chain`](#orama-chain) — Read the Orama chain: status, balances, earnings, nodes, deals, validators; fund test accounts
  - [`orama chain balance`](#orama-chain-balance) — Show an account's bank balances
  - [`orama chain deal`](#orama-chain-deal) — Show a storage deal (x/storage)
  - [`orama chain earnings`](#orama-chain-earnings) — Show an account's earnings balance (x/fees)
  - [`orama chain faucet`](#orama-chain-faucet) — Fund an account on a test network (stagenet, devnet)
  - [`orama chain node`](#orama-chain-node) — Show a registered node (x/nodes)
  - [`orama chain query`](#orama-chain-query) — Run any Orama module query through the gateway or --rpc
  - [`orama chain status`](#orama-chain-status) — Show the chain's height, network and sync state
  - [`orama chain validator`](#orama-chain-validator) — List the validator set, or show one validator
- [`orama cluster`](#orama-cluster) — Choose who may create namespaces on this cluster
  - [`orama cluster creators`](#orama-cluster-creators) — Wallets that may create namespaces when creation is allowlist
    - [`orama cluster creators add`](#orama-cluster-creators-add) — Let a wallet create namespaces when creation is allowlist
    - [`orama cluster creators list`](#orama-cluster-creators-list) — List wallets allowed to create namespaces
    - [`orama cluster creators remove`](#orama-cluster-creators-remove) — Take a wallet off the namespace-creator list
  - [`orama cluster namespace`](#orama-cluster-namespace) — Operator actions on a namespace
    - [`orama cluster namespace remove`](#orama-cluster-namespace-remove) — Remove a namespace whose owner can no longer delete it
  - [`orama cluster register-onchain`](#orama-cluster-register-onchain) — Register this cluster's public name on the Orama chain
  - [`orama cluster retire-onchain`](#orama-cluster-retire-onchain) — Retire this cluster's public row on the Orama chain
  - [`orama cluster settings`](#orama-cluster-settings) — Show or change namespace-creation settings
    - [`orama cluster settings set`](#orama-cluster-settings-set) — Change namespace creation or the per-wallet cap
    - [`orama cluster settings show`](#orama-cluster-settings-show) — Show who may create namespaces, and the per-wallet cap
- [`orama db`](#orama-db) — Manage SQLite databases
  - [`orama db backup`](#orama-db-backup) — Backup database to IPFS
  - [`orama db backups`](#orama-db-backups) — List backups for a database
  - [`orama db create`](#orama-db-create) — Create a new SQLite database
  - [`orama db delete`](#orama-db-delete) — Delete a database and its file
  - [`orama db list`](#orama-db-list) — List all databases
  - [`orama db query`](#orama-db-query) — Execute a SQL query
- [`orama deploy`](#orama-deploy) — Deploy applications to the Orama network
  - [`orama deploy go`](#orama-deploy-go) — Deploy a Go backend
  - [`orama deploy nextjs`](#orama-deploy-nextjs) — Deploy a Next.js application
  - [`orama deploy nodejs`](#orama-deploy-nodejs) — Deploy a Node.js backend
  - [`orama deploy static`](#orama-deploy-static) — Deploy a static site (React, Vue, etc.)
- [`orama domain`](#orama-domain) — Attach custom domains to your apps
  - [`orama domain add`](#orama-domain-add) — Attach a domain to an app
  - [`orama domain list`](#orama-domain-list) — List your custom domains
  - [`orama domain remove`](#orama-domain-remove) — Detach a domain
  - [`orama domain verify`](#orama-domain-verify) — Check the TXT record and activate the domain
- [`orama env`](#orama-env) — Manage environments
  - [`orama env add`](#orama-env-add) — Add a custom environment
  - [`orama env current`](#orama-env-current) — Show current active environment
  - [`orama env list`](#orama-env-list) — List all available environments
  - [`orama env remove`](#orama-env-remove) — Remove an environment
  - [`orama env use`](#orama-env-use) — Switch to a different environment
- [`orama function`](#orama-function) — Manage serverless functions
  - [`orama function build`](#orama-function-build) — Build a function to WASM using TinyGo
  - [`orama function delete`](#orama-function-delete) — Delete a deployed function
  - [`orama function deploy`](#orama-function-deploy) — Deploy a function to the Orama Network
  - [`orama function disable`](#orama-function-disable) — Disable a function without deleting it
  - [`orama function enable`](#orama-function-enable) — Re-enable a previously disabled function
  - [`orama function get`](#orama-function-get) — Get details of a deployed function
  - [`orama function init`](#orama-function-init) — Create a new serverless function project
  - [`orama function invoke`](#orama-function-invoke) — Invoke a deployed function
  - [`orama function list`](#orama-function-list) — List deployed functions
  - [`orama function logs`](#orama-function-logs) — Get invocation history for a function
  - [`orama function secrets`](#orama-function-secrets) — Manage function secrets
    - [`orama function secrets delete`](#orama-function-secrets-delete) — Delete a secret
    - [`orama function secrets list`](#orama-function-secrets-list) — List secret names
    - [`orama function secrets set`](#orama-function-secrets-set) — Set a secret
  - [`orama function triggers`](#orama-function-triggers) — Manage function PubSub and cron triggers
    - [`orama function triggers add`](#orama-function-triggers-add) — Add a PubSub or Cron trigger
    - [`orama function triggers delete`](#orama-function-triggers-delete) — Delete a trigger
    - [`orama function triggers list`](#orama-function-triggers-list) — List triggers for a function
  - [`orama function versions`](#orama-function-versions) — List all versions of a function
- [`orama global`](#orama-global) — Install and operate a global node, and build its chain messages
  - [`orama global bind`](#orama-global-bind) — Sign orama-global-bind-v1 for one service key
  - [`orama global bond`](#orama-global-bond) — Bond norama to one role on a global node
  - [`orama global capacity`](#orama-global-capacity) — Declare how many bytes a storage node will hold
  - [`orama global install`](#orama-global-install) — Install the global services on this node (run as root)
  - [`orama global register`](#orama-global-register) — Register a global node from signed service-key bindings
  - [`orama global restart`](#orama-global-restart) — Restart the installed global services in order (run as root)
  - [`orama global retire`](#orama-global-retire) — Retire a global node
  - [`orama global stage-oramad`](#orama-global-stage-oramad) — Place a TUF-verified oramad in the cosmovisor layout
  - [`orama global start`](#orama-global-start) — Start the installed global services, chain first (run as root)
  - [`orama global status`](#orama-global-status) — Show the state of each installed global service (run as root)
  - [`orama global stop`](#orama-global-stop) — Stop the installed global services, chain last (run as root)
  - [`orama global unbond`](#orama-global-unbond) — Start unbonding norama from one role
  - [`orama global validator`](#orama-global-validator) — Back up, move and manage this node's validator key
    - [`orama global validator check-sign-floor`](#orama-global-validator-check-sign-floor) — Fail when the chain must not start: key moved away or state behind its floor
    - [`orama global validator edit`](#orama-global-validator-edit) — Build or send MsgEditValidator (description, commission)
    - [`orama global validator export-key`](#orama-global-validator-export-key) — Write priv_validator_key.json sealed to the operator's public key (run as root)
    - [`orama global validator migrate`](#orama-global-validator-migrate) — Move the validator key to another host without a double sign
      - [`orama global validator migrate cancel`](#orama-global-validator-migrate-cancel) — Remove this host's prepared migration key (run on the new host)
      - [`orama global validator migrate export`](#orama-global-validator-migrate-export) — Stop the chain and seal the key and its sign state (run on the old host)
      - [`orama global validator migrate import`](#orama-global-validator-migrate-import) — Install a migrated key and record its sign floor (run on the new host)
      - [`orama global validator migrate prepare`](#orama-global-validator-migrate-prepare) — Print this host's migration key (run on the new host)
    - [`orama global validator reseal`](#orama-global-validator-reseal) — Turn a key backup into a migration bundle for a new host
    - [`orama global validator unjail`](#orama-global-validator-unjail) — Build or send MsgUnjail for the operator's validator
- [`orama inspect`](#orama-inspect) — Inspect cluster health via SSH
- [`orama invite`](#orama-invite) — Mint an invite for a new node
- [`orama members`](#orama-members) — Manage who may work in a namespace
  - [`orama members add`](#orama-members-add) — Give a wallet a role in this namespace
  - [`orama members list`](#orama-members-list) — List who holds a grant in this namespace
  - [`orama members remove`](#orama-members-remove) — Take a wallet's grant away
  - [`orama members transfer`](#orama-members-transfer) — Hand this namespace to another wallet
- [`orama monitor`](#orama-monitor) — Monitor cluster health from your local machine
  - [`orama monitor alerts`](#orama-monitor-alerts) — Alerts, most severe first, with what to do (one-shot)
  - [`orama monitor chain`](#orama-monitor-chain) — Orama L1 height, sync and validators (one-shot)
  - [`orama monitor cluster`](#orama-monitor-cluster) — Verdict, components and a row per node (one-shot)
  - [`orama monitor dns`](#orama-monitor-dns) — DNS and TLS health of the nameservers (one-shot)
  - [`orama monitor live`](#orama-monitor-live) — Interactive live view (the default)
  - [`orama monitor mesh`](#orama-monitor-mesh) — WireGuard mesh connectivity (one-shot)
  - [`orama monitor namespaces`](#orama-monitor-namespaces) — Namespace health across nodes (one-shot)
  - [`orama monitor node`](#orama-monitor-node) — Per-node health details (one-shot)
  - [`orama monitor report`](#orama-monitor-report) — Full cluster report as JSON (one-shot)
  - [`orama monitor service`](#orama-monitor-service) — Service status across the cluster (one-shot)
  - [`orama monitor traffic`](#orama-monitor-traffic) — Gateway requests, errors and latency (one-shot)
- [`orama namespace`](#orama-namespace) — Manage namespaces
  - [`orama namespace backup`](#orama-namespace-backup) — Take a backup of the namespace, sealed to your X25519 public key
  - [`orama namespace backup-open`](#orama-namespace-backup-open) — Decrypt a backup file with an X25519 private key
  - [`orama namespace backup-seal`](#orama-namespace-backup-seal) — Encrypt a backup file to an X25519 public key
  - [`orama namespace create`](#orama-namespace-create) — Create a namespace and start its cluster
  - [`orama namespace delete`](#orama-namespace-delete) — Delete the current namespace and all its resources
  - [`orama namespace disable`](#orama-namespace-disable) — Disable a feature for a namespace
  - [`orama namespace enable`](#orama-namespace-enable) — Enable a feature for a namespace
  - [`orama namespace keys`](#orama-namespace-keys) — Manage scoped API keys (bugboard #148)
    - [`orama namespace keys create`](#orama-namespace-keys-create) — Mint a new scoped API key
    - [`orama namespace keys list`](#orama-namespace-keys-list) — List scoped API keys
    - [`orama namespace keys revoke`](#orama-namespace-keys-revoke) — Revoke a single API key by id
    - [`orama namespace keys revoke-legacy`](#orama-namespace-keys-revoke-legacy) — Revoke ALL legacy (unscoped) keys — the cutover step
    - [`orama namespace keys rotate`](#orama-namespace-keys-rotate) — Mint a successor to a key and keep the old one working for an overlap
  - [`orama namespace list`](#orama-namespace-list) — List namespaces owned by the current wallet
  - [`orama namespace repair`](#orama-namespace-repair) — Repair an under-provisioned namespace cluster
  - [`orama namespace restore`](#orama-namespace-restore) — Restore a namespace backup onto the namespace gateway (DESTRUCTIVE)
  - [`orama namespace restore-key`](#orama-namespace-restore-key) — Print the namespace gateway's restore public key
  - [`orama namespace rqlite`](#orama-namespace-rqlite) — Manage the namespace's internal RQLite database
    - [`orama namespace rqlite export`](#orama-namespace-rqlite-export) — Export the namespace's RQLite database to a local SQLite file
    - [`orama namespace rqlite import`](#orama-namespace-rqlite-import) — Import a SQLite dump into the namespace's RQLite (DESTRUCTIVE)
  - [`orama namespace session-policy`](#orama-namespace-session-policy) — Show or set who may sign in to a namespace and what its sessions bind
  - [`orama namespace webrtc-status`](#orama-namespace-webrtc-status) — Show WebRTC service status for a namespace
- [`orama node`](#orama-node) — Node operator commands
  - [`orama node autoupdate`](#orama-node-autoupdate) — Decide whether a newer release should be installed
  - [`orama node clean`](#orama-node-clean) — Deprecated: use 'orama node wipe' or 'orama node remove'
  - [`orama node dns`](#orama-node-dns) — Cluster DNS: what the outside world needs to reach its nameservers
    - [`orama node dns delegation`](#orama-node-dns-delegation) — Print the NS and glue records to create at the parent zone
  - [`orama node doctor`](#orama-node-doctor) — Diagnose common node issues
  - [`orama node enroll`](#orama-node-enroll) — Enroll an OramaOS node into the cluster
  - [`orama node install`](#orama-node-install) — Install production node (requires sudo)
  - [`orama node invite`](#orama-node-invite) — Manage invite tokens for joining the cluster
  - [`orama node list`](#orama-node-list) — List your nodes across environments
  - [`orama node logs`](#orama-node-logs) — View production service logs
  - [`orama node migrate-conf`](#orama-node-migrate-conf) — Register nodes.conf nodes with your wallet
  - [`orama node migrate-raft-id`](#orama-node-migrate-raft-id) — Move nodes to stable, peer-id-based raft identities (one-time)
  - [`orama node push`](#orama-node-push) — Push the binary archive to your nodes
  - [`orama node recover-raft`](#orama-node-recover-raft) — Recover RQLite cluster from split-brain
  - [`orama node remove`](#orama-node-remove) — Remove one node from the cluster, then erase it
  - [`orama node report`](#orama-node-report) — Output comprehensive node health data as JSON
  - [`orama node restart`](#orama-node-restart) — Restart all production services (requires sudo)
  - [`orama node rollout`](#orama-node-rollout) — Build, push, and rolling upgrade every node in an environment
  - [`orama node schema`](#orama-node-schema) — Inspect and apply gateway schema migrations against the local RQLite
    - [`orama node schema apply`](#orama-node-schema-apply) — Apply pending migrations to the local RQLite
    - [`orama node schema status`](#orama-node-schema-status) — Show required vs applied schema version + pending migrations
  - [`orama node setup`](#orama-node-setup) — Set up a fresh VPS as an Orama node
  - [`orama node stage-archive`](#orama-node-stage-archive) — Verify a pushed build archive and put it in place (run by 'orama push')
  - [`orama node start`](#orama-node-start) — Start all production services (requires sudo)
  - [`orama node status`](#orama-node-status) — Show the service status of the node on this machine
  - [`orama node stop`](#orama-node-stop) — Stop all production services (requires sudo)
  - [`orama node uninstall`](#orama-node-uninstall) — Remove production services (requires sudo)
  - [`orama node unlock`](#orama-node-unlock) — Unlock an OramaOS genesis node
  - [`orama node upgrade`](#orama-node-upgrade) — Upgrade existing installation (requires sudo)
  - [`orama node wipe`](#orama-node-wipe) — Erase Orama from remote nodes (target-side only)
- [`orama nodes`](#orama-nodes) — List your nodes across environments
- [`orama operator`](#orama-operator) — Operate the cluster
  - [`orama operator add`](#orama-operator-add) — Let another wallet operate this cluster
  - [`orama operator list`](#orama-operator-list) — List the wallets that operate this cluster
  - [`orama operator remove`](#orama-operator-remove) — Take a wallet off this cluster's operator list
  - [`orama operator rotate-secrets`](#orama-operator-rotate-secrets) — Re-encrypt stored secrets, optionally under a new encryption root
  - [`orama operator rotate-signing-key`](#orama-operator-rotate-signing-key) — Replace the key this gateway signs tokens with
- [`orama push`](#orama-push) — Push the binary archive to your nodes
- [`orama rollout`](#orama-rollout) — Build, push, and rolling upgrade every node in an environment
- [`orama sandbox`](#orama-sandbox) — Manage ephemeral Hetzner Cloud clusters for testing
  - [`orama sandbox create`](#orama-sandbox-create) — Create a new 5-node sandbox cluster (~5 min)
  - [`orama sandbox destroy`](#orama-sandbox-destroy) — Destroy a sandbox cluster and release resources
  - [`orama sandbox list`](#orama-sandbox-list) — List active sandbox clusters
  - [`orama sandbox reset`](#orama-sandbox-reset) — Delete all sandbox infrastructure and config to start fresh
  - [`orama sandbox rollout`](#orama-sandbox-rollout) — Build + push + rolling upgrade to sandbox cluster
  - [`orama sandbox setup`](#orama-sandbox-setup) — Interactive setup: Hetzner API key, domain, floating IPs, SSH key
  - [`orama sandbox ssh`](#orama-sandbox-ssh) — SSH into a sandbox node (1-5)
  - [`orama sandbox status`](#orama-sandbox-status) — Show cluster health report
- [`orama ssh`](#orama-ssh) — SSH into a node
- [`orama status`](#orama-status) — Show health status of your nodes
- [`orama storage`](#orama-storage) — Storage deals on the Orama chain
  - [`orama storage accept`](#orama-storage-accept) — Accept an assigned storage slot
  - [`orama storage create`](#orama-storage-create) — Open a private or public-pin storage deal
  - [`orama storage decline`](#orama-storage-decline) — Decline an assigned storage slot
  - [`orama storage extend`](#orama-storage-extend) — Add epochs to a storage deal
  - [`orama storage get`](#orama-storage-get) — Fetch and open a private file from its providers
  - [`orama storage grant`](#orama-storage-grant) — Grant a cluster a capped deal allowance
  - [`orama storage open`](#orama-storage-open) — Open one sealed storage slot
  - [`orama storage prove`](#orama-storage-prove) — Submit storage challenge proofs
  - [`orama storage put`](#orama-storage-put) — Upload sealed slots to the providers a deal assigned
  - [`orama storage revoke`](#orama-storage-revoke) — Revoke a deal allowance
  - [`orama storage rewrap`](#orama-storage-rewrap) — Rebuild one storage slot from another slot's ciphertext
  - [`orama storage seal`](#orama-storage-seal) — Seal a file into one ciphertext per storage slot
- [`orama version`](#orama-version) — Show version information
- [`orama vpn`](#orama-vpn) — Route traffic through an Orama Tor network
  - [`orama vpn check`](#orama-vpn-check) — Join an Orama Tor network and reach a validator onion service through it
  - [`orama vpn up`](#orama-vpn-up) — Run a SOCKS5 proxy into an Orama Tor network

---

### orama app

Manage deployed applications

```
orama app
```

Aliases: `apps`

List, get, delete, rollback, and view logs/stats for your deployed applications.

Subcommands: `delete`, `env`, `get`, `grants`, `list`, `logs`, `rollback`, `stats`

### orama app delete

Delete a deployment

```
orama app delete <name>
```

### orama app env

Manage an app's environment variables

```
orama app env
```

Read and change the environment variables a deployed app runs with.

Setting or removing a variable restarts the app, on every node that runs it,
so it picks up the change. The command succeeds only when every node applied
it. If a node could not be reached it is named in the error, still runs the old
environment, and running the same command again retries it.

Values are never printed back. They are where secrets live, so 'list' shows
names only.

Subcommands: `list`, `set`, `unset`

### orama app env list

List an app's environment variable names

```
orama app env list <app>
```

### orama app env set

Set environment variables and restart the app

```
orama app env set <app> [flags]
```

Set one or more variables and restart the app.

Values given with --env never appear in shell history if you read them from a
file instead: --env-file takes a .env and sends every variable in it.

| Flag | Default | Description |
|------|---------|-------------|
| `--env-file` | — | Read variables from a .env file |
| `--env` | — | Variable as KEY=VALUE (repeatable) |

### orama app env unset

Remove environment variables and restart the app

```
orama app env unset <app> <KEY>...
```

### orama app get

Get deployment details

```
orama app get <name>
```

### orama app grants

Say what a deployed app may do, as itself

```
orama app grants
```

Read and change what a deployment is allowed to reach.

Your app is handed a short-lived token of its own at start, in the file named by
$ORAMA_TOKEN_FILE, and renews it with the gateway before it expires. It reaches
nothing until you grant it something — which is the point: an app that ships
with no credential cannot leak one.

A deployment cannot be granted the control plane. If something needs to deploy
or mint keys, that is a person or a CI key, not an app.

Subcommands: `list`, `set`

### orama app grants list

Show what deployments in this namespace may do

```
orama app grants list [app]
```

### orama app grants set

Grant a deployment a role

```
orama app grants set <app> <role> [flags]
```

Give a deployment a role in its own namespace.

  runtime  the data plane: invoke, storage, push, webrtc, proxy, pubsub, cache
  reader   nothing beyond the routes that ask for no grant

The change reaches a running app on its next token renewal, or immediately if
you redeploy.

| Flag | Default | Description |
|------|---------|-------------|
| `--resource` | — | Narrow the role to a resource, e.g. pubsub:topic=orders.* |

### orama app list

List all deployments

```
orama app list
```

### orama app logs

Stream deployment logs

```
orama app logs <name> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-f`, `--follow` | `false` | Follow log output |
| `-n`, `--lines` | `100` | Number of lines to show |

### orama app rollback

Rollback a deployment to a previous version

```
orama app rollback <name> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--version` | `0` | Version to rollback to (required) |

### orama app stats

Show resource usage for a deployment

```
orama app stats <name>
```

### orama audit

Read this namespace's audit trail

```
orama audit [flags]
```

Print what has happened in a namespace: sign-ins, keys minted and revoked,
grants given and taken away, deployments, functions, secrets and namespace changes.

Events are shown oldest first. --follow keeps the command running and prints new
ones as they are recorded.

Actions: auth.challenge, auth.verify, auth.refresh, auth.refresh.replay, auth.logout, key.issue, key.revoke, key.rotate, key.revoke_all, namespace.create, namespace.delete, namespace.operator_remove, secret.set, secret.delete, function.deploy, function.delete, deployment.deploy, deployment.delete, operator.action, auth.legacy_credential, grant.add, grant.revoke, namespace.transfer, namespace.backup, namespace.restore, auth.device.start, auth.device.approve, auth.device.deny, auth.device.claim, auth.device.revoke, namespace.session_policy, namespace.sign_in_policy, node.register, node.key.enrol

| Flag | Default | Description |
|------|---------|-------------|
| `--action` | — | Show only this action |
| `--limit` | `0` | How many events to fetch at once (default 50, max 200) |
| `--namespace` | — | Namespace name |
| `--principal` | — | Show only what this wallet or key did |
| `--since` | — | Show only what happened after this time (RFC3339, or the created_at of a row) |
| `-f`, `--follow` | `false` | Keep running and print new events as they are recorded |

### orama auth

Authentication management

```
orama auth
```

Manage authentication with the Orama network.

Signing in is a wallet signature over a gateway challenge. On a machine with
RootWallet running it is signed here; on one without — a server reached over
SSH, a container, CI — 'orama auth login' prints a code and 'orama auth approve'
on a machine that does have a wallet approves it.

What is stored is a session, not a key: an access token lasting 15 minutes,
renewed transparently from a refresh token.

Subcommands: `approve`, `list`, `login`, `logout`, `sessions`, `status`, `switch`, `whoami`

### orama auth approve

Approve a login waiting on another machine

```
orama auth approve <code> [flags]
```

Approve the code 'orama auth login' printed on a machine with no wallet on it.

It costs the same wallet signature signing in does, which is what makes the code
on its own worthless. --deny refuses instead, so the waiting machine stops
rather than polling until the code expires.

| Flag | Default | Description |
|------|---------|-------------|
| `--deny` | `false` | Refuse the login instead of approving it |
| `--namespace` | — | Namespace to sign in to (defaults to the one this machine is signed in to) |

### orama auth list

List all stored credentials

```
orama auth list
```

### orama auth login

Sign in, here or from another machine

```
orama auth login [flags]
```

Sign in, here or from another machine.

Run at a terminal with no --namespace and a credential already saved, it first
offers the saved ones to switch to. With --namespace, or without a terminal
(a script, CI), it signs in straight away.

--device-key enrolls that Ed25519 key with this sign-in. The file is a private
JWK and stays on this machine; the gateway receives the public half and the
device's signature over the same message the wallet signs.

| Flag | Default | Description |
|------|---------|-------------|
| `--device-key` | — | Ed25519 private JWK file to enroll with this sign-in |
| `--namespace` | — | Namespace name |

### orama auth logout

End this session on the gateway and clear it here

```
orama auth logout [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--all` | `false` | End every session for this wallet, not only this machine's |

### orama auth sessions

Which machines are signed in as this wallet

```
orama auth sessions
```

Subcommands: `revoke`

### orama auth sessions revoke

End one session, or every one

```
orama auth sessions revoke [id] [flags]
```

End a session listed by 'orama auth sessions'.

Ending a session stops it minting new access tokens, and refuses the access
tokens it already minted — and closes the sockets they hold open — within ten
seconds. A session issued before sessions carried an id cannot be named that
way: its access tokens work until they expire, at most 15 minutes, and the
command says so.

| Flag | Default | Description |
|------|---------|-------------|
| `--all` | `false` | End every session for this wallet |

### orama auth status

Show what is stored on this machine, without asking the gateway

```
orama auth status
```

### orama auth switch

Switch between stored credentials

```
orama auth switch
```

### orama auth whoami

Ask the gateway who this credential is and what it may do

```
orama auth whoami
```

### orama build

Build pre-compiled binary archive for deployment

```
orama build [flags]
```

Cross-compile all Orama binaries and dependencies for Linux,
then package them into a deployment archive. The archive includes:
  - Orama binaries (CLI, node, gateway, identity, SFU, TURN)
  - Olric, IPFS Kubo, IPFS Cluster, RQLite, CoreDNS, Caddy
  - Systemd namespace templates
  - manifest.json with checksums of every file, and manifest.sig

The manifest is signed with your RootWallet (the agent's active account, through
its wallet:sign capability). Nodes install only archives signed by an address in
their trust anchor, /etc/orama/archive-signers, so signing is the default;
--unsigned makes an archive for local inspection that no node will install.

--signers rotates the trusted signers: nodes that install this build replace
their list with the given addresses. The build must be signed by a signer the
nodes trust now, and the list must include that signer; retiring a key takes
two builds (the old key adds the new one, the new key then drops the old).

The resulting archive can be pushed to nodes with 'orama node push'.

Examples:
  orama build
  orama build --signers 0xYourWallet,0xNewOperator
  orama build --unsigned --output /tmp/inspect.tar.gz

| Flag | Default | Description |
|------|---------|-------------|
| `--arch` | `amd64` | Target architecture (amd64, arm64) |
| `--output` | — | Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz) |
| `--signers` | — | Rotate the trusted archive signers: nodes that install this build trust only these addresses (comma-separated) |
| `--unsigned` | `false` | Do not sign the manifest (a local-only archive: nodes refuse it) |
| `--verbose` | `false` | Verbose output |

### orama chain

Read the Orama chain: status, balances, earnings, nodes, deals, validators; fund test accounts

```
orama chain [flags]
```

Read the Orama chain. Every command here only reads, except 'faucet', which
funds an account on a test network.

Three read paths exist, and each command uses one:

  --gateway  the gateway's read-only /v1/chain/ proxy (default: the active
             environment's gateway). Status, blocks, transactions, the
             validator set, supply, the indexer and the Orama module queries
             (x/nodes, x/storage, x/fees, ...) under /v1/chain/query/.
  --node     a node's Cosmos REST API, for example http://127.0.0.1:31003.
             Accounts, bank balances, staking validators.
  --rpc      a node's CometBFT RPC, for example http://127.0.0.1:31001. The
             Orama modules answer gRPC only and abci_query is their one node
             HTTP route; with --rpc set, earnings, node, deal and query read
             it directly instead of through the gateway.

Transactions are built and signed by 'orama global', 'orama storage' and
'orama cluster'; --onion on those submits through Tor. 'faucet' is the one
transaction here, and it signs on a node over SSH (see 'orama chain faucet').

| Flag | Default | Description |
|------|---------|-------------|
| `--gateway` | — | Gateway URL for /v1/chain/ reads (default: the active environment's gateway) |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003 |
| `--rpc` | — | CometBFT RPC, for example http://127.0.0.1:31001 |

Subcommands: `balance`, `deal`, `earnings`, `faucet`, `node`, `query`, `status`, `validator`

### orama chain balance

Show an account's bank balances

```
orama chain balance <address>
```

Show an account's bank balances from --node's REST API. This is the account's
spendable bank balance. Earnings live in a separate account: see
'orama chain earnings'.

### orama chain deal

Show a storage deal (x/storage)

```
orama chain deal <deal-id>
```

Show a storage deal from x/storage through the gateway (or --rpc).

### orama chain earnings

Show an account's earnings balance (x/fees)

```
orama chain earnings <address>
```

Show the earnings balance x/fees holds for an account, through the gateway (or --rpc). Earnings
are what the account is paid for running nodes and services; they are not in
the bank balance.

### orama chain faucet

Fund an account on a test network (stagenet, devnet)

```
orama chain faucet <recipient> [flags]
```

Send test ORAMA to an account from the chain's faucet (MsgFaucet).

The faucet exists only on a test network: a chain whose id contains -stagenet-,
-devnet- or -localnet- and whose genesis turned it on (faucet_enabled). This
command reads the chain id from the node first and refuses any other chain
before it signs anything. The chain refuses a drip over its maximum and a second
drip to the same recipient inside its cooldown (24 hours by default).

The transaction is signed ON a node, with the node's operator key (the
"validator" key of oramad's test keyring), over SSH with the environment's
wallet-provided key. The key never leaves the node and nothing secret is
printed. The operator pays the fee from its earnings. The recipient need not
exist yet. --amount is in norama (1 ORAMA = 1000000000 norama).

Prints the transaction hash, the amount, and the recipient's bank balance once
the transaction is in a block.

  orama chain faucet orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53 --env stagenet
  orama chain faucet <address> --env stagenet --amount 5000000000 --node 57.129.166.16

--node is the SSH host of the node that signs (here it is not the REST URL the
other 'orama chain' commands take); without it the environment's first node
signs.

| Flag | Default | Description |
|------|---------|-------------|
| `--amount` | `100000000000` | Amount of norama to send (1 ORAMA = 1000000000 norama) |
| `--env` | — | Environment whose node signs (default: the active environment) |

### orama chain node

Show a registered node (x/nodes)

```
orama chain node <node-id>
```

Show a node's record from x/nodes through the gateway (or --rpc): operator, roles, bonds, endpoints, capacity and status.

### orama chain query

Run any Orama module query through the gateway or --rpc

```
orama chain query <Service/Method> [request-json] [flags]
```

Run a gRPC query of an Orama module and print the response as JSON. By default it
goes through the gateway's GET /v1/chain/query/<Service>/<Method>; with --rpc it
goes to that node's CometBFT abci_query. The request is JSON with the proto
field names. For example:

  orama chain query orama.nodes.v1.Query/Node '{"node_id":"node-1"}'
  orama chain query orama.nodes.v1.Query/Node '{"node_id":"node-1"}' --rpc http://127.0.0.1:31001

'orama chain query --list' prints every query the CLI knows.

| Flag | Default | Description |
|------|---------|-------------|
| `--list` | `false` | List every query the CLI knows and exit |

### orama chain status

Show the chain's height, network and sync state

```
orama chain status
```

Show CometBFT's status: the network id, the latest block and whether the node is
catching up. Reads the gateway's /v1/chain/status, or --rpc's /status.

### orama chain validator

List the validator set, or show one validator

```
orama chain validator [oramavaloper-address]
```

Without an argument, list the CometBFT validator set from the gateway's
/v1/chain/validators (or --rpc's /validators). With an oramavaloper address,
show that validator's staking record from --node's REST API.

### orama cluster

Choose who may create namespaces on this cluster

```
orama cluster
```

Who may create a namespace on this cluster, and how many one wallet may own.

A new cluster allows only its operators. A cluster that already had a
namespace besides the seeded default, a node, or an operator when this was
upgraded stays open — any signed-in wallet — until an operator changes it.
The per-wallet cap stays 10 until an operator raises or lowers it.

Changing a setting or the creator list needs the operator grant and a wallet
on the operator list, and is written to the audit trail.

Subcommands: `creators`, `namespace`, `register-onchain`, `retire-onchain`, `settings`

### orama cluster creators

Wallets that may create namespaces when creation is allowlist

```
orama cluster creators
```

The allowlist consulted when namespace creation is allowlist.

An operator is not on it unless added. An empty list lets nobody create a
namespace, and removing the last wallet does not lock operators out.

Subcommands: `add`, `list`, `remove`

### orama cluster creators add

Let a wallet create namespaces when creation is allowlist

```
orama cluster creators add <wallet>
```

### orama cluster creators list

List wallets allowed to create namespaces

```
orama cluster creators list
```

### orama cluster creators remove

Take a wallet off the namespace-creator list

```
orama cluster creators remove <wallet>
```

### orama cluster namespace

Operator actions on a namespace

```
orama cluster namespace
```

Subcommands: `remove`

### orama cluster namespace remove

Remove a namespace whose owner can no longer delete it

```
orama cluster namespace remove <namespace> [flags]
```

Remove a namespace and everything in it: its cluster on every node, its
deployments, its stored content (unless another namespace holds the same), its
keys and its grants.

The owner deletes a namespace with 'orama namespace delete'. This is for a
namespace whose owner cannot: the owner's wallet is lost, or it belonged to a
test run's throwaway wallet. Such a namespace keeps its port blocks and
processes on three nodes until an operator removes it.

It needs the operator grant and a wallet on the operator list. --reason is
required and is written to the audit trail with your wallet
(namespace.operator_remove). You are asked to type the namespace name unless
--force is given.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Do not ask to type the namespace name |
| `--reason` | — | Why the namespace is removed; recorded in the audit trail [required] |

### orama cluster register-onchain

Register this cluster's public name on the Orama chain

```
orama cluster register-onchain [flags]
```

Register an optional public row for this cluster on the Orama chain.

The row is the operator, the cluster id, the base domain, the public
endpoints, and an optional metadata URI. It does not include node addresses,
tenants, or any cluster secret, and registering it does not join a node.

--node is that chain's REST API. The command reads the account there, builds
a SIGN_MODE_DIRECT transaction, asks the RootWallet agent to sign that one
transaction, and broadcasts it. Without --node it prints the sign document
and does not submit anything. --onion sends the same transaction to a validator
onion service through a Tor SOCKS proxy on this machine instead, on a fresh
circuit, and never falls back to the clearnet: when Tor or the service is
unreachable the command fails and the transaction is not sent.

The fee is an explicit amount of norama. There is no default.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--base-domain` | — | Public base domain [required] |
| `--chain-id` | — | Chain id [required] |
| `--endpoint` | — | Public endpoint (repeatable) [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Cluster id [required] |
| `--metadata-uri` | — | HTTPS metadata URI |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex; required when the account has not signed before |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama cluster retire-onchain

Retire this cluster's public row on the Orama chain

```
orama cluster retire-onchain [flags]
```

Retire the optional public cluster row. This does not change any node and
does not delete the cluster's namespaces. Without --node the command prints
the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Cluster id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama cluster settings

Show or change namespace-creation settings

```
orama cluster settings
```

Subcommands: `set`, `show`

### orama cluster settings set

Change namespace creation or the per-wallet cap

```
orama cluster settings set <setting> <value>
```

namespace-creation is operators, allowlist or open.

  operators   only wallets on the operator list
  allowlist   only wallets added with orama cluster creators add
  open        any signed-in wallet

max-namespaces-per-wallet is an integer from 1 to 10000. The default is 10.

### orama cluster settings show

Show who may create namespaces, and the per-wallet cap

```
orama cluster settings show
```

### orama db

Manage SQLite databases

```
orama db
```

Create and manage per-namespace SQLite databases.

Subcommands: `backup`, `backups`, `create`, `delete`, `list`, `query`

### orama db backup

Backup database to IPFS

```
orama db backup <database_name>
```

### orama db backups

List backups for a database

```
orama db backups <database_name>
```

### orama db create

Create a new SQLite database

```
orama db create <database_name>
```

### orama db delete

Delete a database and its file

```
orama db delete <database_name> [flags]
```

Permanently delete a database.

The file and its write-ahead log are removed from the node that holds them.
There is no undo: restore from a backup with 'orama db backups' if you need the
data again.

| Flag | Default | Description |
|------|---------|-------------|
| `--yes` | `false` | Skip the confirmation prompt |

### orama db list

List all databases

```
orama db list
```

### orama db query

Execute a SQL query

```
orama db query <database_name> <sql>
```

### orama deploy

Deploy applications to the Orama network

```
orama deploy
```

Deploy static sites, Next.js apps, Go backends, and Node.js backends.
If a deployment with the same name exists, it will be updated.

Subcommands: `go`, `nextjs`, `nodejs`, `static`

### orama deploy go

Deploy a Go backend

```
orama deploy go <source_path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--env-file` | — | Read environment variables from a .env file |
| `--env` | — | Environment variable as KEY=VALUE (repeatable) |
| `--health-check` | — | Path the platform polls to decide the app is up (default /health) |
| `--name` | — | Deployment name (required) |
| `--subdomain` | — | Custom subdomain |
| `--update` | `false` | Update existing deployment |

### orama deploy nextjs

Deploy a Next.js application

```
orama deploy nextjs <source_path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--env-file` | — | Read environment variables from a .env file |
| `--env` | — | Environment variable as KEY=VALUE (repeatable) |
| `--health-check` | — | Path the platform polls to decide the app is up (default /health) |
| `--name` | — | Deployment name (required) |
| `--ssr` | `false` | Deploy with SSR (server-side rendering) |
| `--subdomain` | — | Custom subdomain |
| `--update` | `false` | Update existing deployment |

### orama deploy nodejs

Deploy a Node.js backend

```
orama deploy nodejs <source_path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--env-file` | — | Read environment variables from a .env file |
| `--env` | — | Environment variable as KEY=VALUE (repeatable) |
| `--health-check` | — | Path the platform polls to decide the app is up (default /health) |
| `--name` | — | Deployment name (required) |
| `--subdomain` | — | Custom subdomain |
| `--update` | `false` | Update existing deployment |

### orama deploy static

Deploy a static site (React, Vue, etc.)

```
orama deploy static <source_path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--env-file` | — | Read environment variables from a .env file |
| `--env` | — | Environment variable as KEY=VALUE (repeatable) |
| `--name` | — | Deployment name (required) |
| `--subdomain` | — | Custom subdomain |
| `--update` | `false` | Update existing deployment |

### orama domain

Attach custom domains to your apps

```
orama domain
```

Add, verify, list and remove custom domains.

A domain is proved yours with a TXT record before it serves traffic. 'add'
prints the record to create, 'verify' checks it.

Subcommands: `add`, `list`, `remove`, `verify`

### orama domain add

Attach a domain to an app

```
orama domain add <domain> [flags]
```

Register a domain against a deployment and print the TXT record that proves
you own it.

The domain does not serve traffic until 'orama domain verify' succeeds.

| Flag | Default | Description |
|------|---------|-------------|
| `--app` | — | Deployment to attach the domain to [required] |
| `--verify` | `false` | Wait for the TXT record and verify in one step |
| `--wait` | `5m0s` | How long --verify waits for the record to propagate |

### orama domain list

List your custom domains

```
orama domain list [flags]
```

List every custom domain in the namespace, or only one app's with --app.

| Flag | Default | Description |
|------|---------|-------------|
| `--app` | — | Only this deployment's domains |

### orama domain remove

Detach a domain

```
orama domain remove <domain>
```

Remove a custom domain and the DNS record that pointed it at your app.

### orama domain verify

Check the TXT record and activate the domain

```
orama domain verify <domain> [flags]
```

Ask the gateway to resolve the domain's TXT record and, if it matches, start
serving the domain.

With --wait the check is repeated until the record appears, which is what a
freshly created DNS record needs.

| Flag | Default | Description |
|------|---------|-------------|
| `--wait` | `0s` | Keep checking until the record appears, up to this long |

### orama env

Manage environments

```
orama env
```

List, switch, add, and remove Orama network environments.
Available default environments: production, devnet, testnet.

Subcommands: `add`, `current`, `list`, `remove`, `use`

### orama env add

Add a custom environment

```
orama env add <name> <gateway_url> [description] [flags]
```

Add a custom environment, or update one already configured.

The name may not be blank, and the gateway URL must be https:// with a host
(http:// only for a gateway on this machine: localhost or a loopback address),
because every command sends its credential there.

--ca-file trusts a PEM bundle for this environment's domain and every name
under it, in addition to the system roots: a cluster on Let's Encrypt's
staging CA, or on a private CA. It is not trusted for any other host.

| Flag | Default | Description |
|------|---------|-------------|
| `--ca-file` | — | PEM CA bundle to trust for this environment's domain only |

### orama env current

Show current active environment

```
orama env current
```

### orama env list

List all available environments

```
orama env list
```

### orama env remove

Remove an environment

```
orama env remove <name>
```

### orama env use

Switch to a different environment

```
orama env use <name>
```

Aliases: `switch`, `enable`

### orama function

Manage serverless functions

```
orama function
```

Deploy, invoke, and manage serverless functions on the Orama Network.

A function is a folder containing:
  function.go    — your handler code (uses the fn SDK)
  function.yaml  — configuration (name, memory, timeout, etc.)
  go.mod         — the Go module TinyGo builds

Quick start:
  orama function init my-function
  cd my-function
  orama function build
  orama function deploy
  orama function invoke my-function --data '{"name": "World"}'

Subcommands: `build`, `delete`, `deploy`, `disable`, `enable`, `get`, `init`, `invoke`, `list`, `logs`, `secrets`, `triggers`, `versions`

### orama function build

Build a function to WASM using TinyGo

```
orama function build [directory]
```

Compiles function.go in the given directory (or current directory) to a WASM binary.
Requires TinyGo to be installed (https://tinygo.org/getting-started/install/).

### orama function delete

Delete a deployed function

```
orama function delete <name> [flags]
```

Deletes a function from the Orama Network. This action cannot be undone.

| Flag | Default | Description |
|------|---------|-------------|
| `-f`, `--force` | `false` | Skip confirmation prompt |

### orama function deploy

Deploy a function to the Orama Network

```
orama function deploy [directory]
```

Deploys the function in the given directory (or current directory).
If no .wasm file exists, it will be built automatically using TinyGo.
Reads configuration from function.yaml.

### orama function disable

Disable a function without deleting it

```
orama function disable <name>
```

Disables a deployed function. The function row stays in the registry but
new invocations are rejected. Use 'orama function enable' to resume.

Useful during incident response — pause a misbehaving function until you
can root-cause without losing its deployed code or version history.

### orama function enable

Re-enable a previously disabled function

```
orama function enable <name>
```

Re-enables a function that was paused with 'orama function disable'.

### orama function get

Get details of a deployed function

```
orama function get <name>
```

Retrieves and displays detailed information about a specific function.

### orama function init

Create a new serverless function project

```
orama function init <name>
```

Scaffolds a new directory with function.go, function.yaml, go.mod and a copy of the function SDK, ready for 'orama function build'.

### orama function invoke

Invoke a deployed function

```
orama function invoke <name> [flags]
```

Sends a request to invoke the named function with optional JSON payload.

| Flag | Default | Description |
|------|---------|-------------|
| `--data` | `{}` | JSON payload to send to the function |

### orama function list

List deployed functions

```
orama function list
```

Lists all functions deployed in the current namespace.

### orama function logs

Get invocation history for a function

```
orama function logs <name> [flags]
```

Retrieves the most recent invocations for a deployed function.

Each invocation record shows: timestamp, request_id, status, duration_ms,
and (if any) the error message. WASM functions that emit log entries via
log_info / log_error have those entries nested under each record.

Pass --wasm-only to retrieve only the WASM-emitted log lines (legacy
behavior; rarely useful on functions that don't call log_info).

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | `50` | Maximum number of records to retrieve |
| `--wasm-only` | `false` | Show only WASM-emitted log entries (legacy view) |

### orama function secrets

Manage function secrets

```
orama function secrets
```

Set, list, and delete encrypted secrets for your serverless functions.

Functions access secrets at runtime via the get_secret() host function.
Secrets are scoped to your namespace and encrypted at rest with AES-256-GCM.

Examples:
  orama function secrets set API_KEY "sk-abc123"
  orama function secrets set CERT_PEM --from-file ./cert.pem
  orama function secrets list
  orama function secrets delete API_KEY

Subcommands: `delete`, `list`, `set`

### orama function secrets delete

Delete a secret

```
orama function secrets delete <name> [flags]
```

Permanently deletes a secret. Functions will no longer be able to access it.

| Flag | Default | Description |
|------|---------|-------------|
| `-f`, `--force` | `false` | Skip confirmation prompt |

### orama function secrets list

List secret names

```
orama function secrets list
```

Lists all secret names in the current namespace. Values are never shown.

### orama function secrets set

Set a secret

```
orama function secrets set <name> [value] [flags]
```

Stores an encrypted secret. Functions access it via get_secret("name"). If --from-file is used, value is read from the file instead.

| Flag | Default | Description |
|------|---------|-------------|
| `--from-file` | — | Read secret value from a file |

### orama function triggers

Manage function PubSub and cron triggers

```
orama function triggers
```

Add, list, and delete triggers for your serverless functions.

PubSub: when a message is published to a topic, every function with a
matching trigger is invoked with the message as input.

Cron: a function is invoked on a schedule (5-field crontab, or 6-field
crontab with a leading seconds column).

Examples:
  orama function triggers add my-function --topic calls:invite
  orama function triggers add my-function --schedule "0 3 * * *"
  orama function triggers add my-function --schedule "*/30 * * * * *"
  orama function triggers list my-function
  orama function triggers delete my-function <trigger-id>

Subcommands: `add`, `delete`, `list`

### orama function triggers add

Add a PubSub or Cron trigger

```
orama function triggers add <function-name> [flags]
```

Registers a trigger that invokes the function automatically.

Pass exactly one of --topic (PubSub) or --schedule (cron). Schedules
accept either 5-field crontab (minute hour dom month dow) or 6-field
with seconds (sec minute hour dom month dow).

| Flag | Default | Description |
|------|---------|-------------|
| `--schedule` | — | Cron expression to trigger on (e.g. "0 3 * * *") |
| `--topic` | — | PubSub topic to trigger on |

### orama function triggers delete

Delete a trigger

```
orama function triggers delete <function-name> <trigger-id>
```

### orama function triggers list

List triggers for a function

```
orama function triggers list <function-name>
```

### orama function versions

List all versions of a function

```
orama function versions <name>
```

Shows all deployed versions of a specific function.

### orama global

Install and operate a global node, and build its chain messages

```
orama global
```

Operate the global role.

On the node, as root: install puts the global services on this machine;
start, stop, restart and status run their units in order, chain first;
validator backs up and migrates the consensus key and builds unjail and edit
messages; stage-oramad places a verified chain binary for cosmovisor.

bind signs the binding that proves a service key belongs to an operator. The
private key stays in its file; the command writes the public key and the
signature. register, bond, unbond, capacity and retire build the node's chain
messages.

Subcommands: `bind`, `bond`, `capacity`, `install`, `register`, `restart`, `retire`, `stage-oramad`, `start`, `status`, `stop`, `unbond`, `validator`

### orama global bind

Sign orama-global-bind-v1 for one service key

```
orama global bind [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--chain-id` | — | Chain id the binding is for [required] |
| `--key-file` | — | Service secret file [required] |
| `--key-type` | — | secp256k1, ed25519, or ed25519-expanded; required for a raw 32-byte file |
| `--operator` | — | Operator account (orama1...) [required] |
| `--service` | — | Service name, for example provider or tor [required] |

### orama global bond

Bond norama to one role on a global node

```
orama global bond [flags]
```

Move norama from the operator account into the node's role bond.

The amount is added to the bond that role already holds. The node must already
be registered with that role. Without --node the command prints the sign
document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--amount` | — | Amount of norama [required] |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--role` | — | Role: validator, storage, relay, exit, dirauth, archiver [required] |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama global capacity

Declare how many bytes a storage node will hold

```
orama global capacity [flags]
```

Declare the storage capacity of a registered node that has the storage role.

The chain refuses a declaration above the capacity the role bond backs, and
below the bytes already reserved by deals. Zero is a declaration of no
capacity. Without --node the command prints the sign document and does not
submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--bytes` | `0` | Declared capacity in bytes |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama global install

Install the global services on this node (run as root)

```
orama global install [flags]
```

Install global services on this machine: chain, and optionally ipfs,
provider, archiver, indexer or repair. The chain is required: the other
services reach it only on this host's loopback RPC. provider needs ipfs beside
it (it pins public deals through the public Kubo). provider and repair are
never installed together. indexer is optional: it serves the chain read API on
loopback for a node that runs an RPC or index endpoint.

For each service it creates the service's system account, copies its binaries
(oramad and this orama CLI for the chain, whose unit runs 'orama global
validator check-sign-floor' before every start; ipfs, Kubo v0.43.1, for ipfs;
orama-global for the others) from --staged-dir into /usr/lib/orama-global/bin
(root-owned, 0755; a symlink in the staged directory is refused, and as root the
directory must be root's and not writable by others), writes and enables its
orama-global-* unit, and opens its public port in ufw (31000 tcp+udp for the
chain, 31010 tcp+udp for the public Kubo swarm, 31013 tcp for the provider)
with the comment orama-global. It does not start anything: 'orama global start'
does, chain first.

The chain unit runs oramad under cosmovisor v1.7.3. Stage the official
cosmovisor-v1.7.3-linux-<amd64|arm64>.tar.gz beside the other binaries: its
SHA-256 must equal the pin built into this CLI, and only its cosmovisor file is
installed. oramad itself is placed in the chain home's cosmovisor layout as the
genesis binary, so the chain home must already have a genesis (--init-chain, or
an existing home). A binary already staged there with different bytes is
refused: change the chain binary with 'orama global stage-oramad --upgrade'.

The ipfs service is a public Kubo of its own: no swarm.key, its own repo in
/var/lib/orama-global/ipfs, swarm on 31010, RPC on 127.0.0.1:31011 (198.18.0.2:31011 with --colocated) behind a
token only the provider's group can read, and a GC timer. --public-storage-gb
is the capacity you will declare with 'orama global capacity'; Kubo's
StorageMax is that plus 10%. It never touches a private cluster's Kubo.

--init-chain creates the chain home with 'oramad init' as orama-chain and puts
the network's --genesis in place. It is never done without the flag, and it is
refused when the home already has a genesis.

An inactive ufw is refused unless --enable-firewall is given; then incoming is
denied by default, --ssh-port is allowed, and ufw is enabled; --ssh-port must
be a port 'sshd -T' reports, or nothing is changed. Running the
command again with the same flags changes nothing but the binaries' bytes.

--colocated installs the services on a machine that already runs a cluster node
(orama node setup first). The global units run in their own network namespace,
orama-global, joined to the root namespace by a veth pair (198.18.0.0/30): they
have their own loopback and port space, cannot reach the cluster's loopback,
WireGuard mesh or any private network, and only the ports they publish are
forwarded in. It writes orama-global-netns.service, two nftables rulesets and a
resolv.conf under /etc/orama-global, and records role both in preferences.yaml.
The machine must have iproute2, nftables, a kernel with network namespaces and
veth, and systemd 242 or newer; otherwise nothing is changed. A machine that is
co-located must keep using --colocated on later installs.

On a co-located machine the chain's RPC and REST API (and the indexer) are
reachable on the namespace address only by root and the cluster node's account.
--chain-client-user <name> (repeatable) also allows a local account, for example
the ssh login that tunnels to the chain or runs 'orama chain'; an unknown account
refuses the install, and the set is kept by later installs.

| Flag | Default | Description |
|------|---------|-------------|
| `--chain-client-user` | — | With --colocated: a local account, besides root and the cluster node's, allowed to connect to the chain's RPC and REST ports on the namespace address (repeatable; kept by later installs) |
| `--chain-id` | — | Chain id, with --init-chain |
| `--colocated` | `false` | Run the services in their own network namespace on a machine that also runs a cluster node |
| `--enable-firewall` | `false` | Enable an inactive ufw (deny incoming, allow --ssh-port) |
| `--genesis` | — | The network's genesis.json, with --init-chain |
| `--init-chain` | `false` | Create the chain home with oramad init and install --genesis |
| `--moniker` | — | Node moniker, with --init-chain |
| `--persistent-peers` | — | Chain peers, id@host:port,... (written into the chain unit) |
| `--public-storage-gb` | `0` | Capacity in GB you will declare for the provider; sizes the public Kubo (required with ipfs) |
| `--services` | — | Services: chain[,ipfs,provider,archiver,indexer,repair] [required] |
| `--ssh-port` | `22` | SSH port --enable-firewall allows |
| `--staged-dir` | — | Directory holding the release's oramad, orama, orama-global, ipfs and the cosmovisor tarball [required] |

### orama global register

Register a global node from signed service-key bindings

```
orama global register [flags]
```

Build MsgRegisterNode from bindings that 'orama global bind' wrote.

The message names the operator, a node id, roles, a hot key that is not the
operator, the bindings, public endpoints, an optional region and an optional
--asn, the autonomous system number the node declares. The chain cannot verify
the ASN; a protocol deal slot goes only to a node that declared one, and slots
go to distinct ASNs. Reserved, documentation and private-use numbers are
refused. It does not include a tenant list or a cluster secret.

--node is the chain REST API. The command reads the account there, asks the
RootWallet agent to sign this one transaction, and broadcasts it. Without
--node it prints the sign document and does not submit anything.

Each --binding file is the JSON bind printed. Its signature must verify for
this --chain-id and --operator.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--asn` | `0` | Autonomous system number the node declares (0 leaves it undeclared) |
| `--binding` | — | Binding JSON from orama global bind [required] |
| `--chain-id` | — | Chain id [required] |
| `--endpoint` | — | Public endpoint (repeatable) |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--hot-key` | — | Hot key account, not the operator [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--region` | — | Region hint |
| `--role` | — | Role: validator, storage, relay, exit, dirauth, archiver [required] |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama global restart

Restart the installed global services in order (run as root)

```
orama global restart [service...]
```

Stop then start the named global services (all installed ones when none is
named). Restarting the chain restarts every installed service, chain first.

### orama global retire

Retire a global node

```
orama global retire [flags]
```

Retire a global node. The chain records its service pubkeys so they cannot
be bound again. Without --node the command prints the sign document and does
not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama global stage-oramad

Place a TUF-verified oramad in the cosmovisor layout

```
orama global stage-oramad [flags]
```

Place an oramad binary where cosmovisor runs it, after it verifies against
the release root adopted at /etc/orama/release-root.json.

--upgrade <name> stages <home>/cosmovisor/upgrades/<name>/bin/oramad for the
upgrade plan <name>; cosmovisor switches to it at the plan's height. --genesis
stages <home>/cosmovisor/genesis/bin/oramad and points current at genesis if
current does not exist yet. A binary already there is refused.

The binary is copied into a root-only staging directory and verified there,
through the descriptor that wrote it, as --release-target in the TUF metadata
in --release-metadata (threshold, timestamp expiry, snapshot rollback, length
and hashes); only then is it linked into place. Every directory on the way is
opened without following symlinks and must be root's; a symlink or a
directory another account owns or may write is refused. Nothing stages
automatically: a validator's operator runs this for every chain upgrade.

The chain unit 'orama global install' writes runs cosmovisor, which reads this
layout; install places the first oramad here as the genesis binary.

| Flag | Default | Description |
|------|---------|-------------|
| `--binary` | — | The oramad binary to stage [required] |
| `--genesis` | `false` | Stage the genesis binary instead of an upgrade |
| `--home` | `/var/lib/orama-global/chain` | cosmovisor DAEMON_HOME |
| `--release-metadata` | — | Directory holding timestamp.json, snapshot.json and targets.json [required] |
| `--release-target` | — | Name the binary has in the release targets metadata [required] |
| `--upgrade` | — | Upgrade plan name to stage for |

### orama global start

Start the installed global services, chain first (run as root)

```
orama global start [service...]
```

Start the installed orama-global-* units, or only the named ones.

The chain starts first. Before it starts, a validator key migrated to this host
is checked against the sign state it last had on its old host; a state behind
it is refused, since it could sign a step the old host already signed. The other
services start once the chain's loopback RPC answers. Starting ipfs, provider,
archiver, indexer or repair alone needs the chain already running. The public
Kubo's GC timer starts and stops with it.

### orama global status

Show the state of each installed global service (run as root)

```
orama global status
```

### orama global stop

Stop the installed global services, chain last (run as root)

```
orama global stop [service...]
```

Stop the installed orama-global-* units, or only the named ones, in reverse
start order. Stopping the chain stops every installed service that needs it
first.

### orama global unbond

Start unbonding norama from one role

```
orama global unbond [flags]
```

Start unbonding norama from one role on a registered global node.

The amount has to be covered by that role's bond. Without --node the command
prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--amount` | — | Amount of norama [required] |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--role` | — | Role: validator, storage, relay, exit, dirauth, archiver [required] |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama global validator

Back up, move and manage this node's validator key

```
orama global validator
```

Subcommands: `check-sign-floor`, `edit`, `export-key`, `migrate`, `reseal`, `unjail`

### orama global validator check-sign-floor

Fail when the chain must not start: key moved away or state behind its floor

```
orama global validator check-sign-floor
```

The double-sign guard. orama-global-chain.service runs it as root before every
start (ExecStartPre), from /usr/lib/orama-global/bin, where 'orama global
install' puts this CLI. With no sign floor recorded it passes. With one, it
fails when priv_validator_key.json is missing (the key moved to another host)
or priv_validator_state.json is behind the floor.

### orama global validator edit

Build or send MsgEditValidator (description, commission)

```
orama global validator edit [flags]
```

Build x/staking MsgEditValidator for the operator's validator. Only the flags
given change; every other description field is sent as [do-not-modify]. An empty
value clears that field. --commission-rate is a decimal from 0 to 1; x/staking
allows one commission change per 24 hours, within the validator's
max-change-rate. Without --node the command prints the sign document.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--commission-rate` | — | New commission rate, 0 to 1 |
| `--details` | — | New details |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--identity` | — | New identity (for example a keybase id) |
| `--moniker` | — | New moniker |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Validator operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--security-contact` | — | New security contact |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--website` | — | New website |

### orama global validator export-key

Write priv_validator_key.json sealed to the operator's public key (run as root)

```
orama global validator export-key [flags]
```

Seal priv_validator_key.json to --recipient, an X25519 public key (64 hex
characters), with the same ORBK seal as a namespace backup, and write it to --to.
The node never holds the private half, so it cannot open the file. --to must
not exist.

To restore the key on a new host, run 'orama global validator migrate prepare'
there, then 'orama global validator reseal' on the machine holding the private
key, then 'orama global validator migrate import' on the new host. Restore only
when the old host is gone: two hosts signing with one key is a double sign.

| Flag | Default | Description |
|------|---------|-------------|
| `--recipient` | — | Operator X25519 public key, hex [required] |
| `--to` | — | File to write; must not exist [required] |

### orama global validator migrate

Move the validator key to another host without a double sign

```
orama global validator migrate
```

Move priv_validator_key.json and priv_validator_state.json from this host to
another, in three steps, each run as root:

  1. on the new host:  orama global validator migrate prepare
  2. on the old host:  orama global validator migrate export --recipient <key> --to <file>
  3. copy <file> to the new host, then:
                       orama global validator migrate import --from <file>

export stops and disables the old host's chain (and stops the services that
need it) before it reads anything. It seals the key and state in memory, records
the state as the old host's sign floor, keeps a copy of the state, moves the key
out of the chain home, and then writes the bundle. The chain unit checks the
floor before every start, so the old host's chain no longer starts: the floor is
recorded and the key is gone. import refuses while the new host's chain runs,
records the old host's last sign state as the new host's floor, writes the
state, and installs the key last; the chain unit then refuses to start from a
state behind the floor. cancel removes a prepared migration key.

A bundle from 'orama global validator reseal' (a restored backup) has no sign
state. Its import needs --old-host-destroyed and --floor-height with the
network's latest committed height H. The floor and the state become height H+1,
round 0, before any step: the restored key signs nothing at or below H, in any
round. It can sign at H+1, so a vote the lost host cast at H+1 is excluded only
if that host stopped before H+1 began.

Subcommands: `cancel`, `export`, `import`, `prepare`

### orama global validator migrate cancel

Remove this host's prepared migration key (run on the new host)

```
orama global validator migrate cancel
```

### orama global validator migrate export

Stop the chain and seal the key and its sign state (run on the old host)

```
orama global validator migrate export [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--recipient` | — | The new host's migration key, from prepare [required] |
| `--to` | — | Bundle file to write; must not exist [required] |

### orama global validator migrate import

Install a migrated key and record its sign floor (run on the new host)

```
orama global validator migrate import [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--floor-height` | `0` | For a reseal bundle: the network's latest committed height; the key signs only above it |
| `--from` | — | Bundle file from export or reseal [required] |
| `--old-host-destroyed` | `false` | For a reseal bundle: confirm the old host can never start again |

### orama global validator migrate prepare

Print this host's migration key (run on the new host)

```
orama global validator migrate prepare
```

### orama global validator reseal

Turn a key backup into a migration bundle for a new host

```
orama global validator reseal [flags]
```

Open a key backup from 'orama global validator export-key' with the operator's
X25519 private key (--identity-file, hex, mode 0600) and seal the key to the new
host's migration key (--recipient, printed by 'orama global validator migrate
prepare'). Run it on the machine that holds the private key, not on a node. The
bundle carries no sign state: nobody knows what a lost host last signed. Its
import therefore needs --old-host-destroyed and --floor-height <the network's
latest committed height>; the key then signs only above that height.

| Flag | Default | Description |
|------|---------|-------------|
| `--from` | — | Key backup from export-key [required] |
| `--identity-file` | — | File holding the operator X25519 private key, hex, mode 0600 [required] |
| `--recipient` | — | The new host's migration key, hex [required] |
| `--to` | — | Bundle file to write; must not exist [required] |

### orama global validator unjail

Build or send MsgUnjail for the operator's validator

```
orama global validator unjail [flags]
```

Build x/slashing MsgUnjail for the validator whose operator account is
--operator (the same bytes as its oramavaloper address), signed by that account.

x/slashing refuses it while the jail period runs, when the validator has no
self-delegation or less than its minimum, and for a tombstoned validator, which
can never unjail. Without --node the command prints the sign document and does
not submit it; with --node the RootWallet agent signs and it is broadcast.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--operator` | — | Validator operator account (orama1...) [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |

### orama inspect

Inspect cluster health via SSH

```
orama inspect [flags]
```

SSH into cluster nodes and run health checks.
Supports AI-powered failure analysis and result export.

The report is written to stdout and progress to stderr, so --format json is one
JSON document. A bad flag value (an unknown --subsystem or --format, a timeout
that is not positive) is refused as usage before any node is contacted.

| Flag | Default | Description |
|------|---------|-------------|
| `--ai` | `false` | Enable AI analysis of failures |
| `--api-key` | — | OpenRouter API key (or OPENROUTER_API_KEY env) |
| `--config` | — | Read nodes from this file instead of resolving them |
| `--env` | — | Environment to inspect (devnet, testnet) |
| `--format` | `table` | Output format (table, json) |
| `--model` | `moonshotai/kimi-k2.5` | OpenRouter model for AI analysis |
| `--output` | — | Save results to directory as markdown (e.g., ./results) |
| `--subsystem` | `all` | Subsystem to inspect (rqlite,olric,ipfs,dns,wg,system,network,tor,global,all) |
| `--timeout` | `30s` | SSH command timeout |
| `--verbose` | `false` | Verbose output |

### orama invite

Mint an invite for a new node

```
orama invite [flags]
```

Create a single-use invite that lets a new node join the cluster.

The invite names one node of the cluster: its public address, the domain to
present to it, and the fingerprint of the TLS certificate it serves. The
joining node connects to exactly that node and pins exactly that certificate,
rather than resolving the cluster's domain — which reaches any nameserver,
each with a certificate of its own — or trusting whatever certificate it is
first shown. There is nothing else to copy across.

The node is the lowest address the environment's domain resolves to, or the
one named with --node. The token is minted through that node, on the same
connection whose certificate is pinned.

This is the same token as 'orama node invite', which does the same thing from
an existing node instead of from here.

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment to invite into (default: active) |
| `--expiry` | `1h0m0s` | How long the invite stays usable (the gateway caps it at 1h) |
| `--node` | — | Public IP of the node the invite names (default: the lowest address the environment's domain resolves to) |

### orama members

Manage who may work in a namespace

```
orama members
```

List, add and remove the wallets that hold a grant in a namespace, and
transfer the namespace itself.

A namespace has exactly one owner. Everybody else holds a role:

  admin    the control plane — deployments, functions, secrets, keys, raw database
  runtime  the data plane — invoke, storage, push, webrtc, proxy, pubsub, cache
  reader   a member with no grant at all

Ownership is not a role you can hand out: use 'orama members transfer'.

Subcommands: `add`, `list`, `remove`, `transfer`

### orama members add

Give a wallet a role in this namespace

```
orama members add <wallet> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--expires-in-hours` | `0` | Expire the grant after this many hours (default: never) |
| `--name` | — | Human label for this member |
| `--namespace` | — | Namespace name |
| `--resource` | — | Narrow the role to a resource, e.g. storage:avatars/* (applied in the cache, fn, pubsub, storage domains; any other is refused) |
| `--role` | — | Role to grant (reader, runtime, developer, admin) |

### orama members list

List who holds a grant in this namespace

```
orama members list [flags]
```

Aliases: `ls`

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama members remove

Take a wallet's grant away

```
orama members remove <wallet> [flags]
```

Aliases: `rm`

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama members transfer

Hand this namespace to another wallet

```
orama members transfer <wallet> [flags]
```

Make another wallet the owner of this namespace.

Only the current owner may do this, and it is one step rather than a removal and
a grant, so there is no moment where the namespace has no owner.
You keep an admin grant, so handing a project over does not lock you out of it.
<wallet> must be a wallet address (0x and 40 hex digits, or a Solana public key);
the gateway refuses anything else rather than hand the namespace to nobody.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Skip confirmation prompt |
| `--namespace` | — | Namespace name |

### orama monitor

Monitor cluster health from your local machine

```
orama monitor [flags]
```

Show the cluster's health: a live view, or one aspect at a time.

The data comes from the gateway's operator telemetry API
(GET /v1/operator/telemetry, and its server-sent event stream for the live
view), authenticated with the credentials 'orama auth login' stored for the
environment's gateway. Only the cluster's operators may read it.

--ssh is the break-glass path for when no gateway answers: it SSHes into every
node and runs 'sudo orama node report --json' there instead. It is never chosen
automatically; when the API fails the error says so and suggests it. Traffic is
counted by the gateways, so it is empty over --ssh.

Without a subcommand, opens the live view. Every view starts with the verdict:
"✓ All systems operational" or what is degraded, with the alert counts and the
age of the data. Live view keys: tab/shift+tab or 1-9 switch tabs, ↑/↓ (j/k)
select or scroll, enter opens a node's full report on the Nodes tab, esc goes
back, c/w/i/a filter the Alerts tab by severity, r refreshes, ? shows help,
q quits.

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | — | With --ssh: read nodes from this file instead of resolving them |
| `--env` | — | Environment: devnet, testnet, mainnet (required) |
| `--interval` | `5s` | How often the live view refreshes, 2s to 60s (with --ssh: at least 15s, which is also its default) |
| `--node` | — | Show only this node (public IP or WireGuard IP) |
| `--ssh` | `false` | Collect over SSH from every node instead of the gateway API (break-glass) |

Subcommands: `alerts`, `chain`, `cluster`, `dns`, `live`, `mesh`, `namespaces`, `node`, `report`, `service`, `traffic`

### orama monitor alerts

Alerts, most severe first, with what to do (one-shot)

```
orama monitor alerts
```

### orama monitor chain

Orama L1 height, sync and validators (one-shot)

```
orama monitor chain
```

### orama monitor cluster

Verdict, components and a row per node (one-shot)

```
orama monitor cluster
```

### orama monitor dns

DNS and TLS health of the nameservers (one-shot)

```
orama monitor dns
```

### orama monitor live

Interactive live view (the default)

```
orama monitor live [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `5s` | How often the live view refreshes, 2s to 60s (with --ssh: at least 15s, which is also its default) |

### orama monitor mesh

WireGuard mesh connectivity (one-shot)

```
orama monitor mesh
```

### orama monitor namespaces

Namespace health across nodes (one-shot)

```
orama monitor namespaces
```

### orama monitor node

Per-node health details (one-shot)

```
orama monitor node
```

### orama monitor report

Full cluster report as JSON (one-shot)

```
orama monitor report
```

### orama monitor service

Service status across the cluster (one-shot)

```
orama monitor service
```

### orama monitor traffic

Gateway requests, errors and latency (one-shot)

```
orama monitor traffic
```

### orama namespace

Manage namespaces

```
orama namespace
```

Aliases: `ns`

List, delete, and repair namespaces on the Orama network.

Subcommands: `backup-open`, `backup-seal`, `backup`, `create`, `delete`, `disable`, `enable`, `keys`, `list`, `repair`, `restore-key`, `restore`, `rqlite`, `session-policy`, `webrtc-status`

### orama namespace backup

Take a backup of the namespace, sealed to your X25519 public key

```
orama namespace backup [flags]
```

Ask the namespace gateway for a backup: its RQLite snapshot, the CIDs it
has pinned, and its secrets, decrypted by the cluster and sealed with the rest
to the public key you give. The cluster never holds the private key and cannot
open what it wrote. Keep the private key off the cluster.

It goes to the namespace's own gateway (the host 'orama auth login --namespace'
stored). With ORAMA_TOKEN, set ORAMA_API_URL to that host
(https://ns-<name>.<domain>): the environment's gateway does not serve backup.

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | — | your X25519 backup public key, 64 hex characters |
| `--out` | — | file to write the sealed backup to |

### orama namespace backup-open

Decrypt a backup file with an X25519 private key

```
orama namespace backup-open [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--in` | — | input file |
| `--key` | — | 32-byte X25519 key, hex (public for seal, private for open) |
| `--out` | — | output file |

### orama namespace backup-seal

Encrypt a backup file to an X25519 public key

```
orama namespace backup-seal [flags]
```

Encrypt a file to the owner's backup public key.

The cluster holds only that public key. It cannot decrypt the file.
This seals any file. A namespace's own backup is 'orama namespace backup', and
putting one back is 'orama namespace restore'.

| Flag | Default | Description |
|------|---------|-------------|
| `--in` | — | input file |
| `--key` | — | 32-byte X25519 key, hex (public for seal, private for open) |
| `--out` | — | output file |

### orama namespace create

Create a namespace and start its cluster

```
orama namespace create <name>
```

Create a namespace. The wallet you are signed in as becomes its owner.

Creating a namespace used to happen by itself: signing in to a name that did
not exist created it. So a typo made a namespace, and one belonged to whoever
happened to sign in first.

  orama namespace create myapp
  orama auth login --namespace myapp

### orama namespace delete

Delete the current namespace and all its resources

```
orama namespace delete [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Skip confirmation prompt |

### orama namespace disable

Disable a feature for a namespace

```
orama namespace disable <feature> [flags]
```

Disable a feature for a namespace. Supported features: webrtc

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama namespace enable

Enable a feature for a namespace

```
orama namespace enable <feature> [flags]
```

Enable a feature for a namespace. Supported features: webrtc

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama namespace keys

Manage scoped API keys (bugboard #148)

```
orama namespace keys
```

Create, list, and revoke scoped API keys. Profiles: invoke-only | app-runtime | admin.

Subcommands: `create`, `list`, `revoke-legacy`, `revoke`, `rotate`

### orama namespace keys create

Mint a new scoped API key

```
orama namespace keys create [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--expires-in-days` | `0` | How long the key lives, in days (default 90, max 365). A key that never expires is not on offer |
| `--label` | — | Human label for the key |
| `--namespace` | — | Namespace name |
| `--scope` | — | Profile (invoke-only\|app-runtime\|admin) or a comma-separated grant list (admin, cache, invoke, proxy, pubsub, push, storage, webrtc) |

### orama namespace keys list

List scoped API keys

```
orama namespace keys list [flags]
```

Aliases: `ls`

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama namespace keys revoke

Revoke a single API key by id

```
orama namespace keys revoke [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--id` | `0` | Key id to revoke |
| `--namespace` | — | Namespace name |

### orama namespace keys revoke-legacy

Revoke ALL legacy (unscoped) keys — the cutover step

```
orama namespace keys revoke-legacy [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Skip confirmation prompt |
| `--namespace` | — | Namespace name |

### orama namespace keys rotate

Mint a successor to a key and keep the old one working for an overlap

```
orama namespace keys rotate [flags]
```

Mint a new key with the same grants and label, and shorten the original's life
to the overlap.

Rotating by minting a new key and revoking the old one in the same breath is an
outage: whatever is deployed with the old key stops the moment the new one
exists. The overlap is the window in which to deploy the successor — both keys
work, and the original then expires on its own.

| Flag | Default | Description |
|------|---------|-------------|
| `--expires-in-days` | `0` | How long the successor lives, in days (default 90) |
| `--id` | `0` | Key id to rotate |
| `--namespace` | — | Namespace name |
| `--overlap-days` | `0` | How long the old key keeps working (default 7, max 30) — the window to deploy the new one |

### orama namespace list

List namespaces owned by the current wallet

```
orama namespace list
```

Aliases: `ls`

### orama namespace repair

Repair an under-provisioned namespace cluster

```
orama namespace repair <namespace>
```

Repair an under-provisioned namespace cluster. Run it on a node. It talks to that node's gateway on the node's WireGuard address; localhost is where public traffic arrives, so a repair sent there is refused.

### orama namespace restore

Restore a namespace backup onto the namespace gateway (DESTRUCTIVE)

```
orama namespace restore [flags]
```

Open a backup on this machine with your private key, seal its secrets to
the destination gateway's restore key (--dest-key, from 'orama namespace
restore-key'), and send it to the namespace gateway you are signed in to.

The gateway replaces the namespace's entire RQLite database with the backup,
writes the secrets under its own cluster's encryption root, and pins every CID
in the backup. The namespace must already exist on the destination, and
--namespace must name the namespace the backup was taken of. A wrong key, a
corrupt file or a different namespace stops before anything is sent.

The gateway also refuses, before writing anything, a restore that would put
the namespace over its storage quota on the destination, and it keeps the
destination's quota rather than the one in the backup. It runs one backup or
restore at a time and answers 429 while one is running.

The live keys, grants and sessions (in the cluster's registry) are not
restored: a restore neither brings back a key that was revoked nor removes one
minted since. The backup's database still carries stale copies of those rows,
which nothing reads. Before loading, the gateway checks the database image and
refuses (400, nothing written) one that is damaged or carries triggers, views
over platform tables or stored-object records for content only other namespaces
hold; it removes plaintext API keys and other namespaces' records an older backup
may carry, and checks again, after the load.

With ORAMA_TOKEN, set ORAMA_API_URL to the namespace's gateway
(https://ns-<name>.<domain>): the environment's gateway does not serve restore.

| Flag | Default | Description |
|------|---------|-------------|
| `--dest-key` | — | destination gateway's restore public key, from 'orama namespace restore-key' |
| `--in` | — | sealed backup file |
| `--key-file` | — | file holding your X25519 backup private key, 64 hex characters |
| `--namespace` | — | namespace the backup was taken of; must match the backup |

### orama namespace restore-key

Print the namespace gateway's restore public key

```
orama namespace restore-key
```

Print the X25519 public key a restore's secrets are sealed to. It is
derived from the destination cluster's encryption root and the namespace, so
it is different for every namespace and changes when that root is rotated.
Pass it to 'orama namespace restore --dest-key'.

### orama namespace rqlite

Manage the namespace's internal RQLite database

```
orama namespace rqlite
```

Export and import the namespace's internal RQLite database: your own tables and the
namespace's functions, function secrets, stored-object records, quotas and push and
WebRTC settings. Keys, grants and deployments are in the cluster registry, not in it.

Both go to the namespace's own gateway (the host 'orama auth login --namespace' stored).
With ORAMA_TOKEN, set ORAMA_API_URL to that host (https://ns-<name>.<domain>): the
environment's gateway does not serve them.

Subcommands: `export`, `import`

### orama namespace rqlite export

Export the namespace's RQLite database to a local SQLite file

```
orama namespace rqlite export [flags]
```

Downloads a consistent SQLite snapshot of the namespace's internal RQLite database.

| Flag | Default | Description |
|------|---------|-------------|
| `-o`, `--output` | — | Output file path (default: rqlite-export.db) |

### orama namespace rqlite import

Import a SQLite dump into the namespace's RQLite (DESTRUCTIVE)

```
orama namespace rqlite import [flags]
```

Replaces the namespace's entire RQLite database with the contents of the provided SQLite file.

WARNING: This is a destructive operation. All existing data in the namespace's RQLite
(your tables, functions, function secrets, stored-object records, quotas and push and
WebRTC settings) will be replaced with the imported file. The file must be a SQLite
database, as 'export' writes; at most 256 MiB. Live keys and grants (in the cluster
registry) are not replaced, and stale copies in the file are ignored. The namespace's
storage quota stays as it was. The gateway checks the file before loading it and
refuses (400, nothing written) one that is damaged or carries triggers, views over
platform tables or stored-object records for content only other namespaces hold;
it removes plaintext API keys and other namespaces' records an older file may carry,
and checks again, after the load. One backup, restore, export or
import runs at a time on a gateway.

| Flag | Default | Description |
|------|---------|-------------|
| `-i`, `--input` | — | Input SQLite file path |

### orama namespace session-policy

Show or set who may sign in to a namespace and what its sessions bind

```
orama namespace session-policy [flags]
```

With no flags, show the namespace's session policy. With flags, set them; a flag
left out keeps its value.

  --sign-in members   only wallets holding a grant sign in (the default)
  --sign-in open      a wallet holding none may sign in too, as an end user of
                      the application. It gets a session and no API key, is
                      never granted anything, and reaches only what a grantless
                      wallet reaches. Closing it again ends those sessions at
                      their next refresh. A namespace nobody owns stays closed.

  --device-policy     optional | required | approval: what an end user's
                      sign-in must bind. Requiring devices revokes the sign-in
                      keys end users already hold.

Changing either needs write access to the namespace.

  orama namespace session-policy --namespace myapp --sign-in open

| Flag | Default | Description |
|------|---------|-------------|
| `--device-policy` | — | What an end user's sign-in must bind: optional \| required \| approval |
| `--namespace` | — | Namespace name |
| `--sign-in` | — | Who may sign in: members \| open |

### orama namespace webrtc-status

Show WebRTC service status for a namespace

```
orama namespace webrtc-status [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama node

Node operator commands

```
orama node
```

Operate Orama nodes, both the one on this machine and the fleet you own.

Local, run on the node itself and needing root (sudo):
  install, uninstall, upgrade, start, stop, restart, status, logs, doctor,
  report, invite, unlock, schema, migrate, migrate-raft-id, migrate-conf,
  stage-archive (run by push)

Remote, run from your machine and reaching nodes over SSH:
  list, setup, enroll, push, rollout, clean, remove, wipe, recover-raft,
  dns delegation

The remote commands are the same implementations as the top-level 'orama push',
'orama rollout' and 'orama nodes'.

Subcommands: `autoupdate`, `clean`, `dns`, `doctor`, `enroll`, `install`, `invite`, `list`, `logs`, `migrate-conf`, `migrate-raft-id`, `push`, `recover-raft`, `remove`, `report`, `restart`, `rollout`, `schema`, `setup`, `stage-archive`, `start`, `status`, `stop`, `uninstall`, `unlock`, `upgrade`, `wipe`

### orama node autoupdate

Decide whether a newer release should be installed

```
orama node autoupdate [flags]
```

Report what this cluster should do with a candidate release.

The default mode is notify: a newer verified release is reported and not
installed. auto means the node may install, and only when the cluster is
healthy, the release is newer, and the maintenance window is open. The
install itself is one node at a time and is not performed by this command.

A release that fails TUF verification, including a rolled-back snapshot or
an expired timestamp, is refused. So is a downgrade and a release a previous
health-gate failure marked bad.

A validator (--role validator) is never auto: the mode is refused, and chain
upgrades are staged explicitly with 'orama global stage-oramad'.

| Flag | Default | Description |
|------|---------|-------------|
| `--bad` | `false` | candidate was marked bad by a failed health gate |
| `--candidate` | — | version being considered |
| `--channel` | `stable` | release channel |
| `--current` | — | version installed now |
| `--degraded` | `false` | cluster is already degraded |
| `--healthy-voters` | `2` | raft voters that are up |
| `--mode` | `notify` | off, notify, or auto |
| `--role` | `cluster` | this node's role: cluster or validator |
| `--verify` | — | simulated TUF failure: rollback, freeze, threshold, or hash |
| `--voters` | `3` | raft voters |
| `--window` | — | maintenance window as start-end hours, for example 1-5 |

### orama node clean

Deprecated: use 'orama node wipe' or 'orama node remove'

```
orama node clean [flags]
```

DEPRECATED. Use 'orama node wipe' or 'orama node remove'.

'clean' only ever erased the target. It said nothing to the rest of the cluster,
so a cleaned node stayed a configured raft voter counted toward quorum, kept its
wireguard_peers row re-applied to every survivor's interface, and kept its
dns_nodes row. It also stopped only the legacy host unit names, leaving tenant
'orama-namespace-*@*' units running under a deleted data directory.

  orama node wipe           erases a node (what clean did, fixed)
  orama node remove   removes one node from the cluster, then erases it

This command now runs 'wipe'.

Examples:
  orama node wipe --env testnet --node 1.2.3.4
  orama node remove --env testnet --node 1.2.3.4

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--force` | `false` | Skip confirmation (DESTRUCTIVE) |
| `--node` | — | Public IP of the node to wipe; omit to wipe every node in the environment |
| `--nuclear` | `false` | Also remove shared binaries (rqlited, ipfs, caddy, ...) |

### orama node dns

Cluster DNS: what the outside world needs to reach its nameservers

```
orama node dns
```

Subcommands: `delegation`

### orama node dns delegation

Print the NS and glue records to create at the parent zone

```
orama node dns delegation [flags]
```

Print exactly the records the operator must create at the parent zone (or
registrar) so the internet reaches this cluster's nameservers: one NS record
per nameserver, and the glue A record that gives each nameserver its address.

Nameserver slots (ns1, ns2, …) are claimed by the --nameserver nodes as they
come up, so which address holds which name is only known to the cluster. This
reads it from the cluster over SSH. Only slots whose glue the cluster has
written are listed — the same set the cluster's own zone publishes.

Run it again after adding or removing a nameserver, and update the parent
zone to match. See docs/NAMESERVER_SETUP.md.

| Flag | Default | Description |
|------|---------|-------------|
| `--cloudflare-token-file` | — | Create or update the NS and glue records in the parent Cloudflare zone, then check DNS |
| `--env` | — | Environment to read (devnet, testnet, …) [required] |

### orama node doctor

Diagnose common node issues

```
orama node doctor
```

Run a series of diagnostic checks on this node to identify
common issues with services, connectivity, disk space, and more.

### orama node enroll

Enroll an OramaOS node into the cluster

```
orama node enroll [flags]
```

Enroll a freshly booted OramaOS node into the cluster.

The OramaOS node prints a registration code on its console. Provide that code
along with an invite token. The Gateway pushes cluster configuration
(WireGuard, secrets, peer list) to the node, sealed under the code.

The code is not served over the network. A GET on port 9999 used to return it.

Usage:
  orama node enroll --node-ip <ip> --code <code> --token <invite-token> --gateway <url>

--gateway must be an https:// URL: the invite token is a credential and is never
sent in the clear.

The node must be reachable over the public internet on port 9999 (enrollment only).
After enrollment, port 9999 is permanently closed and all communication goes over WireGuard.

| Flag | Default | Description |
|------|---------|-------------|
| `--code` | — | Registration code from the node's console (required) |
| `--env` | `production` | Environment name |
| `--gateway` | — | Gateway URL (required, e.g. https://gateway.example.com) |
| `--node-ip` | — | Public IP of the OramaOS node (required) |
| `--token` | — | Invite token for cluster joining (required) |

### orama node install

Install production node (requires sudo)

```
orama node install [flags]
```

Install and configure an Orama production node on this machine.
For the first node, this creates a new cluster. For subsequent nodes,
use --join and --token to join an existing cluster.

Run it on the node itself with sudo, or from your own machine with --remote to
drive the install over SSH against --vps-ip. Which of the two happened used to
be decided by whether you had used sudo.

The build archive must be extracted at /opt/orama and signed by a wallet in the
node's trust anchor, /etc/orama/archive-signers. A genesis install creates the
anchor from --operator-wallet (required) before it verifies the archive; a
joining node takes it from the cluster in the join response. With --remote,
--archive names the build: it is verified on this machine against
--operator-wallet before it is uploaded.

| Flag | Default | Description |
|------|---------|-------------|
| `--acme-ca` | — | ACME directory for TLS certificates: letsencrypt (production, the default), letsencrypt-staging (clusters rebuilt many times a week) or an https URL |
| `--archive` | — | With --remote: the build archive to upload, verified here against --operator-wallet first |
| `--base-domain` | — | Base domain for deployment routing (e.g., example.com) |
| `--ca-fingerprint` | — | SHA-256 fingerprint of the gateway's TLS cert; the invite carries this, so it is only needed to override it |
| `--domain` | — | Domain for HTTPS (auto-generated for non-nameserver nodes if omitted) |
| `--dry-run` | `false` | Show what would be done without making changes |
| `--environment` | — | Environment name (devnet, testnet, etc.) |
| `--expect-archive-signers` | — | When joining: the archive signers the cluster must send (comma-separated); the archive is verified against them before the join |
| `--force` | `false` | Force reconfiguration even if already installed |
| `--host-key` | — | Expected SSH host-key fingerprint (SHA256:...) for --remote; omit to confirm it interactively |
| `--ipfs-addrs` | — | Comma-separated multiaddrs of existing IPFS node |
| `--ipfs-cluster-addrs` | — | Comma-separated multiaddrs of existing IPFS Cluster node |
| `--ipfs-cluster-peer` | — | Peer ID of existing IPFS Cluster node |
| `--ipfs-peer` | — | Peer ID of existing IPFS node to peer with |
| `--join-sni` | — | Server name to present to --join; the invite carries it, so it is only needed to override it |
| `--join` | — | Gateway to join; the invite carries this, so it is only needed to override it |
| `--nameserver` | `false` | Make this node a nameserver (runs CoreDNS + Caddy) |
| `--operator-wallet` | — | Operator wallet address |
| `--peers` | — | Comma-separated list of bootstrap peer multiaddrs |
| `--remote` | `false` | Install the machine at --vps-ip over SSH, instead of this machine |
| `--skip-checks` | `false` | Skip minimum resource checks (RAM/CPU) |
| `--skip-firewall` | `false` | Skip UFW firewall setup (for users who manage their own firewall) |
| `--ssh-user` | — | SSH user for remote management |
| `--token` | — | Invite from 'orama invite'; it carries the gateway to join and the certificate to pin |
| `--vps-ip` | — | Public IP of this VPS (required) |

### orama node invite

Manage invite tokens for joining the cluster

```
orama node invite [flags]
```

Generate invite tokens that allow new nodes to join the cluster.
Running without a subcommand creates a new token (same as 'invite create').

| Flag | Default | Description |
|------|---------|-------------|
| `--expiry` | `1h0m0s` | How long the token stays valid |
| `--raw` | `false` | Print only the invite, for scripts (orama node setup --join-via reads it this way) |

### orama node list

List your nodes across environments

```
orama node list [flags]
```

List all nodes owned by your wallet. Queries the network API
with your stored credentials, falling back to nodes.conf.

Requires: orama auth login (for API-based resolution)

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Filter by environment (default: active environment) |

### orama node logs

View production service logs

```
orama node logs <service> [flags]
```

Stream the journal of one service on this node.

<service> is an alias or a unit name. A tenant service is a systemd template
instance, so name it in full:

  orama node logs orama-namespace-olric@anchat

--since takes a window rather than a line count, which is what a diagnostic
that greps for a periodic line needs:

  orama node logs node --since -30min | grep 'WireGuard peer sync completed'

Aliases: caddy, cluster, coredns, gateway, ipfs, ipfs-cluster, node, olric, rqlite, turn

| Flag | Default | Description |
|------|---------|-------------|
| `--since` | — | Show entries newer than this, e.g. -30min or "2 hours ago" (overrides --lines) |
| `-f`, `--follow` | `false` | Stream new log lines as they arrive |
| `-n`, `--lines` | `50` | How many lines of history to show |

### orama node migrate-conf

Register nodes.conf nodes with your wallet

```
orama node migrate-conf [flags]
```

One-time migration: reads nodes from nodes.conf for an environment
and registers each with your wallet via the gateway API. After migration,
these nodes will appear in 'orama nodes' output.

Requires: orama auth login (for API authentication)

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment to migrate (default: active) |

### orama node migrate-raft-id

Move nodes to stable, peer-id-based raft identities (one-time)

```
orama node migrate-raft-id [flags]
```

Give each node a raft identity that survives an address change.

RQLite defaults a node's raft id to its raft advertise address, so identity has
been a function of routing: give the same machine a new overlay address — a
replacement, a WireGuard re-provision, a 10.0.0.x reassignment — and it mints a
new raft id, joins as a SECOND member, and the old entry stays in the
configuration as a voter nothing can reach. Two such events on a five-voter
cluster leave quorum at 3-of-7 with five live voters; one more failure freezes
the registry.

RQLite cannot rename a member in place, so this is a deliberate migration rather
than something an upgrade does silently. Nodes are migrated ONE AT A TIME. For
each: the quorum arithmetic is checked, the old id is removed from the raft
configuration and tombstoned, the node's local raft state is discarded, and it
rejoins under its libp2p peer id and replicates back from the leader. The next
node is not touched until the previous one is back in the configuration.

Safe to re-run: nodes already on a stable id are skipped, so an interrupted run
continues where it stopped.

Examples:
  orama node migrate-raft-id --env testnet --dry-run
  orama node migrate-raft-id --env testnet
  orama node migrate-raft-id --env testnet --node 1.2.3.4

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Report what would change and exit |
| `--env` | — | Target environment [required] |
| `--force` | `false` | Skip the confirmation prompt |
| `--node` | — | Migrate only this public IP. Default: every node that needs it |

### orama node push

Push the binary archive to your nodes

```
orama node push [flags]
```

Upload the pre-built binary archive to nodes and extract it.

By default the archive is uploaded once to a hub node, which then distributes
it to the others server-to-server. Use --direct to upload from this machine to
each node in turn.

'orama push' and 'orama node push' are the same command.

--archive names the build: the path 'orama build' printed. There is no
default — the newest archive in /tmp may be another checkout's build.

Examples:
  orama push --env devnet --archive /tmp/orama-0.200.0-linux-amd64.tar.gz
  orama push --env devnet --archive <path> --direct    # Upload to each node in turn
  orama push --env devnet --archive <path> --node 1.2.3.4
  orama push --host 1.2.3.4 --archive <path>           # A node not in the inventory yet
  orama push --env devnet --archive <path> --trust-signers 0xYourWallet  # Nodes from before archive signing

Each node verifies the archive with its installed orama before anything under
/opt/orama changes: the manifest signature must recover to an address in the
node's /etc/orama/archive-signers and every file must match the manifest.

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | The build archive to push (the path `orama build` printed) [required] |
| `--direct` | `false` | Upload from here to each node in turn, instead of fanning out |
| `--env` | — | Target environment (default: active) |
| `--host` | — | Push to a node that is not in the inventory yet |
| `--node` | — | Push to a single node IP from the inventory |
| `--trust-signers` | — | Create the archive trust anchor on nodes that have none (installed before archive signing); never changes an existing one |
| `--user` | — | SSH user for --host (default: root) |

### orama node recover-raft

Recover RQLite cluster from split-brain

```
orama node recover-raft [flags]
```

Recover the RQLite Raft cluster from split-brain failure.

One node's data is kept. Every other node's raft log and database are DELETED
and rebuilt from it. Nothing is backed up: there is no copy to restore from
afterwards, and the deleted nodes' data is gone. Take a backup yourself first
if the surviving node might not be the right one.

What happens:
  1. Stop orama-node on every node
  2. Reset the kept node to a single-member cluster, preserving its data,
     raft log and raft term
  3. Start it and confirm it comes back as Leader with its data intact
  4. Delete raft.db, raft/, db.sqlite (+shm/wal) and wsnapshots (rsnapshots) on every other
     node, and record the kept node as the member each one re-joins
     (data/cluster-membership.json)
  5. Start them one at a time; each pulls a full snapshot from the kept node
  6. Verify cluster health

Which node is kept decides which copy of the data survives. Without --leader
the command reads every node's applied index, keeps the furthest ahead, and
prints what each one reported before asking you to confirm. --leader overrides
that.

Use --leader-raft-addr when quorum is already lost and rqlite is not answering
anywhere, so the leader's raft address cannot be read from the cluster.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node recover-raft --env testnet
  orama node recover-raft --env testnet --leader 1.2.3.4
  orama node recover-raft --env devnet --leader-raft-addr 10.0.0.1:10101 --force

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--force` | `false` | Skip confirmation (DESTRUCTIVE) |
| `--leader-raft-addr` | — | Explicit leader raft address host:port (e.g. 10.0.0.1:10101). Use when quorum is already lost so the leader can't be auto-resolved; bypasses the live-Leader check. |
| `--leader` | — | IP of the node whose data to keep; default is the node with the highest applied index |

### orama node remove

Remove one node from the cluster, then erase it

```
orama node remove [flags]
```

Aliases: `decommission`

Retire a node from every store the cluster keeps, then wipe it.

Runs the cluster-side removal from a SURVIVOR. First it prints what the removal
costs every raft cluster the node is a voter in — the platform cluster and each
namespace it serves — and refuses if any of them would lose quorum. Then it
takes the node out of the raft configuration, writes an eviction tombstone so
nothing re-adds it automatically, releases its mesh address, nameserver slot,
namespace memberships, namespace port blocks and its TURN and SFU allocations,
and marks it retired so the cluster purges its DNS records. Then it wipes the
target, unless --offline.

Use --offline when the machine is already gone. The cluster-side removal still
happens; nothing is attempted against the target.

Every step is keyed on the node and safe to repeat, so a removal that failed
part way through is finished by running it again.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node remove --env testnet --node 1.2.3.4 --dry-run   # Show the plan only
  orama node remove --env testnet --node 1.2.3.4
  orama node remove --env testnet --node 1.2.3.4 --offline   # VPS already deleted
  orama node remove --env testnet --node 1.2.3.4 --force

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Print the quorum impact and the statements, change nothing |
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--force` | `false` | Skip confirmation (DESTRUCTIVE) |
| `--node` | — | Public IP of the node to remove [required] |
| `--nuclear` | `false` | When wiping, also remove shared binaries and the Tor package |
| `--offline` | `false` | The node is already gone: retire it cluster-side only, do not try to wipe it |

### orama node report

Output comprehensive node health data as JSON

```
orama node report [flags]
```

Collect all system and service data from this node and output
as a single JSON blob. Designed to be called by 'orama monitor' over SSH.
Requires root privileges for full data collection.

| Flag | Default | Description |
|------|---------|-------------|
| `--pretty` | `false` | Indent the JSON for reading, instead of one line |

### orama node restart

Restart all production services (requires sudo)

```
orama node restart [flags]
```

Restart all Orama services. Stops in dependency order then restarts.
Includes explicit namespace service restart.
Use --force to bypass quorum safety check.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Bypass quorum safety check |

### orama node rollout

Build, push, and rolling upgrade every node in an environment

```
orama node rollout [flags]
```

Full deployment pipeline: build the binary archive, push it to every node,
then upgrade them one at a time.

The rolling upgrade prints its plan — which node holds the raft leadership and
the order the restarts happen in — and stops unless --yes is given.

'orama rollout' and 'orama node rollout' are the same command.

Examples:
  orama rollout --env testnet             # Build, push, then print the plan
  orama rollout --env testnet --yes       # Execute the plan
  orama rollout --env testnet --no-build  # Reuse the existing archive

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | With --no-build: the build archive to roll out |
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--no-build` | `false` | Skip the build step; roll out the archive named by --archive |
| `--yes` | `false` | Execute the rollout plan instead of only printing it |

### orama node schema

Inspect and apply gateway schema migrations against the local RQLite

```
orama node schema [flags]
```

Schema lifecycle commands.

The gateway binary embeds a set of SQL migrations. Each migration is numbered;
the highest number is the schema version the binary requires. After deploying
a new gateway binary, run 'orama node schema apply' on every namespace's RQLite
to bring the schema up to date — otherwise function deploys fail at runtime
with cryptic missing-column errors.

| Flag | Default | Description |
|------|---------|-------------|
| `--dsn` | — | RQLite DSN (default: this node's index rqlite from /opt/orama/.orama/configs/node.yaml) |

Subcommands: `apply`, `status`

### orama node schema apply

Apply pending migrations to the local RQLite

```
orama node schema apply [flags]
```

Apply every embedded migration not yet recorded in schema_migrations.

Each migration runs as one transaction together with its schema_migrations
row, so it is applied and recorded, or not applied at all. A statement whose
effect is already in place (an existing column, table or index, left by an older
engine that applied migrations statement by statement) is skipped. Any other
error aborts the run at that migration, which leaves no trace; re-running is
safe because each migration is independently versioned.

| Flag | Default | Description |
|------|---------|-------------|
| `--yes` | `false` | Skip the confirmation prompt |

### orama node schema status

Show required vs applied schema version + pending migrations

```
orama node schema status
```

### orama node setup

Set up a fresh VPS as an Orama node

```
orama node setup [flags]
```

Bootstrap a fresh VPS into a running Orama node in one command.

Creates an SSH key in rootwallet, installs it on the VPS, uploads the binary
archive, and runs the node install. For the first node, use --genesis to
create a new cluster.

Examples:
  # Genesis node (first node, creates new cluster).
  # Store the VPS login first: rw vault add 1.2.3.4 (username root).
  # --password is a switch; it reads that login. --archive is the path
  # "orama build" printed.
  orama node setup --ip 1.2.3.4 --password --env devnet \
    --base-domain orama-devnet.network --role nameserver --genesis \
    --archive /tmp/orama-<version>-linux-amd64.tar.gz

  # Join existing cluster
  orama node setup --ip 5.6.7.8 --password --env devnet \
    --base-domain orama-devnet.network \
    --archive /tmp/orama-<version>-linux-amd64.tar.gz

  # Key-only VPS (no password login): install the RootWallet key once
  # with the key that opens it today. Do not pass --password as well.
  orama node setup --ip 5.6.7.8 --user ubuntu --bootstrap-key ~/.ssh/id_ed25519 \
    --env devnet --base-domain orama-devnet.network \
    --archive /tmp/orama-<version>-linux-amd64.tar.gz

  # Join as nameserver
  orama node setup --ip 9.10.11.12 --password --env devnet \
    --base-domain orama-devnet.network --role nameserver \
    --archive /tmp/orama-<version>-linux-amd64.tar.gz

| Flag | Default | Description |
|------|---------|-------------|
| `--acme-ca` | — | ACME directory for the node's TLS certificates (passed to node install): letsencrypt, letsencrypt-staging or an https URL |
| `--archive` | — | Build archive to install — the path `orama build` printed [required]; a node already running this exact build is not re-uploaded |
| `--base-domain` | — | Base domain for the network |
| `--bootstrap-key` | — | SSH private key that opens the VPS today (key-only images, e.g. --user ubuntu); used once to install the RootWallet key, never stored |
| `--env` | — | Target environment (default: active) |
| `--gateway` | — | Gateway URL of the cluster to join (default: the environment's): its domain, e.g. https://orama-devnet.network; the invite is minted through one of its nodes and pins that node's certificate |
| `--genesis` | `false` | Create a new cluster (first node) |
| `--host-key` | — | Expected SSH host-key fingerprint (SHA256:...) of the VPS; omit to confirm it interactively |
| `--ip` | — | Public IP address of the VPS (required) |
| `--join-via` | — | user@ip of a node already in the cluster; the invite is minted there over SSH (no 'orama auth login' needed) |
| `--password` | `false` | Bootstrap over password login; the password is read from your RootWallet vault login for the IP (rw vault add <ip>), never from the command line |
| `--role` | `node` | Node role: node or nameserver |
| `--user` | `root` | SSH user on the VPS |

### orama node stage-archive

Verify a pushed build archive and put it in place (run by 'orama push')

```
orama node stage-archive [flags]
```

Verify a build archive against this node's trust anchor, /etc/orama/archive-signers,
and only then replace the archive files under /opt/orama with it.

'orama push' runs this on every node with the node's installed orama. The
archive is extracted into a private directory, its manifest signature must
recover to a trusted signer and every file must match the signed manifest;
anything else leaves /opt/orama untouched. The replacement is undone if any
step of it fails, and holds the lock install and upgrade take on /opt/orama.

--trust-signers creates the anchor on a node installed before archives were
signed, and only after the archive has verified against those addresses. It
never changes an existing anchor.

--release-metadata and --release-target opt in to the release root adopted at
/etc/orama/release-root.json. Before anything is extracted, the archive file
must be that target in the TUF metadata: the root signs timestamp, snapshot and
targets, the timestamp is unexpired, the snapshot is not older than the one
recorded in /etc/orama/release-seen.json, and the file has the target's length
and hashes. Any failure refuses the archive; the wallet check is not tried
instead. An archive that passes is then verified against the trust anchor as
above: the release root is required in addition to it, not in place of it.

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | The pushed archive on this node [required] |
| `--release-metadata` | — | Directory holding timestamp.json, snapshot.json and targets.json; requires --release-target |
| `--release-target` | — | Name the archive has in the release targets metadata; requires --release-metadata |
| `--trust-signers` | — | Create a missing trust anchor with these addresses (nodes installed before archive signing only) |

### orama node start

Start all production services (requires sudo)

```
orama node start
```

### orama node status

Show the service status of the node on this machine

```
orama node status
```

Report the systemd units of the Orama node installed on this machine.

For the health of your whole fleet from your own machine, use 'orama status'.

### orama node stop

Stop all production services (requires sudo)

```
orama node stop [flags]
```

Stop all Orama services in dependency order and disable auto-start.
Includes namespace services, global services, and supporting services.
Use --force to bypass quorum safety check.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Bypass quorum safety check |

### orama node uninstall

Remove production services (requires sudo)

```
orama node uninstall
```

### orama node unlock

Unlock an OramaOS genesis node

```
orama node unlock [flags]
```

Manually unlock a genesis OramaOS node that cannot reconstruct its LUKS key
via Shamir shares (not enough peers online).

This is only needed for the genesis node before enough peers have joined for
Shamir-based unlock. Once 5+ peers exist, the genesis node transitions to
normal Shamir unlock and this command is no longer needed.

The encrypted genesis key is written where the node was created, and the
OramaOS agent does not serve it, so --key-file is required. The command used to
try fetching it from the node first, on a path the agent has never served, and
spent ten seconds timing out before telling you to pass the flag.

Usage:
  orama node unlock --genesis --node-ip <wg-ip> --key-file <path>

The node must be reachable over WireGuard on port 9998.

| Flag | Default | Description |
|------|---------|-------------|
| `--genesis` | `false` | Confirm genesis node unlock |
| `--key-file` | — | Path to the encrypted genesis key file (required) |
| `--node-ip` | — | WireGuard IP of the OramaOS node (required) |

### orama node upgrade

Upgrade existing installation (requires sudo)

```
orama node upgrade [flags]
```

Upgrade the Orama node binary and optionally restart services.
Uses rolling restart with quorum safety to ensure zero downtime.

| Flag | Default | Description |
|------|---------|-------------|
| `--acme-ca` | — | ACME directory this node's TLS certificates come from, recorded in node.yaml: letsencrypt (production), letsencrypt-staging or an https URL (default: the recorded one) |
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment for remote rolling upgrade (devnet, testnet) |
| `--force` | `false` | Reconfigure all settings |
| `--nameserver` | `false` | Make this node a nameserver (uses saved preference if not specified) |
| `--node` | — | Upgrade a single node IP only |
| `--public-ip` | — | This node's public IP, recorded as node.public_ip (default: the recorded one, else the source address of the default route) |
| `--restart` | `false` | Automatically restart services after upgrade |
| `--skip-checks` | `false` | Skip minimum resource checks (RAM/CPU) |
| `--yes` | `false` | Execute the rolling upgrade plan (without it the plan is printed and nothing is restarted) |

### orama node wipe

Erase Orama from remote nodes (target-side only)

```
orama node wipe [flags]
```

Remove all Orama data, services and configuration from remote nodes.
Tor is left installed (its config and state are removed); --nuclear purges it.

Target-side only: this says nothing to the cluster. If the node is still a
member, use 'orama node remove' instead — otherwise the survivors keep
counting it toward quorum and re-adding its WireGuard peer.

This is a DESTRUCTIVE operation. Use --force to skip confirmation.

Examples:
  orama node wipe --env testnet                      # Wipe every node
  orama node wipe --env testnet --node 1.2.3.4       # Wipe one node
  orama node wipe --env testnet --nuclear             # Also remove shared binaries

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--force` | `false` | Skip confirmation (DESTRUCTIVE) |
| `--node` | — | Public IP of the node to wipe; omit to wipe every node in the environment |
| `--nuclear` | `false` | Also remove shared binaries (rqlited, ipfs, caddy, ...) and the Tor package |

### orama nodes

List your nodes across environments

```
orama nodes [flags]
```

List all nodes owned by your wallet. Queries the network API
with your stored credentials, falling back to nodes.conf.

Requires: orama auth login (for API-based resolution)

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Filter by environment (default: active environment) |

### orama operator

Operate the cluster

```
orama operator
```

Commands for the wallets on the cluster's operator list.

Every one of them needs the admin grant and a wallet on that list; a namespace's
own admin key is not enough.

Subcommands: `add`, `list`, `remove`, `rotate-secrets`, `rotate-signing-key`

### orama operator add

Let another wallet operate this cluster

```
orama operator add <wallet>
```

### orama operator list

List the wallets that operate this cluster

```
orama operator list
```

### orama operator remove

Take a wallet off this cluster's operator list

```
orama operator remove <wallet>
```

### orama operator rotate-secrets

Re-encrypt stored secrets, optionally under a new encryption root

```
orama operator rotate-secrets [flags]
```

Rewrite function secrets, push tokens, TURN secrets, deployment
environments and agent tokens onto the versioned envelope (enc:v1:<id>:).

Without --rotate the IKM does not change: leftover plaintext and the legacy
enc: form are rewritten so a captured snapshot of the old format is no longer
the live one, and Decrypt can fail closed.

With --rotate a new encryption root is generated. Existing ciphertext is
re-encrypted under it. A disk that holds only the previous root cannot open
the new rows. IPFS-Cluster and the mesh bearer are not touched.

Do not run this until every gateway is on a binary that can read enc:v1:.
The walker is idempotent; if it is interrupted, run it again.

| Flag | Default | Description |
|------|---------|-------------|
| `--rotate` | `false` | Generate a new encryption root and re-encrypt under it |

### orama operator rotate-signing-key

Replace the key this gateway signs tokens with

```
orama operator rotate-signing-key
```

Generate a new signing key for the gateway, publish it, and start signing
with it.

Nobody is signed out. The outgoing key keeps verifying the tokens it already
signed until they expire on their own, so both keys are accepted for one
access-token lifetime and then the old one stops.

The key used to be derived from the cluster secret, which meant there was
nothing to rotate to: changing it meant changing the cluster secret, which
invalidates every token in the cluster at once.

### orama push

Push the binary archive to your nodes

```
orama push [flags]
```

Upload the pre-built binary archive to nodes and extract it.

By default the archive is uploaded once to a hub node, which then distributes
it to the others server-to-server. Use --direct to upload from this machine to
each node in turn.

'orama push' and 'orama node push' are the same command.

--archive names the build: the path 'orama build' printed. There is no
default — the newest archive in /tmp may be another checkout's build.

Examples:
  orama push --env devnet --archive /tmp/orama-0.200.0-linux-amd64.tar.gz
  orama push --env devnet --archive <path> --direct    # Upload to each node in turn
  orama push --env devnet --archive <path> --node 1.2.3.4
  orama push --host 1.2.3.4 --archive <path>           # A node not in the inventory yet
  orama push --env devnet --archive <path> --trust-signers 0xYourWallet  # Nodes from before archive signing

Each node verifies the archive with its installed orama before anything under
/opt/orama changes: the manifest signature must recover to an address in the
node's /etc/orama/archive-signers and every file must match the manifest.

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | The build archive to push (the path `orama build` printed) [required] |
| `--direct` | `false` | Upload from here to each node in turn, instead of fanning out |
| `--env` | — | Target environment (default: active) |
| `--host` | — | Push to a node that is not in the inventory yet |
| `--node` | — | Push to a single node IP from the inventory |
| `--trust-signers` | — | Create the archive trust anchor on nodes that have none (installed before archive signing); never changes an existing one |
| `--user` | — | SSH user for --host (default: root) |

### orama rollout

Build, push, and rolling upgrade every node in an environment

```
orama rollout [flags]
```

Full deployment pipeline: build the binary archive, push it to every node,
then upgrade them one at a time.

The rolling upgrade prints its plan — which node holds the raft leadership and
the order the restarts happen in — and stops unless --yes is given.

'orama rollout' and 'orama node rollout' are the same command.

Examples:
  orama rollout --env testnet             # Build, push, then print the plan
  orama rollout --env testnet --yes       # Execute the plan
  orama rollout --env testnet --no-build  # Reuse the existing archive

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | With --no-build: the build archive to roll out |
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--no-build` | `false` | Skip the build step; roll out the archive named by --archive |
| `--yes` | `false` | Execute the rollout plan instead of only printing it |

### orama sandbox

Manage ephemeral Hetzner Cloud clusters for testing

```
orama sandbox
```

Spin up temporary 5-node Orama clusters on Hetzner Cloud for development and testing.

Setup (one-time):
  orama sandbox setup

Usage:
  orama sandbox create [--name <name>] [--archive <path>]
                                           Create a new 5-node cluster
  orama sandbox destroy [--name <name>]    Tear down a cluster
  orama sandbox list                       List active sandboxes
  orama sandbox status [--name <name>]     Show cluster health
  orama sandbox rollout [--name <name>] [--archive <path>]
                                           Build + push + rolling upgrade
  orama sandbox ssh <node-number>          SSH into a sandbox node (1-5)
  orama sandbox reset                      Delete all infra and config to start fresh

The archive (--archive, or this checkout built now) must be signed by the
RootWallet account that is unlocked: it is the only signer a sandbox trusts.
Create and rollout install it the way 'orama node setup' and 'orama push' do.

Subcommands: `create`, `destroy`, `list`, `reset`, `rollout`, `setup`, `ssh`, `status`

### orama sandbox create

Create a new 5-node sandbox cluster (~5 min)

```
orama sandbox create [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | Build archive to deploy (default: build this checkout now) |
| `--name` | — | Sandbox name (random if not specified) |

### orama sandbox destroy

Destroy a sandbox cluster and release resources

```
orama sandbox destroy [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Skip confirmation |
| `--name` | — | Sandbox name (uses active if not specified) |

### orama sandbox list

List active sandbox clusters

```
orama sandbox list
```

### orama sandbox reset

Delete all sandbox infrastructure and config to start fresh

```
orama sandbox reset
```

Deletes floating IPs, firewall, and SSH key from Hetzner Cloud,
then removes the local config (~/.orama/sandbox.yaml) and SSH keys.

Use this when you need to switch datacenter locations (floating IPs are
location-bound) or to completely start over with sandbox setup.

### orama sandbox rollout

Build + push + rolling upgrade to sandbox cluster

```
orama sandbox rollout [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--archive` | — | Build archive to roll out (default: build this checkout now) |
| `--name` | — | Sandbox name (uses active if not specified) |

### orama sandbox setup

Interactive setup: Hetzner API key, domain, floating IPs, SSH key

```
orama sandbox setup
```

### orama sandbox ssh

SSH into a sandbox node (1-5)

```
orama sandbox ssh <node-number> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | — | Sandbox name (uses active if not specified) |

### orama sandbox status

Show cluster health report

```
orama sandbox status [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | — | Sandbox name (uses active if not specified) |

### orama ssh

SSH into a node

```
orama ssh <ip-or-hostname> [-- command] [flags]
```

SSH into a node by IP address or hostname.
Resolves the SSH key from rootwallet automatically.

The node's host key must already be pinned in ~/.orama/known_hosts, where
'orama node setup' writes it. A host with no pinned key is refused, never
trusted on first use, and a key that differs from the pinned one is refused.

Pass a command after the IP to run it non-interactively:
  orama ssh 1.2.3.4 'sudo systemctl status orama-node'

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment to search (default: active) |

### orama status

Show health status of your nodes

```
orama status [flags]
```

Check the health of all your nodes in an environment.

A node is healthy when its gateway answers and its RQLite has settled into
Leader or Follower. The data comes from the gateway's operator telemetry API;
--ssh reads every node over SSH instead, for when no gateway answers. For the
numbers behind the verdict use 'orama monitor cluster'; for the state of a
single machine you are logged into, 'orama node status'.

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment (default: active) |
| `--ssh` | `false` | Collect over SSH from every node instead of the gateway API (break-glass) |

### orama storage

Storage deals on the Orama chain

```
orama storage
```

Subcommands: `accept`, `create`, `decline`, `extend`, `get`, `grant`, `open`, `prove`, `put`, `revoke`, `rewrap`, `seal`

### orama storage accept

Accept an assigned storage slot

```
orama storage accept [flags]
```

Accept one slot of a deal. The signer is the node's hot key. Without --node the command prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--deal-id` | `0` | Deal id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Signing account (orama1...) [required] |
| `--slot` | `0` | Slot index |

### orama storage create

Open a private or public-pin storage deal

```
orama storage create [flags]
```

Open a PRIVATE or PUBLIC_PIN deal.

The command does not encrypt the bytes and does not upload them. Each --piece
is a 32-byte root and a byte count, written as <64 hex chars>:<bytes>. Leaf
counts follow the 1024-byte piece rule. The root is not checked against the
bytes. A private deal needs one piece per replica. A public-pin deal needs
exactly one piece. Archive deals are refused. Without --node the command
prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--class` | — | private or public-pin [required] |
| `--duration-epochs` | `0` | Deal length in epochs [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--granter` | — | Account whose deal allowance pays, when the signer is the grantee |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--nonce` | — | 32-byte deal nonce hex [required] |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--piece` | — | Piece as <64-hex-root>:<bytes> [required] |
| `--price` | — | Price per epoch per replica, in norama [required] |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--repair-delegate` | — | Repair delegate id |
| `--replicas` | `3` | Replica count |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Signing account (orama1...) [required] |

### orama storage decline

Decline an assigned storage slot

```
orama storage decline [flags]
```

Decline one slot of a deal. The signer is the node's hot key. Without --node the command prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--deal-id` | `0` | Deal id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--reason` | — | Why the slot is declined |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Signing account (orama1...) [required] |
| `--slot` | `0` | Slot index |

### orama storage extend

Add epochs to a storage deal

```
orama storage extend [flags]
```

Add epochs to a user deal. Without --node the command prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--deal-id` | `0` | Deal id [required] |
| `--extra-epochs` | `0` | Epochs to add [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Signing account (orama1...) [required] |

### orama storage get

Fetch and open a private file from its providers

```
orama storage get [flags]
```

Fetch the first slot of a deal that a provider serves with the on-chain
piece root, strip its slot layer, and decrypt it. A wrong storage key or repair seed
fails and writes nothing.

| Flag | Default | Description |
|------|---------|-------------|
| `--deal-id` | `0` | Deal id |
| `--out` | — | Plaintext output file |
| `--repair-seed-file` | — | File holding the repair seed, hex, at least 32 bytes, mode 0600 |
| `--rpc` | — | oramad CometBFT RPC, for example http://127.0.0.1:31001 |
| `--storage-key-file` | — | File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600 |

### orama storage grant

Grant a cluster a capped deal allowance

```
orama storage grant [flags]
```

Grant a deal allowance to another account.

The grant is not SDK authz. It caps spend, piece size, duration, and replica
count. Without --node the command prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--expiry-epoch` | `0` | Epoch after which the grant is dead |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--grantee` | — | Grantee account (orama1...) [required] |
| `--max-duration-epochs` | `0` | Longest deal the grant allows [required] |
| `--max-piece-bytes` | `0` | Largest piece the grant allows [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--period-epochs` | `0` | Epochs in one spend period |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--replicas` | `3` | Exact replica count a deal must use |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Granter account (orama1...) [required] |
| `--spend-limit` | — | Spend limit in norama [required] |

### orama storage open

Open one sealed storage slot

```
orama storage open [flags]
```

Open one slot file written by seal.

A wrong storage key, repair seed, or slot fails and writes nothing.

| Flag | Default | Description |
|------|---------|-------------|
| `--in` | — | Sealed slot file |
| `--nonce` | — | Deal nonce, 32 bytes hex |
| `--out` | — | Plaintext output file |
| `--repair-seed-file` | — | File holding the repair seed, hex, at least 32 bytes, mode 0600 |
| `--slot` | `0` | Slot index |
| `--storage-key-file` | — | File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600 |

### orama storage prove

Submit storage challenge proofs

```
orama storage prove [flags]
```

Submit one or more challenge proofs for a storage node.

--file is a JSON array. Each object has deal_id, slot, leaf_index, leaf,
and siblings. leaf and siblings are hex. The leaf is 1024 bytes and each
sibling is 32 bytes. The command does not choose the challenged leaf and
does not read the stored piece. Without --node it prints the sign document
and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--file` | — | JSON file of proofs [required] |
| `--gas` | `0` | Gas limit [required] |
| `--id` | — | Node id [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Hot key account (orama1...) [required] |

### orama storage put

Upload sealed slots to the providers a deal assigned

```
orama storage put [flags]
```

Upload the slot-N files written by seal to the providers the chain assigned.

The deal must already exist (orama storage create, with the roots seal printed).
Every file's piece root is checked against its slot on chain before any byte
is sent, so a wrong file or a wrong deal uploads nothing. The command waits
for each slot to be assigned and for its provider to accept the root. The
provider endpoint is the node's first http(s) endpoint in x/nodes.

| Flag | Default | Description |
|------|---------|-------------|
| `--deal-id` | `0` | Deal id |
| `--dir` | — | Directory holding slot-N files from seal |
| `--rpc` | — | oramad CometBFT RPC, for example http://127.0.0.1:31001 |
| `--wait` | `5m0s` | How long to wait for assignment and acceptance |

### orama storage revoke

Revoke a deal allowance

```
orama storage revoke [flags]
```

Revoke a deal allowance. Without --node the command prints the sign document and does not submit it.

| Flag | Default | Description |
|------|---------|-------------|
| `--account-number` | `0` | Account number, when not read from --node |
| `--chain-id` | — | Chain id [required] |
| `--fee` | — | Fee in norama [required] |
| `--gas` | `0` | Gas limit [required] |
| `--grantee` | — | Grantee account (orama1...) [required] |
| `--node` | — | Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it |
| `--onion-network` | — | Start a Tor client for this Orama Tor network file (network.json) and submit through it; without --onion a validator onion from the file is picked at random ($ORAMA_ONION_NETWORK) |
| `--onion-socks` | — | Tor SOCKS5 address for --onion, a loopback host:port (default 127.0.0.1:9050, $ORAMA_ONION_SOCKS) |
| `--onion-tor` | `tor` | The tor binary --onion-network starts |
| `--onion` | — | Submit through this validator onion service (addr.onion[:port]) over Tor instead of --node; never falls back to the clearnet ($ORAMA_CHAIN_ONION) |
| `--pubkey` | — | Compressed secp256k1 pubkey hex of the signing account |
| `--sequence` | `0` | Account sequence, when not read from --node |
| `--signer` | — | Granter account (orama1...) [required] |

### orama storage rewrap

Rebuild one storage slot from another slot's ciphertext

```
orama storage rewrap [flags]
```

Turn one sealed slot into another slot of the same deal.

The command uses the repair seed only. It does not recover the plaintext
and it does not upload the result.

| Flag | Default | Description |
|------|---------|-------------|
| `--from` | `0` | Slot the input file belongs to |
| `--in` | — | Source slot file |
| `--nonce` | — | Deal nonce, 32 bytes hex |
| `--out` | — | Destination slot file |
| `--repair-seed-file` | — | File holding the repair seed, hex, at least 32 bytes, mode 0600 |
| `--to` | `0` | Slot to write |

### orama storage seal

Seal a file into one ciphertext per storage slot

```
orama storage seal [flags]
```

Seal a private file before a storage deal.

The file key is wrapped under the owner's orama-storage-v1 key from RootWallet. Each slot gets a different
ciphertext. The command writes slot-N files and prints each piece root.
It does not upload the bytes and it does not submit a deal.

| Flag | Default | Description |
|------|---------|-------------|
| `--in` | — | Plaintext file |
| `--nonce` | — | Deal nonce, 32 bytes hex |
| `--out-dir` | — | Directory for slot-N files |
| `--repair-seed-file` | — | File holding the repair seed, hex, at least 32 bytes, mode 0600 |
| `--replicas` | `3` | Number of slots, 1 to 32 |
| `--storage-key-file` | — | File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600 |

### orama version

Show version information

```
orama version
```

### orama vpn

Route traffic through an Orama Tor network

```
orama vpn
```

Join an Orama Tor network from this machine.

A network is described by a network.json file: its directory authorities, a
fallback list and, optionally, validator onion services. up starts an unmodified
upstream tor on it and offers a SOCKS5 proxy on loopback; check joins the
network and proves a circuit reaches a validator's onion service.

The client has one route: the tor it starts, configured with the network's
authorities and no others. It never falls back to the public Tor network or to
a direct connection. When tor stops, the proxy port closes and whatever was
using it fails; nothing is routed around it. This is a proxy, not a system-wide
tunnel: only applications pointed at the SOCKS port, with names resolved by the
proxy (socks5h), use the network.

Only a private network can be joined: the public Orama network is not launched.

Subcommands: `check`, `up`

### orama vpn check

Join an Orama Tor network and reach a validator onion service through it

```
orama vpn check [flags]
```

Start tor on the network (stopped again when the check ends) and request the status
route of a validator onion service through the proxy, over a fresh circuit each.

By default every validator onion service the network file lists is tried, and the check
passes when at least one answers; --onion tries only the one given. It fails when tor
cannot bootstrap on the network's authorities, when none of the onion services answers, and
when the network file lists none and no --onion is given. Nothing is tried outside the
network.

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | — | Tor state directory (default: the user cache directory, per network) |
| `--network` | — | Orama Tor network file (network.json) [required] ($ORAMA_ONION_NETWORK) |
| `--onion` | — | Check only this validator onion service (addr.onion[:port]) |
| `--tor` | `tor` | The tor binary to run |

### orama vpn up

Run a SOCKS5 proxy into an Orama Tor network

```
orama vpn up [flags]
```

Start tor on the network and keep it running until interrupted.

The SOCKS5 proxy listens on loopback only (--socks). Point an application at it
as socks5h, so the proxy resolves names, and each distinct SOCKS username gets
its own circuit. --dns also offers a DNS resolver on loopback that answers
through the network.

If tor stops, up exits with an error and the proxy port closes; applications
using it fail instead of connecting some other way.

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | — | Tor state directory (default: the user cache directory, per network) |
| `--dns` | — | Loopback address for a DNS resolver that answers through the network (off by default) |
| `--network` | — | Orama Tor network file (network.json) [required] ($ORAMA_ONION_NETWORK) |
| `--socks` | `127.0.0.1:9150` | Loopback address for the SOCKS5 proxy |
| `--tor` | `tor` | The tor binary to run |

