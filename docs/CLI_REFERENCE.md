<!--
Generated from the cobra command tree by core/cmd/cli/reference_test.go.
Do not edit by hand: run `make -C core docs`.
-->

# CLI reference

Every command the `orama` binary defines, with its flags. Generated from the
command tree, so it cannot drift from the code: a test fails when this file and
the tree disagree.

Task-shaped documentation lives elsewhere — [deploying apps](DEPLOYMENT_GUIDE.md),
[building and rolling out](DEV_DEPLOY.md), [functions](SERVERLESS.md). This page
is the index.

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
  - [`orama auth list`](#orama-auth-list) — List all stored credentials
  - [`orama auth login`](#orama-auth-login) — Authenticate with wallet
  - [`orama auth logout`](#orama-auth-logout) — Clear stored credentials
  - [`orama auth status`](#orama-auth-status) — Show detailed authentication info
  - [`orama auth switch`](#orama-auth-switch) — Switch between stored credentials
  - [`orama auth whoami`](#orama-auth-whoami) — Show current authentication status
- [`orama build`](#orama-build) — Build pre-compiled binary archive for deployment
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
- [`orama inspect`](#orama-inspect) — Inspect cluster health via SSH
- [`orama invite`](#orama-invite) — Mint an invite for a new node
- [`orama members`](#orama-members) — Manage who may work in a namespace
  - [`orama members add`](#orama-members-add) — Give a wallet a role in this namespace
  - [`orama members list`](#orama-members-list) — List who holds a grant in this namespace
  - [`orama members remove`](#orama-members-remove) — Take a wallet's grant away
  - [`orama members transfer`](#orama-members-transfer) — Hand this namespace to another wallet
- [`orama monitor`](#orama-monitor) — Monitor cluster health from your local machine
  - [`orama monitor alerts`](#orama-monitor-alerts) — Active alerts and warnings (one-shot)
  - [`orama monitor cluster`](#orama-monitor-cluster) — Cluster overview (one-shot)
  - [`orama monitor dns`](#orama-monitor-dns) — DNS health overview (one-shot)
  - [`orama monitor live`](#orama-monitor-live) — Interactive TUI monitor
  - [`orama monitor mesh`](#orama-monitor-mesh) — Mesh connectivity status (one-shot)
  - [`orama monitor namespaces`](#orama-monitor-namespaces) — Namespace usage summary (one-shot)
  - [`orama monitor node`](#orama-monitor-node) — Per-node health details (one-shot)
  - [`orama monitor report`](#orama-monitor-report) — Full cluster report (JSON)
  - [`orama monitor service`](#orama-monitor-service) — Service status across the cluster (one-shot)
- [`orama namespace`](#orama-namespace) — Manage namespaces
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
  - [`orama namespace rqlite`](#orama-namespace-rqlite) — Manage the namespace's internal RQLite database
    - [`orama namespace rqlite export`](#orama-namespace-rqlite-export) — Export the namespace's RQLite database to a local SQLite file
    - [`orama namespace rqlite import`](#orama-namespace-rqlite-import) — Import a SQLite dump into the namespace's RQLite (DESTRUCTIVE)
  - [`orama namespace webrtc-status`](#orama-namespace-webrtc-status) — Show WebRTC service status for a namespace
- [`orama node`](#orama-node) — Node operator commands
  - [`orama node clean`](#orama-node-clean) — Deprecated: use 'orama node wipe' or 'orama node decommission'
  - [`orama node doctor`](#orama-node-doctor) — Diagnose common node issues
  - [`orama node enroll`](#orama-node-enroll) — Enroll an OramaOS node into the cluster
  - [`orama node install`](#orama-node-install) — Install production node (requires sudo)
  - [`orama node invite`](#orama-node-invite) — Manage invite tokens for joining the cluster
  - [`orama node list`](#orama-node-list) — List your nodes across environments
  - [`orama node logs`](#orama-node-logs) — View production service logs
  - [`orama node migrate`](#orama-node-migrate) — Migrate from old unified setup (requires sudo)
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
  - [`orama node start`](#orama-node-start) — Start all production services (requires sudo)
  - [`orama node status`](#orama-node-status) — Show the service status of the node on this machine
  - [`orama node stop`](#orama-node-stop) — Stop all production services (requires sudo)
  - [`orama node uninstall`](#orama-node-uninstall) — Remove production services (requires sudo)
  - [`orama node unlock`](#orama-node-unlock) — Unlock an OramaOS genesis node
  - [`orama node upgrade`](#orama-node-upgrade) — Upgrade existing installation (requires sudo)
  - [`orama node wipe`](#orama-node-wipe) — Erase Orama from remote nodes (target-side only)
- [`orama nodes`](#orama-nodes) — List your nodes across environments
- [`orama operator`](#orama-operator) — Operate the cluster
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
- [`orama version`](#orama-version) — Show version information

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

Setting or removing a variable restarts the app so it picks up the change.

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

Actions: auth.challenge, auth.verify, auth.refresh, auth.refresh.replay, auth.logout, key.issue, key.revoke, key.rotate, key.revoke_all, namespace.create, namespace.delete, secret.set, secret.delete, function.deploy, function.delete, deployment.deploy, deployment.delete, operator.action, auth.legacy_credential, grant.add, grant.revoke, namespace.transfer

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
Authentication is a RootWallet (rw) signature over a gateway challenge.

Subcommands: `list`, `login`, `logout`, `status`, `switch`, `whoami`

### orama auth list

List all stored credentials

```
orama auth list
```

### orama auth login

Authenticate with wallet

```
orama auth login [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | — | Namespace name |

### orama auth logout

Clear stored credentials

```
orama auth logout
```

### orama auth status

Show detailed authentication info

```
orama auth status
```

### orama auth switch

Switch between stored credentials

```
orama auth switch
```

### orama auth whoami

Show current authentication status

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
  - manifest.json with checksums

The resulting archive can be pushed to nodes with 'orama node push'.

| Flag | Default | Description |
|------|---------|-------------|
| `--arch` | `amd64` | Target architecture (amd64, arm64) |
| `--output` | — | Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz) |
| `--sign` | `false` | Sign the manifest with rootwallet (requires rw in PATH) |
| `--verbose` | `false` | Verbose output |

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
orama env add <name> <gateway_url> [description]
```

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

Scaffolds a new directory with function.go and function.yaml templates.

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

### orama inspect

Inspect cluster health via SSH

```
orama inspect [flags]
```

SSH into cluster nodes and run health checks.
Supports AI-powered failure analysis and result export.

| Flag | Default | Description |
|------|---------|-------------|
| `--ai` | `false` | Enable AI analysis of failures |
| `--api-key` | — | OpenRouter API key (or OPENROUTER_API_KEY env) |
| `--config` | — | Read nodes from this file instead of resolving them |
| `--env` | — | Environment to inspect (devnet, testnet) |
| `--format` | `table` | Output format (table, json) |
| `--model` | `moonshotai/kimi-k2.5` | OpenRouter model for AI analysis |
| `--output` | — | Save results to directory as markdown (e.g., ./results) |
| `--subsystem` | `all` | Subsystem to inspect (rqlite,olric,ipfs,dns,wg,system,network,anyone,all) |
| `--timeout` | `30s` | SSH command timeout |
| `--verbose` | `false` | Verbose output |

### orama invite

Mint an invite for a new node

```
orama invite [flags]
```

Create a single-use invite that lets a new node join the cluster.

The invite carries the gateway to join and the fingerprint of its TLS
certificate, so the joining node pins the cluster it was actually invited to
rather than trusting whatever certificate it is first shown. There is nothing
else to copy across.

This is the same token as 'orama node invite', which does the same thing from
an existing node instead of from here.

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment to invite into (default: active) |
| `--expiry` | `1h0m0s` | How long the invite stays usable |

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
| `--resource` | — | Narrow the role to a resource, e.g. storage:avatars/* — RECORDED BUT NOT ENFORCED YET, so a grant carrying one authorises nothing |
| `--role` | — | Role to grant (reader, runtime, admin) |

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
a grant: a namespace with no owner is claimable by whoever signs in to it next.
You keep an admin grant, so handing a project over does not lock you out of it.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Skip confirmation prompt |
| `--namespace` | — | Namespace name |

### orama monitor

Monitor cluster health from your local machine

```
orama monitor [flags]
```

SSH into cluster nodes and display real-time health data.
Runs 'orama node report --json' on each node and aggregates results.

Without a subcommand, launches the interactive TUI.

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | — | Read nodes from this file instead of resolving them |
| `--env` | — | Environment: devnet, testnet, mainnet (required) |
| `--node` | — | Filter to specific node host/IP |

Subcommands: `alerts`, `cluster`, `dns`, `live`, `mesh`, `namespaces`, `node`, `report`, `service`

### orama monitor alerts

Active alerts and warnings (one-shot)

```
orama monitor alerts
```

### orama monitor cluster

Cluster overview (one-shot)

```
orama monitor cluster
```

### orama monitor dns

DNS health overview (one-shot)

```
orama monitor dns
```

### orama monitor live

Interactive TUI monitor

```
orama monitor live
```

### orama monitor mesh

Mesh connectivity status (one-shot)

```
orama monitor mesh
```

### orama monitor namespaces

Namespace usage summary (one-shot)

```
orama monitor namespaces
```

### orama monitor node

Per-node health details (one-shot)

```
orama monitor node
```

### orama monitor report

Full cluster report (JSON)

```
orama monitor report
```

### orama monitor service

Service status across the cluster (one-shot)

```
orama monitor service
```

### orama namespace

Manage namespaces

```
orama namespace
```

Aliases: `ns`

List, delete, and repair namespaces on the Orama network.

Subcommands: `create`, `delete`, `disable`, `enable`, `keys`, `list`, `repair`, `rqlite`, `webrtc-status`

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

### orama namespace rqlite

Manage the namespace's internal RQLite database

```
orama namespace rqlite
```

Export and import the namespace's internal RQLite database (stores deployments, DNS records, API keys, etc.).

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
(deployments, DNS records, API keys, etc.) will be replaced with the imported file.

| Flag | Default | Description |
|------|---------|-------------|
| `-i`, `--input` | — | Input SQLite file path |

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
  report, invite, unlock, schema, migrate, migrate-raft-id, migrate-conf

Remote, run from your machine and reaching nodes over SSH:
  list, setup, enroll, push, rollout, clean, remove, wipe, recover-raft

The remote commands are the same implementations as the top-level 'orama push',
'orama rollout' and 'orama nodes'.

Subcommands: `clean`, `doctor`, `enroll`, `install`, `invite`, `list`, `logs`, `migrate-conf`, `migrate-raft-id`, `migrate`, `push`, `recover-raft`, `remove`, `report`, `restart`, `rollout`, `schema`, `setup`, `start`, `status`, `stop`, `uninstall`, `unlock`, `upgrade`, `wipe`

### orama node clean

Deprecated: use 'orama node wipe' or 'orama node decommission'

```
orama node clean [flags]
```

DEPRECATED. Use 'orama node wipe' or 'orama node decommission'.

'clean' only ever erased the target. It said nothing to the rest of the cluster,
so a cleaned node stayed a configured raft voter counted toward quorum, kept its
wireguard_peers row re-applied to every survivor's interface, and kept its
dns_nodes row. It also stopped only the legacy host unit names, leaving tenant
'orama-namespace-*@*' units running under a deleted data directory.

  orama node wipe           erases a node (what clean did, fixed)
  orama node decommission   removes one node from the cluster, then erases it

This command now runs 'wipe'.

Examples:
  orama node wipe --env testnet --node 1.2.3.4
  orama node decommission --env testnet --node 1.2.3.4

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--force` | `false` | Skip confirmation (DESTRUCTIVE) |
| `--node` | — | Public IP of the node to wipe; omit to wipe every node in the environment |
| `--nuclear` | `false` | Also remove shared binaries (rqlited, ipfs, caddy, ...) |

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

The OramaOS node displays a registration code on port 9999. Provide this code
along with an invite token to complete enrollment. The Gateway pushes cluster
configuration (WireGuard, secrets, peer list) to the node.

Usage:
  orama node enroll --node-ip <ip> --code <code> --token <invite-token> --env <environment>

The node must be reachable over the public internet on port 9999 (enrollment only).
After enrollment, port 9999 is permanently closed and all communication goes over WireGuard.

| Flag | Default | Description |
|------|---------|-------------|
| `--code` | — | Registration code from the node (auto-fetched if not provided) |
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

| Flag | Default | Description |
|------|---------|-------------|
| `--anyone-client` | `false` | Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay) |
| `--base-domain` | — | Base domain for deployment routing (e.g., dbrs.space) |
| `--ca-fingerprint` | — | SHA-256 fingerprint of the gateway's TLS cert; the invite carries this, so it is only needed to override it |
| `--domain` | — | Domain for HTTPS (auto-generated for non-nameserver nodes if omitted) |
| `--dry-run` | `false` | Show what would be done without making changes |
| `--environment` | — | Environment name (devnet, testnet, etc.) |
| `--force` | `false` | Force reconfiguration even if already installed |
| `--ipfs-addrs` | — | Comma-separated multiaddrs of existing IPFS node |
| `--ipfs-cluster-addrs` | — | Comma-separated multiaddrs of existing IPFS Cluster node |
| `--ipfs-cluster-peer` | — | Peer ID of existing IPFS Cluster node |
| `--ipfs-peer` | — | Peer ID of existing IPFS node to peer with |
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

### orama node migrate

Migrate from old unified setup (requires sudo)

```
orama node migrate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Show what would be migrated without making changes |

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

Examples:
  orama push --env devnet             # Fan out across the devnet nodes
  orama push --env devnet --direct    # Upload to each node in turn
  orama push --env devnet --node 1.2.3.4
  orama push --host 1.2.3.4           # A node that is not in the inventory yet

| Flag | Default | Description |
|------|---------|-------------|
| `--direct` | `false` | Upload from here to each node in turn, instead of fanning out |
| `--env` | — | Target environment (default: active) |
| `--host` | — | Push to a node that is not in the inventory yet |
| `--node` | — | Push to a single node IP from the inventory |
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
  2. Reset the kept node to a single-member cluster, preserving its data
  3. Start it and confirm it comes back as Leader with its data intact
  4. Delete raft.db, raft/, db.sqlite (+shm/wal) and rsnapshots on every other
     node
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
| `--nuclear` | `false` | When wiping, also remove shared binaries |
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
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--no-build` | `false` | Skip the build step and reuse the existing archive |
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
| `--dsn` | — | RQLite DSN (default: discover from node config or http://localhost:10100) |

Subcommands: `apply`, `status`

### orama node schema apply

Apply pending migrations to the local RQLite

```
orama node schema apply [flags]
```

Apply every embedded migration not yet recorded in schema_migrations.

ALTER TABLE statements that target an already-existing column are tolerated
(the migration is marked complete). Other errors abort the run with the
schema in a partially-applied state — re-running is safe because each
migration is independently versioned.

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
  # Genesis node (first node, creates new cluster)
  orama node setup --ip 1.2.3.4 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network --role nameserver --genesis

  # Join existing cluster
  orama node setup --ip 5.6.7.8 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network

  # Join as nameserver
  orama node setup --ip 9.10.11.12 --password 'vps-pass' --env devnet \
    --base-domain orama-devnet.network --role nameserver

| Flag | Default | Description |
|------|---------|-------------|
| `--base-domain` | — | Base domain for the network |
| `--env` | — | Target environment (default: active) |
| `--gateway` | — | Gateway URL for invite tokens (e.g., http://1.2.3.4) |
| `--genesis` | `false` | Create a new cluster (first node) |
| `--host-key` | — | Expected SSH host-key fingerprint (SHA256:...) of the VPS; omit to confirm it interactively |
| `--ip` | — | Public IP address of the VPS (required) |
| `--password` | — | One-time password for initial SSH access |
| `--role` | `node` | Node role: node or nameserver |
| `--user` | `root` | SSH user on the VPS |

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
| `--anyone-client` | `false` | Install Anyone as client-only (SOCKS5 proxy on port 9050, no relay) |
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment for remote rolling upgrade (devnet, testnet) |
| `--force` | `false` | Reconfigure all settings |
| `--nameserver` | `false` | Make this node a nameserver (uses saved preference if not specified) |
| `--node` | — | Upgrade a single node IP only |
| `--restart` | `false` | Automatically restart services after upgrade |
| `--skip-checks` | `false` | Skip minimum resource checks (RAM/CPU) |
| `--yes` | `false` | Execute the rolling upgrade plan (without it the plan is printed and nothing is restarted) |

### orama node wipe

Erase Orama from remote nodes (target-side only)

```
orama node wipe [flags]
```

Remove all Orama data, services and configuration from remote nodes.
Anyone relay keys at /var/lib/anon/ are preserved.

Target-side only: this says nothing to the cluster. If the node is still a
member, use 'orama node decommission' instead — otherwise the survivors keep
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
| `--nuclear` | `false` | Also remove shared binaries (rqlited, ipfs, caddy, ...) |

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

Subcommands: `rotate-signing-key`

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

Examples:
  orama push --env devnet             # Fan out across the devnet nodes
  orama push --env devnet --direct    # Upload to each node in turn
  orama push --env devnet --node 1.2.3.4
  orama push --host 1.2.3.4           # A node that is not in the inventory yet

| Flag | Default | Description |
|------|---------|-------------|
| `--direct` | `false` | Upload from here to each node in turn, instead of fanning out |
| `--env` | — | Target environment (default: active) |
| `--host` | — | Push to a node that is not in the inventory yet |
| `--node` | — | Push to a single node IP from the inventory |
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
| `--delay` | `300` | Seconds a node has to rejoin the cluster after its upgrade before the rollout stops |
| `--env` | — | Target environment (devnet, testnet) [required] |
| `--no-build` | `false` | Skip the build step and reuse the existing archive |
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
  orama sandbox create [--name <name>]     Create a new 5-node cluster
  orama sandbox destroy [--name <name>]    Tear down a cluster
  orama sandbox list                       List active sandboxes
  orama sandbox status [--name <name>]     Show cluster health
  orama sandbox rollout [--name <name>]    Build + push + rolling upgrade
  orama sandbox ssh <node-number>          SSH into a sandbox node (1-5)
  orama sandbox reset                      Delete all infra and config to start fresh

Subcommands: `create`, `destroy`, `list`, `reset`, `rollout`, `setup`, `ssh`, `status`

### orama sandbox create

Create a new 5-node sandbox cluster (~5 min)

```
orama sandbox create [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
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
| `--anyone-client` | `false` | Enable Anyone client (SOCKS5 proxy) on all nodes |
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
Leader or Follower. For the numbers behind the verdict use 'orama monitor
cluster'; for the state of a single machine you are logged into, 'orama node
status'.

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | — | Environment (default: active) |

### orama version

Show version information

```
orama version
```

