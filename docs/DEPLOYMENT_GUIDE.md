# Orama Network Deployment Guide

Complete guide for deploying applications and managing databases on Orama Network.

## Table of Contents

- [Overview](#overview)
- [Authentication](#authentication)
- [Deploying Static Sites (React, Vue, etc.)](#deploying-static-sites)
- [Deploying Next.js Applications](#deploying-nextjs-applications)
- [Deploying Go Backends](#deploying-go-backends)
- [Deploying Node.js Backends](#deploying-nodejs-backends)
- [Managing SQLite Databases](#managing-sqlite-databases)
- [Environment Variables](#environment-variables)
- [How Domains Work](#how-domains-work)
- [Full-Stack Application Example](#full-stack-application-example)
- [Managing Deployments](#managing-deployments)
- [Troubleshooting](#troubleshooting)

---

## Overview

Orama Network provides a decentralized platform for deploying web applications and managing databases. Each deployment:

- **Gets a unique domain** automatically (e.g., `myapp.orama.network`)
- **Isolated per namespace** - your data and apps are completely separate from others
- **Served from IPFS** (static) or **runs as a process** (dynamic apps)
- **Fully managed** - automatic health checks, restarts, and logging

### Supported Deployment Types

| Type | Description | Use Case | Domain Example |
|------|-------------|----------|----------------|
| **Static** | HTML/CSS/JS files served from IPFS | React, Vue, Angular, plain HTML | `myapp.orama.network` |
| **Next.js** | Next.js with SSR support | Full-stack Next.js apps | `myapp.orama.network` |
| **Go** | Compiled Go binaries | REST APIs, microservices | `api.orama.network` |
| **Node.js** | Node.js applications | Express APIs, TypeScript backends | `backend.orama.network` |

---

## Authentication

Before deploying, authenticate with your wallet:

```bash
# Authenticate with your wallet
orama auth login

# Who am I, and what may I do? (asks the gateway)
orama auth whoami

# What is stored on this machine? (asks nobody)
orama auth status
```

`orama auth login` asks which namespace to sign in to; blank signs in without
one. With no terminal to ask — a script, a pipe, CI — it does not prompt and
signs in without one; pass `--namespace <name>` to choose.

What is stored is a **session**, not a key: an access token lasting 15 minutes,
renewed transparently from a 30-day refresh token. The CLI used to store an API
key and send it as the credential of every request it made.

On a machine with RootWallet running, the challenge is signed here. On one
without — a server reached over SSH, a container, CI — `orama auth login` prints
a code instead:

```
    Your code:  BCDF-GHJK

  On a machine where RootWallet is running, run:

    orama auth approve BCDF-GHJK
```

Approving costs the same wallet signature signing in does, which is what makes
the code on its own worthless. `orama auth approve <code> --deny` refuses, and
the waiting machine stops rather than polling until the code expires.

### In CI

Set `ORAMA_TOKEN` to either an API key or a token. A key is exchanged for a
session once per run rather than sent on every request the run makes:

```bash
export ORAMA_TOKEN="$(orama namespace keys create --scope app-runtime --label ci)"
```

### Which machines are signed in

```bash
orama auth sessions                  # every live session for this wallet
orama auth sessions revoke <id>      # end one
orama auth sessions revoke --all     # end all of them
```

Ending a session stops it minting new access tokens, and refuses the access
tokens it already minted — and closes the sockets they hold open — within ten
seconds. A session issued before sessions carried an id cannot be named that
way: its access tokens work until they expire, at most 15 minutes, and the
command says so.

Creating a namespace is its own step, and the wallet that makes it owns it:

```bash
orama namespace create myapp
orama auth login --namespace myapp
```

Signing in to a namespace that does not exist used to create it, so a typo made
a namespace and one belonged to whoever happened to sign in first. It answers
404 now, naming the command above.

One machine can hold credentials for several environments. `orama auth list`
shows them, `orama auth switch` changes the active one, and `orama auth logout`
clears it. Which gateway a command talks to is decided by the active
environment — see `orama network` — not by a flag on each command.

Every command and flag is in the [CLI reference](CLI_REFERENCE.md), which is
generated from the command tree rather than written by hand.

### API keys for your application

Deploying uses your own credentials. An application that talks to the gateway at
runtime needs a key of its own, and which one depends on where the code runs:

```bash
# Safe in a browser bundle: data-plane grants only
orama namespace keys create --scope app-runtime --label web

# Server-side only: the whole control plane
orama namespace keys create --scope admin --label ci

orama namespace keys list
orama namespace keys revoke --id <id>
```

A key carries a set of grants and the gateway refuses an operation whose grant
the key does not hold, naming the one it needed. `--scope` takes a profile
(`invoke-only`, `app-runtime`, `admin`) or an explicit grant list. See
[Where a key belongs](TS_SDK.md#where-a-key-belongs).

Every key expires — 90 days by default, a year at most — and `orama namespace
keys rotate --id <id>` mints a successor with the same grants while leaving the
original alive for a week, so there is a window in which to deploy the new one.

### Working with other people

A namespace has one owner and any number of members, and a member holds a role:

```bash
orama members list
orama members add 0xabc… --role admin     # the control plane
orama members add 0xdef… --role runtime   # the data plane
orama members remove 0xabc…
orama members transfer 0xabc…             # hand the namespace over
```

`admin` is everything except ownership; `runtime` is invoke, storage, push,
webrtc, proxy, pubsub and cache; `reader` is a member with no grant at all.
Ownership is transferred rather than granted, and it is one step rather than a
removal and a grant, so there is no moment where the namespace has no owner. You
keep an admin grant, so handing a project over does not lock you out of it.

### What happened, and who did it

```bash
orama audit                 # oldest first
orama audit --follow        # and keep printing
```

Sign-ins, keys minted and revoked, grants given and taken away, deployments,
functions, secrets and namespace changes, each with who did it and from where.
Events are kept 90 days.

The whole model — identities, roles, grants, tokens, and what each refusal
means — is in [AUTH.md](AUTH.md).

---

## Deploying Static Sites

Deploy static sites built with React, Vue, Angular, or any static site generator.

### React/Vite Example

```bash
# 1. Build your React app
cd my-react-app
npm run build

# 2. Deploy the build directory
orama deploy static ./dist --name my-react-app

# Output:
# 📦 Creating tarball from ./dist...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-react-app
# Type:         static
# Status:       active
# Version:      1
# Content CID:  QmXxxx...
#
# URLs:
#   • https://my-react-app.orama.network
```

### What Happens Behind the Scenes

1. **Tarball Creation**: CLI automatically creates a `.tar.gz` from your directory
2. **IPFS Upload**: Files are uploaded to IPFS and pinned across the network
3. **DNS Record**: A DNS record is created pointing `my-react-app.orama.network` to the gateway
4. **Instant Serving**: Your app is immediately accessible via the URL

### Features

- ✅ **SPA Routing**: Unknown routes automatically serve `/index.html` (perfect for React Router)
- ✅ **Correct Content-Types**: Automatically detects and serves `.html`, `.css`, `.js`, `.json`, `.png`, etc.
- ✅ **Caching**: `Cache-Control: public, max-age=3600` headers for optimal performance
- ✅ **Zero Downtime Updates**: Use `--update` flag to update without downtime

### Updating a Deployment

```bash
# Make changes to your app
# Rebuild
npm run build

# Update deployment
orama deploy static ./dist --name my-react-app --update

# Version increments automatically (1 → 2)
```

### Deployment names

A name is 1–56 characters of letters, digits, `-` and `_`, starting with a
letter or digit. Anything else is refused with `400` before the upload is
stored: the name becomes part of the app's subdomain (one DNS label) and of the
systemd unit that runs it (`orama-deploy-<runtime>@<namespace>-<name>`).

A new deployment is refused with `409` when:

- one with the same name already exists in the namespace — deploy again with
  `--update`;
- its `<namespace>-<name>` is already another deployment's unit instance.
  Namespace `a` with name `b-c` and namespace `a-b` with name `c` would share
  one unit, one directory and one environment file, so the second is refused
  and has to choose a different name.

The second check is made on the node itself, not only in the namespace's
registry: every gateway on a node shares the deployments directory, but each
reads only its own deployments table. A deployment claims its instance by
creating its directory (`<oramaDir>/data/deployments/<namespace>-<name>`),
which only one request can do, and records its namespace and name in a
`.orama-owner` file inside it. A deploy, update, rollback or replica that finds
the directory owned by another deployment is refused with `409`; delete and
replica teardown leave another deployment's unit and files alone. A create that
fails before the deployment is recorded removes the directory again, and a
delete removes it (a delete whose files cannot be removed fails, so it can be
retried). An archive's own `.orama-owner` is never extracted.

A directory from before this check has no `.orama-owner`. It is adopted by the
deployment it is named after if the gateway handling the request has that
deployment, and refused otherwise — the files may be another gateway's, and
until an operator removes them the name cannot be used.

### Deployment addresses

A deployment is served at `https://<name>-<random>.<base domain>`, the
`subdomain` in `orama app get`: the name, a hyphen and a random suffix, unique
across every namespace. A verified custom domain is served too.

The bare `https://<name>.<base domain>` is only the address of a deployment
created before subdomains existed, which has none. A deployment that has a
subdomain is not served at its bare name: names are unique per namespace, not
across them, so the bare host would not say whose deployment it is. If two
namespaces still have a subdomain-less deployment of the same name, that bare
host answers `404` for both until one of them is deleted.

### Replicas

A dynamic deployment (Next.js SSR, Node.js, Go) runs on its home node and on a
second node, its replica. After the home node pins the build artifact it calls
the replica node's internal `POST /v1/internal/deployments/replica/setup`. The
artifact is not always on that node yet when the call arrives, so the replica
polls for it (every 2 s, up to 60 s) instead of failing on the first miss, and
only then extracts it, starts the unit and answers its port. Only "not
retrievable yet" is waited for; any other fetch failure is answered at once.

The internal replica routes (`setup`, `update`, `rollback`, `teardown`, `env`) answer
every error as JSON, `{"error": "<reason>"}`, with the status that fits. The home
node carries that reason into its own error and log, so a failed setup reads
`node <id> returned status 500: Failed to extract content: ...` rather than a
parse error.

A setup that fails is not dropped. The replica's row in `deployment_replicas` is
written with status `failed` and a `replica_setup_failed` event carrying the
reason is added to the deployment's events (`GET /v1/deployments/events`). The
cluster leader's reconciliation, every 5 minutes, sees the deployment
under-replicated and sets a replica up again, with the deployment's stored
(sealed) environment decoded first. A deployment whose environment cannot be
read is not re-replicated, and the failure is logged, rather than started
without its variables.

An **update** (`orama deploy ... --update`) and a **rollback** are applied on the
home node, then on every active replica, and the command waits for each (the
response's deadline is moved to cover one replica call, 180 s plus 30 s). It
succeeds only when all replicas applied it. A replica that did not is reported
with status 502, naming how many replicas applied it and each node that did not
with a generic reason (refused with a status, did not answer in time, or could
not be reached; the peer's own text and overlay addresses go to the home node's
log, not to the response), and a `replica_update_failed` event is
added to the deployment's events; the home node already runs the new version,
and the failed nodes keep serving the old one. Running the same update or
rollback again retries: it makes a new version and sends it to every replica.
Each call carries the deployment's version; a replica refuses (409) a version
older than the one it applied, so an older update that arrives late cannot put it
back. Update, rollback and environment changes of one deployment take turns on
the home node and on the replica, and a replica's setup and teardown take the same lock.
The waits nest, each strictly shorter than the one around it, and an update
or rollback has two segments so a slow local step cannot starve the replicas: the
lock wait and the work on the home node (extract, pin, start, health) share
120 s, then the replicas get their own 210 s (one replica call is bounded at
180 s, with room to report). The home node's whole handler is therefore bounded
at 330 s; the gateway hop to the home node allows 360 s and the entry gateway's
write deadline is moved to 390 s, for the routes that change a deployment
(update, rollback, environment, delete). The home node moves its own deadlines
before it reads the upload or waits for the lock. An update or rollback that
finds the lock held past its 120 s is refused with 503 (run it again).

Update, rollback and environment change are made on the home node only, because
the lock and the version stamps live there. One that reaches another gateway is
forwarded; if the home node does not answer it is refused with 503 and
`Retry-After`, and never run on the node that took the request. The marker
header that says a request was already forwarded (`X-Orama-Proxy-Node`) is
honoured only from a peer on the WireGuard overlay: the entry gateway removes it
from every other request, so a client cannot use it to run a change on any node.
**Delete** is not tied to the home node: it carries no version stamp, takes the
deployment lock on whichever node runs it, and falls through to that node when
the home node cannot be reached, so a deployment whose home node is gone for
good can still be deleted (by `name` or by `id`; both are routed the same way).
An environment change that finds an update holding the lock waits at most its
own 30 s local budget, then is refused with 503 (run it again). (Deleting a deployment still tells its replicas
to tear down without waiting; that is not an update.)

The node's health checker probes each local replica's own port every 30 s. A
replica row with no port (a failed setup) is not probed, and a `failed` replica
is marked `active` again only when the probe answers **and** its own systemd
unit reports `active`; an answer from some other process on the port does not
bring it back.

The same checker reaps **orphan runtime units**: an `orama-deploy-{node,npm,go}@<instance>`
unit that outlived its deployment (a delete whose stop was refused, or that ran
while the node was down) and still holds the port the registry has freed. Every
2 minutes it lists the node's runtime units (any state; the `build@` and
`clean@` oneshots are ignored) and compares their instances with every row of
the registry's `deployments` table, in any status. A unit with no row is stopped,
disabled, and its staged secrets, installed dependencies and extracted files
are removed (a unit whose stop fails keeps its files, which it still runs from),
and a Warn line names it. Every doubt stops nothing: the registry read must
succeed and hold at least one row; the unit must be unmatched on two consecutive
sweeps; its age must be at least 10 minutes, the longer of its systemd age (last
activation, or last state change if failed) and how long this node has been
finding it unmatched, so a crash-looping unit whose activation keeps resetting
is still reaped; and at most 2 units are stopped per sweep. A sweep runs on its
own timer, bounded to 90 seconds, so it never delays the replica probes. A unit
that gains a row, disappears, or is seen while the registry read fails or is
empty starts over. A stop that fails is logged as an error and retried on a
later sweep.

---

## Deploying Next.js Applications

Deploy Next.js apps with full SSR (Server-Side Rendering) support.

### Prerequisites

> ⚠️ **IMPORTANT**: Your `next.config.js` MUST have `output: 'standalone'` for SSR deployments.

```js
// next.config.js
/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',  // REQUIRED for SSR deployments
}

module.exports = nextConfig
```

This setting makes Next.js create a standalone build in `.next/standalone/` that can run without `node_modules`.

### Next.js with SSR

```bash
# 1. Ensure next.config.js has output: 'standalone'

# 2. Deploy the project source directory with SSR enabled
#    (the CLI installs dependencies, runs the build, and uploads for you)
cd my-nextjs-app
orama deploy nextjs . --name my-nextjs --ssr

# Output:
# 📦 Installing dependencies...
# 🔨 Building Next.js application...
# 📦 Creating tarball from standalone output...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-nextjs
# Type:         nextjs
# Status:       active
# Version:      1
# Port:         10200
#
# URLs:
#   • https://my-nextjs.orama.network
#
# ⚠️  Note: SSR deployment may take a minute to start. Check status with: orama app get my-nextjs
```

### What Happens Behind the Scenes

1. **Build**: The CLI runs `npm install` (if `node_modules` is missing) and `npm run build` in your project directory
2. **Tarball Upload**: The `.next/standalone/` output (with `.next/static` and `public` copied in) is tarballed and uploaded
3. **Home Node Assignment**: A node is chosen to host your app based on capacity
4. **Port Allocation**: A unique port (10200-19999) is assigned
5. **Systemd Service**: A systemd service is created to run `node server.js`
6. **Health Checks**: Gateway monitors your app every 30 seconds
7. **Reverse Proxy**: Gateway proxies requests from your domain to the local port

### Static Next.js Export (No SSR)

If you export Next.js to static HTML:

```bash
# next.config.js
module.exports = {
  output: 'export'
}

# Build and deploy as static
npm run build
orama deploy static ./out --name my-nextjs-static
```

---

## Deploying Go Backends

Deploy Go backends for high-performance APIs.

### Prerequisites

> ⚠️ **IMPORTANT**: Your Go application MUST:
> 1. Have a `go.mod` in the project root (the CLI cross-compiles for you)
> 2. Listen on the port from `PORT` environment variable
> 3. Implement a `/health` endpoint that returns HTTP 200 when ready
> 4. Build with `CGO_ENABLED=0` (the CLI compiles with cgo disabled — use pure-Go dependencies)

### Go REST API Example

```bash
# Deploy the project source directory — the CLI cross-compiles
# for linux/amd64 (CGO_ENABLED=0) and uploads for you
cd my-go-api
orama deploy go . --name my-api

# Output:
# 🔨 Building Go binary (linux/amd64)...
# 📦 Creating tarball...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-api
# Type:         go
# Status:       active
# Version:      1
# Port:         10101
#
# URLs:
#   • https://my-api.orama.network
```

### Example Go API Code

```go
// main.go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
)

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    })

    http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
        users := []map[string]interface{}{
            {"id": 1, "name": "Alice"},
            {"id": 2, "name": "Bob"},
        }
        json.NewEncoder(w).Encode(users)
    })

    log.Printf("Starting server on port %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}
```

### Important Notes

- **Environment Variables**: The `PORT` environment variable is automatically set to your allocated port
- **Health Endpoint**: **REQUIRED** - Must implement `/health` that returns HTTP 200 when ready
- **Automatic Cross-Compilation**: The CLI runs `go build -o app .` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` — no manual build needed
- **No cgo**: Because builds use `CGO_ENABLED=0`, dependencies requiring cgo (e.g. `mattn/go-sqlite3`) will not work — use pure-Go alternatives (e.g. `modernc.org/sqlite`)
- **Systemd Managed**: Runs as a systemd service with auto-restart on failure
- **Port Range**: Allocated ports are in the range 10200-19999. 10000-10199 is the tenant and index blocks (rqlite, Olric, the gateway, IPFS) and is not given to an app.

---

## Deploying Node.js Backends

Deploy Node.js/Express/TypeScript backends. Nodes run them on Node.js 24 LTS
(24.21.0), which every node installs; there is no per-deployment choice of
version.

### Prerequisites

> ⚠️ **IMPORTANT**: Your Node.js application MUST:
> 1. Listen on the port from `PORT` environment variable
> 2. Implement a `/health` endpoint that returns HTTP 200 when ready
> 3. Have a valid `package.json` with either:
>    - A `start` script (runs via `npm start`), OR
>    - A `main` field pointing to entry file (runs via `node {main}`), OR
>    - An `index.js` file (default fallback)

### Express API Example

```bash
# Deploy the project source directory — the CLI installs dependencies,
# runs the build script (if any), and uploads for you
cd my-node-api
orama deploy nodejs . --name my-node-api

# Output:
# 📦 Installing dependencies...
# 🔨 Building...
# 📦 Creating tarball...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-node-api
# Type:         nodejs
# Status:       active
# Version:      1
# Port:         10102
#
# URLs:
#   • https://my-node-api.orama.network
```

### Example Node.js API

```javascript
// server.js
const express = require('express');
const app = express();
const port = process.env.PORT || 8080;

app.get('/health', (req, res) => {
  res.json({ status: 'healthy' });
});

app.get('/api/data', (req, res) => {
  res.json({ message: 'Hello from Orama Network!' });
});

app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
```

### Important Notes

- **Environment Variables**: The `PORT` environment variable is automatically set to your allocated port
- **Health Endpoint**: **REQUIRED** - Must implement `/health` that returns HTTP 200 when ready
- **Dependencies**: The CLI runs `npm install --production` locally if `node_modules` is missing; `node_modules` and hidden files are excluded from the uploaded tarball, and dependencies are installed on the server — unless the tarball carries its own `node_modules` or the upload sets the `skip_install=true` form field (API only)
- **How the server installs them**: in a sandboxed one-shot unit, `orama-deploy-build@<namespace>-<name>`, as `npm install --omit=dev --ignore-scripts --no-audit --no-fund` run against copies of your `package.json` and `package-lock.json`/`npm-shrinkwrap.json` only. That means:
  - **No install scripts run** — not yours, not any dependency's (`preinstall`, `install`, `postinstall`, `prepare`). Packages that compile or download a native binary in a script (`bcrypt`, `sharp`, `sqlite3`, …) will not work from a server install: ship `node_modules` built for Linux x86-64 in the tarball instead. `orama deploy nodejs` leaves `node_modules` out of the tarball, so shipping it means uploading the tarball through the API (`POST /v1/deployments/nodejs/upload`)
  - **Only npm registry dependencies are installed.** A git dependency (`github:user/repo`, `user/repo`, `git+ssh://…`), a URL, `file:`, `link:` or a path (`../lib`) — in any dependency field, `overrides` or the lockfile — and `workspaces` are refused before the install starts, with `500` and a message naming the dependency. Ship `node_modules` for those
  - **Your `.npmrc` is not read**, so a private registry or auth token configured there is not used; the build reaches the public npm registry only (no loopback, no private networks), and a lockfile's tarball URLs are fetched from `registry.npmjs.org` whatever host they name. Ship `node_modules` for private packages
  - The install must finish within 4 minutes; a failed install fails the deploy, and npm's output is in `journalctl -u orama-deploy-build@<namespace>-<name>` on the node
  - The installed `node_modules` is mounted one directory above your app, so Node finds it after any `node_modules` your app ships itself. It is removed when the deployment is deleted, and a deployment created without a server-side install starts with none
  - `npm run build` is not run on the server; build before uploading
- **Start Command Detection**:
  1. If `package.json` has `scripts.start` → runs `npm start`
  2. Else if `package.json` has `main` field → runs `node {main}`
  3. Else → runs `node index.js`
- **Systemd Managed**: Runs as a systemd service with auto-restart on failure

---

## Managing SQLite Databases

Each namespace gets its own isolated SQLite databases.

### Creating a Database

```bash
# Create a new database
orama db create my-database

# Output:
# ✅ Database created successfully!
#
# Name:      my-database
# Home Node: node-abc123
# Created:   2024-01-22T10:30:00Z
```

The database file is stored on the home node at `/opt/orama/.orama/data/sqlite/{your-namespace}/my-database.db`. A query, delete, or backup that arrives on another node is forwarded to that home node over the WireGuard overlay (to your namespace's gateway port on that node); the caller does not choose the node.

### Executing Queries

```bash
# Create a table
orama db query my-database "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"

# Insert data
orama db query my-database "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')"

# Query data
orama db query my-database "SELECT * FROM users"

# Output:
# 📊 Query Result
# Rows: 1
#
# id              | name            | email
# ----------------+-----------------+-------------------------
# 1               | Alice           | alice@example.com
```

### Listing Databases

```bash
orama db list

# Output:
# NAME              SIZE        BACKUP CID      CREATED
# my-database       12.3 KB     QmYxxx...       2024-01-22 10:30
# prod-database     1.2 MB      -               2024-01-20 09:15
#
# Total: 2
```

### Backing Up to IPFS

```bash
# Create a backup
orama db backup my-database

# Output:
# ✅ Backup created
# CID: QmYxxx...
# Size: 12.3 KB

# List backups
orama db backups my-database

# Output:
# CID               SIZE        BACKED UP
# QmYxxx...         12.3 KB     2024-01-22 10:45
# QmZxxx...         15.1 KB     2024-01-22 14:20
```

### Deleting a Database

```bash
orama db delete my-database

# This permanently deletes "my-database" and its file. There is no undo.
# Type the database name to confirm: my-database
# ✓ my-database deleted (3 file(s) removed).
```

Typing the name back is the confirmation, not a y/n prompt: a y/n is answered
reflexively, and the mistake this guards against is the right command aimed at
the wrong database. `--yes` skips the prompt for scripts.

The database file and its write-ahead log are removed from the node that holds
them. Back up first with `orama db backup` if you may want the data again.

### Database Features

- ✅ **WAL Mode**: Write-Ahead Logging for better concurrency
- ✅ **Namespace Isolation**: Complete separation between namespaces. Tenant SQL cannot `ATTACH`/`DETACH` another database file or `VACUUM INTO` a path, can run only per-file pragmas (`table_info`, `table_list`, `index_list`, `index_info`, `foreign_key_list`, `foreign_key_check`, `foreign_keys`, `defer_foreign_keys`, `user_version`, `application_id`, `integrity_check`, `quick_check`, `page_count`, `freelist_count`, `optimize`), and extra statements in one query are rejected.
- ✅ **On-Demand Backups**: Back up to IPFS anytime with `orama db backup`
- ✅ **ACID Transactions**: Full SQLite transactional support
- ✅ **Concurrent Reads**: Multiple readers can query simultaneously

---

## Environment Variables

Go, Node.js and Next.js SSR deployments run as a process, and that process reads
its configuration from environment variables. Set them at deploy time, or change
them afterwards without redeploying.

### At deploy time

```bash
orama deploy go ./my-api --name my-api \
  --env DATABASE_URL=postgres://... \
  --env LOG_LEVEL=debug

# Or read them from a file
orama deploy go ./my-api --name my-api --env-file .env.production
```

`--env` is repeatable and splits on the first `=` only, so a value may contain
one. `--env-file` reads a `.env`: `KEY=VALUE` per line, `#` comments and blank
lines skipped, one layer of surrounding quotes removed. It does **not** expand
`$VAR` — the literal text in the file is what gets sent, not whatever the
machine running the deploy happens to have set. A `--env` on the command line
overrides the same name from the file.

`--env` and `--env-file` belong to a first deploy. With `--update` the CLI
refuses them: an update replaces the build and never reads variables, so they
would be dropped without a word. Change variables on a running app with
`orama app env set`, below.

### After deploying

```bash
orama app env list my-api
orama app env set my-api --env DATABASE_URL=postgres://...
orama app env set my-api --env-file .env.production
orama app env unset my-api OLD_FLAG DEBUG_MODE
```

Setting or removing a variable rewrites the app's environment file and restarts
it, so the change takes effect immediately. A static site has no process, so its
variables are recorded and nothing is restarted.

The change reaches **every node that runs the app**, not only the home node, and
the command succeeds only when all of them applied it.

- The home node takes the deployment's lock (an environment change, an update and
  a rollback of one deployment take turns), reads the deployment inside it, and
  writes the new environment with a compare-and-swap on the stored value; a write
  that finds the row moved is refused (409, run the command again).
- It restarts its own unit, then calls each active replica's internal
  `POST /v1/internal/deployments/replica/env` in parallel and waits for every
  answer. The environment goes sealed with the cluster key, as in a replica
  setup, with a version stamp (a nanosecond timestamp made strictly increasing
  per deployment). The stamp is also written as the row's `updated_at`, so after
  a restart with the clock behind, the next stamp starts above the previous one
  and no replica refuses it for good (409).
- A replica takes the deployment's type, limits and port from its own registry
  row, found by deployment id, namespace and name together on this node's active
  replica; a request that pairs an id with another deployment's name is refused
  (404), and so is a deployment with no process (400). It refuses a version older
  than the one it already applied (409; the applied version is a file beside the
  deployment's directory, so it survives a restart), re-writes its env file,
  re-mints the workload token and restarts the unit.
- If a replica could not be reached or refused, the command fails with status 502
  and a message naming how many replicas applied the change and each node that did
  not, with a generic reason (never the peer's own text or its address). A
  `replica_env_failed` event is added to the deployment's events. The change is
  already saved and applied on the home node and on the replicas that answered;
  the failed nodes keep the old environment until the command is run again.
  **Retry is the same command, which re-sends the environment to every replica**
  (applying an environment is idempotent); there is no retry of only the failed
  nodes.

The waits nest, each strictly shorter than the one around it, so the 502 reaches
the user instead of a timeout: a replica's restart is bounded at 20 s, the home
node waits at most 30 s for one replica, the whole change (lock wait included)
is bounded at 60 s, and `orama app env set` / `unset` wait 90 s (every other CLI
call keeps its 30 s). The gateway's own limits sit outside all of them: the
hop to the home node for a deployment change allows 360 s and the entry
gateway's write deadline is 390 s. The environment change's 60 s is split the same
way: 30 s for the lock wait, the write and the local restart, then 30 s for the
replicas whatever the local step took.

**The whole fleet must run the version that has this endpoint.** A replica that
does not know `/v1/internal/deployments/replica/env` answers 404, so during a
rolling upgrade an environment change on an app with a not-yet-upgraded replica
fails with 502 naming that node (`refused the change (status 404)`). The change is
saved and applied on the home node; run the command again once that node is
upgraded.

**`list` shows names, never values.** Environment variables are where secrets
live, so an endpoint that echoed them would put every secret behind nothing more
than a read scope, and into whatever terminal scrollback or CI log the caller is
writing to. To change a value, set it again.

### What the platform sets for you

These names are set by the platform and cannot be overwritten or removed:

| Name | What it is |
|------|------------|
| `PORT` | The port your app must listen on, over TCP. It is how the gateway reaches you, and the only port your app may bind: listening on any other port, or binding a UDP socket, fails with `EPERM`. Outbound connections need no bind and are unaffected |
| `ENTRY_POINT` | What a Node.js deployment runs |
| `ORAMA_ENTRYPOINT` | The script the runtime starts, derived from `ENTRY_POINT` |
| `ORAMA_NAMESPACE` | The namespace this deployment belongs to |
| `ORAMA_GATEWAY_URL` | Your namespace's own gateway, `https://ns-<namespace>.<domain>` |
| `ORAMA_STATE_DIR` | A directory your app may write to that survives restarts |
| `ORAMA_CACHE_DIR` | A directory your app may write to that it must not rely on |

An app that talks back to Orama previously had nothing to go on: no address, no
namespace name. Every such app baked both into its own image.

### Your app's own credential

Your app is a principal of its own. At start it is handed a short-lived token in
the file named by `$ORAMA_TOKEN_FILE`, and it renews that token with the gateway
before it expires:

```js
const token = await readFile(process.env.ORAMA_TOKEN_FILE, "utf8");

// Before it expires, ask for the next one with the one you have.
const res = await fetch(`${process.env.ORAMA_GATEWAY_URL}/v1/auth/renew`, {
  method: "POST",
  headers: { Authorization: `Bearer ${token}` },
});
const { access_token, expires_in } = await res.json();
```

Nothing long-lived is on the node. systemd reads the file as PID 1 and hands
your process a copy owned by your app's own user; the gateway never has to write
anything your app could read directly.

**It reaches nothing until you grant it something**, which is the point — an app
that ships with no credential cannot leak one:

```bash
orama app grants set my-api runtime      # invoke, storage, push, webrtc, proxy, pubsub, cache
orama app grants list
```

A deployment cannot be granted the control plane. If something needs to deploy
or mint keys, that is a person or a CI key, not an app.

A grant you change reaches a running app on its next renewal, or immediately if
you redeploy.

### Where your app may write

Your app's own directory is read-only. The files there are its build output, and
a process that can rewrite its own code cannot be rolled back to a known
version. Write to `$ORAMA_STATE_DIR` instead, which is yours alone and survives
restarts, or `$ORAMA_CACHE_DIR` for anything you can regenerate.

### How the values are handled

Values are held encrypted in the cluster database, with a key derived from the
cluster secret, and sealed to the deployment's namespace and id, so a copy of
the stored value in another deployment's row does not open (on a cluster
upgraded from an earlier release this starts when an operator runs
`orama maint operator rotate-secrets`; a new cluster has it from the start). On the node they are written to a file only the system can read,
which systemd hands to your process — they are not written into the app's
systemd unit, and they are removed from the node when the deployment stops.

A value may contain anything that is valid UTF-8, including quotes,
backslashes, spaces and newlines, so a PEM key or a JSON blob goes in as it is.
It may not contain a NUL byte, and one value may be at most 64 KiB: every value
is replicated to every node in the cluster.
The environment as a whole, rendered as the file your app's unit reads, may be
at most 224 KiB; a deploy or `env set` that would exceed it is refused with
`400`. (The node stages at most 256 KiB, and the platform adds its own
variables.)

### What runs your app

Your app is an instance of a systemd template installed with the platform —
`orama-deploy-node@`, `orama-deploy-npm@` or `orama-deploy-go@` — named
`orama-deploy-<runtime>@<namespace>-<name>`. Which one you get follows from the
deployment type and, for Node.js, from `ENTRY_POINT`: `npm:start` runs
`npm start`, anything else runs `node <that file>`, and no value runs
`node index.js`.

The gateway does not write a unit for your app. It writes only the environment
file, and starts the template. That is not an implementation detail you can
ignore if you are running a node: the gateway runs as an unprivileged user with
`ProtectSystem=strict` and `NoNewPrivileges=yes`, so it *cannot* write into
`/etc`, and a node whose templates were not installed will refuse every deploy
with "Unit orama-deploy-node@… not found". They are installed by
`orama maint node install` and by every upgrade.

### What your app runs as

Each deployment runs as its own unprivileged user, allocated for it and
reclaimed when it stops, so no two deployments share an identity and none of
them is root. It cannot reach the cluster's internal network, and it has the
memory, CPU and process limits recorded on the deployment.

Of the platform's own directory tree it sees only its own deployment
directory, read-only: not the node's configuration or secrets, and not any
other deployment or database. Files it creates in `ORAMA_STATE_DIR` and
`ORAMA_CACHE_DIR` are private to it (`UMask=0077`).

### Health check path

```bash
orama deploy go ./my-api --name my-api --health-check /healthz
```

The platform polls this path to decide the app has started. It defaults to
`/health`.

### Why there is no `orama.yaml`

Repeated deploys still retype `--name`. A project file holding the name, type,
environment and health-check path would remove that, and it is worth doing — but
it is a design commitment, not a convenience: it needs precedence rules against
flags, a version field, validation, and an answer for what a checked-in file
does with secrets. `--env-file` already covers the part that hurt most. The
project file is tracked separately rather than half-built here.

---

## How Domains Work

### Domain Assignment

When you deploy an application, it automatically gets a domain:

```
Format: {deployment-name}.orama.network
Example: my-react-app.orama.network
```

### DNS Resolution Flow

1. **Client**: Browser requests `my-react-app.orama.network`
2. **DNS**: CoreDNS server queries RQLite for DNS record
3. **Record**: Returns IP address of a gateway node (round-robin across all nodes)
4. **Gateway**: Receives request with `Host: my-react-app.orama.network` header
5. **Routing**: Domain routing middleware looks up deployment by domain
6. **Cross-Node Proxy**: If deployment is on a different node, request is forwarded
7. **Response**:
   - **Static**: Serves content from IPFS
   - **Dynamic**: Reverse proxies to the app's local port

### Cross-Node Routing

DNS uses round-robin, so requests may hit any node in the cluster. If a deployment is hosted on a different node than the one receiving the request, the gateway proxies the request to that node's index gateway on its WireGuard address (`10.0.0.x:10104`). The public address is not a listener for that port. A node that does not run the app does not answer from a local process that happens to use the same port number.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Request Flow Example                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Client                                                          │
│    │                                                             │
│    ▼                                                             │
│  DNS (round-robin) ───► Node-2 (141.227.165.154)                │
│                            │                                     │
│                            ▼                                     │
│                    Check: Is deployment here?                    │
│                            │                                     │
│                    No ─────┴───► Cross-node proxy                │
│                                       │                          │
│                                       ▼                          │
│                              Node-1 (141.227.165.168)            │
│                              (Home node for deployment)          │
│                                       │                          │
│                                       ▼                          │
│                              localhost:<deployment port>         │
│                              (Deployment process)                │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

This is **transparent to users** - your app works regardless of which node handles the initial request.

Each node that forwards keeps a circuit breaker per app and node. Five failures in a row of the node itself (connection refused or reset, a timeout, or a 502/503/504 the platform produced because it could not reach your process) stop that node forwarding your app to that node for 30 seconds, then one request tests it. Your app's own error responses never count, so an app that returns 503 does not affect your other apps or anyone else's on the same node.

### Custom Domains

Attach a custom domain (e.g. `www.myapp.com`) to a deployment with `orama domain`.
A domain does not serve traffic until you prove you own it with a TXT record.

```bash
# Register the domain and print the TXT record to create
orama domain add www.myapp.com --app my-api

# After creating the record, activate the domain
orama domain verify www.myapp.com --wait 5m

# Or do both in one step
orama domain add www.myapp.com --app my-api --verify

orama domain list                    # every domain in the namespace
orama domain list --app my-api       # one app's domains
orama domain remove www.myapp.com
```

Every subcommand takes `--json`, which prints the gateway's reply verbatim.

`verify --wait` re-asks the gateway every 10 seconds until the record resolves
or the wait runs out. Only "the record is not visible yet" is retried; a domain
that was never added fails immediately.

Adding a domain proves nothing, so it does not reserve the name. Only a
**verified** domain, or a pending one of your own namespace, refuses an add
(`409 Domain already in use`, the same answer whichever namespace holds it).
A pending row of another namespace is replaced by your add, and a pending row
expires 72 hours after it was added. If two namespaces add the same name, the
last add holds the single row, and only its token verifies: re-run
`orama domain add` and use the token it prints.

After verification, point your domain's A record to your deployment's node IP.

> **No certificate for custom domains yet.** The gateway's TLS check allows only subdomains of the network's base domain (`core/pkg/gateway/status_handlers.go`), so HTTPS works on your deployment's own address, not on the custom domain.

#### HTTP API

| Method | Endpoint | Purpose |
|--------|----------|---------|
| `POST` | `/v1/deployments/domains/add` | Register the domain, return a verification token |
| `POST` | `/v1/deployments/domains/verify` | Check for a TXT record at `_orama-verify.{domain}` matching the token |
| `GET` | `/v1/deployments/domains/list` | List domains — the whole namespace, or one app with `?deployment_name=` |
| `DELETE` | `/v1/deployments/domains/remove?domain=` | Detach a domain (`POST` also accepted) |

Methods are enforced. These endpoints used to accept any verb, so a `GET` to
`remove` deleted the domain and the documentation disagreed with itself about
which verb each one took.

---

## Full-Stack Application Example

Deploy a complete full-stack application with React frontend, Go backend, and SQLite database.

### Architecture

```
┌─────────────────────────────────────────────┐
│   React Frontend (Static)                   │
│   Domain: myapp.orama.network               │
│   Deployed to IPFS                          │
└─────────────────┬───────────────────────────┘
                  │
                  │ API Calls
                  ▼
┌─────────────────────────────────────────────┐
│   Go Backend (Dynamic)                      │
│   Domain: myapp-api.orama.network           │
│   Port: 10200                               │
│   Systemd Service                           │
└─────────────────┬───────────────────────────┘
                  │
                  │ SQL Queries
                  ▼
┌─────────────────────────────────────────────┐
│   SQLite Database                           │
│   Name: myapp-db                            │
│   File: .../data/sqlite/ns/myapp-db.db      │
└─────────────────────────────────────────────┘
```

### Step 1: Create the Database

```bash
# Create database
orama db create myapp-db

# Create schema
orama db query myapp-db "CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)"

# Insert test data
orama db query myapp-db "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')"
```

### Step 2: Deploy Go Backend

**Backend Code** (`main.go`):

```go
package main

import (
    "database/sql"
    "encoding/json"
    "log"
    "net/http"
    "os"

    _ "modernc.org/sqlite" // pure-Go driver (deployments build with CGO_ENABLED=0)
)

type User struct {
    ID        int    `json:"id"`
    Name      string `json:"name"`
    Email     string `json:"email"`
    CreatedAt string `json:"created_at"`
}

var db *sql.DB

func main() {
    // Orama only injects the PORT env var — the database path is up to you.
    // Databases created with `orama db create` live on the home node at:
    //   /opt/orama/.orama/data/sqlite/{your-namespace}/{db-name}.db
    dbPath := "/opt/orama/.orama/data/sqlite/your-namespace/myapp-db.db"

    var err error
    db, err = sql.Open("sqlite", dbPath)
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    // CORS middleware
    http.HandleFunc("/", corsMiddleware(routes))

    log.Printf("Starting server on port %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

func routes(w http.ResponseWriter, r *http.Request) {
    switch r.URL.Path {
    case "/health":
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    case "/api/users":
        if r.Method == "GET" {
            getUsers(w, r)
        } else if r.Method == "POST" {
            createUser(w, r)
        }
    default:
        http.NotFound(w, r)
    }
}

func getUsers(w http.ResponseWriter, r *http.Request) {
    rows, err := db.Query("SELECT id, name, email, created_at FROM users ORDER BY id")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var users []User
    for rows.Next() {
        var u User
        rows.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
        users = append(users, u)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(users)
}

func createUser(w http.ResponseWriter, r *http.Request) {
    var u User
    if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    result, err := db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", u.Name, u.Email)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    id, _ := result.LastInsertId()
    u.ID = int(id)

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(u)
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        next(w, r)
    }
}
```

**Deploy Backend**:

```bash
# Deploy the source directory — the CLI cross-compiles
# for linux/amd64 (CGO_ENABLED=0) and uploads for you
orama deploy go . --name myapp-api
```

### Step 3: Deploy React Frontend

**Frontend Code** (`src/App.jsx`):

```jsx
import { useEffect, useState } from 'react';

function App() {
  const [users, setUsers] = useState([]);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');

  const API_URL = 'https://myapp-api.orama.network';

  useEffect(() => {
    fetchUsers();
  }, []);

  const fetchUsers = async () => {
    const response = await fetch(`${API_URL}/api/users`);
    const data = await response.json();
    setUsers(data);
  };

  const addUser = async (e) => {
    e.preventDefault();
    await fetch(`${API_URL}/api/users`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, email }),
    });
    setName('');
    setEmail('');
    fetchUsers();
  };

  return (
    <div>
      <h1>Orama Network Full-Stack App</h1>

      <h2>Add User</h2>
      <form onSubmit={addUser}>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name"
          required
        />
        <input
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="Email"
          type="email"
          required
        />
        <button type="submit">Add User</button>
      </form>

      <h2>Users</h2>
      <ul>
        {users.map((user) => (
          <li key={user.id}>
            {user.name} - {user.email}
          </li>
        ))}
      </ul>
    </div>
  );
}

export default App;
```

**Deploy Frontend**:

```bash
# Build
npm run build

# Deploy
orama deploy static ./dist --name myapp
```

### Step 4: Access Your App

Open your browser to:
- **Frontend**: `https://myapp.orama.network`
- **Backend API**: `https://myapp-api.orama.network/api/users`

### Full-Stack Summary

✅ **Frontend**: React app served from IPFS
✅ **Backend**: Go API running on allocated port
✅ **Database**: SQLite database with ACID transactions
✅ **Domains**: Automatic DNS for both services
✅ **Isolated**: All resources namespaced and secure

---

## Managing Deployments

### List All Deployments

```bash
orama app list

# Output:
# NAME              TYPE      STATUS    VERSION    CREATED
# my-react-app      static    active    1          2024-01-22 10:30
# myapp-api         go        active    1          2024-01-22 10:45
# my-nextjs         nextjs    active    2          2024-01-22 11:00
#
# Total: 3
```

### Get Deployment Details

```bash
orama app get my-react-app

# Output:
# Deployment: my-react-app
#
# ID:               dep-abc123
# Type:             static
# Status:           active
# Version:          1
# Namespace:        your-namespace
# Content CID:      QmXxxx...
# Memory Limit:     256 MB
# CPU Limit:        50%
# Restart Policy:   always
#
# URLs:
#   • https://my-react-app.orama.network
#
# Created:          2024-01-22T10:30:00Z
# Updated:          2024-01-22T10:30:00Z
```

### View Logs

```bash
# View last 100 lines
orama app logs my-nextjs

# The last 500 lines (1 to 1000; --follow is not supported)
orama app logs my-nextjs --lines 500
```

### Rollback to Previous Version

```bash
# Rollback to version 1
orama app rollback my-nextjs --version 1

# Output:
# ⚠️  Rolling back 'my-nextjs' to version 1. Continue? (y/N): y
#
# ✅ Rollback successful!
#
# Deployment:       my-nextjs
# Current Version:  1
# Rolled Back From: 2
# Rolled Back To:   1
# Status:           active
```

### Delete Deployment

```bash
orama app delete my-old-app

# Output:
# ⚠️  Are you sure you want to delete deployment 'my-old-app'? (y/N): y
#
# ✅ Deployment 'my-old-app' deleted successfully
```

---

## WebRTC (Voice/Video/Data)

Namespaces can enable WebRTC support for real-time communication (voice calls, video calls, data channels).

### Enable WebRTC

```bash
# Enable WebRTC for a namespace
orama namespace enable webrtc --namespace myapp

# Check WebRTC status
orama namespace webrtc-status --namespace myapp
```

This provisions SFU servers on all 3 nodes and TURN relay servers on up to 2 nodes,
allocates port blocks, creates DNS records, and opens firewall ports.

> **TURN is shared per host.** TURN binds the fixed ports 3478/5349, which are
> exclusive per physical host, so every namespace allocated TURN on a node is served
> by that node's single `orama-turn.service`. Each namespace authenticates against
> its own shared secret — the TURN credential already carries the namespace
> (`{expiry}:{namespace}`) — so tenants never share a relay identity, and a namespace
> the server does not serve is rejected outright.
>
> Adding or removing a namespace rewrites that host's `data/turn/turn.yaml`, which the
> running server re-reads within ~15s. It is **not** restarted: a restart would drop
> every other namespace's live relays on that host.
>
> A namespace can therefore get TURN on any node, regardless of what else runs there.
> `orama namespace webrtc-status` reports the number actually running.

### Disable WebRTC

```bash
orama namespace disable webrtc --namespace myapp
```

Stops all SFU/TURN services, deallocates ports, removes DNS records, and closes firewall ports.

### Client Integration

```javascript
// 1. Get TURN credentials
const creds = await fetch('https://ns-myapp.orama.network/v1/webrtc/turn/credentials', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${jwt}` }
});
const { urls, username, credential, ttl } = await creds.json();

// 2. Create PeerConnection (forced relay)
const pc = new RTCPeerConnection({
  iceServers: [{ urls, username, credential }],
  iceTransportPolicy: 'relay'
});

// 3. Connect signaling WebSocket
const ws = new WebSocket(
  `wss://ns-myapp.orama.network/v1/webrtc/signal?room=${roomId}`,
  ['Bearer', jwt]
);
```

See [docs/WEBRTC.md](WEBRTC.md) for the full API reference, room management, credential protocol, and debugging guide.

---

## Troubleshooting

### Deployment Issues

**Problem**: Deployment status is "failed"

```bash
# Check deployment details
orama app get my-app

# View logs for errors
orama app logs my-app

# Common issues:
# - App not listening on the PORT environment variable
# - Missing dependencies (not declared in package.json / go.mod)
# - Port already in use (shouldn't happen, but check logs)
# - bind: operation not permitted — the app listens on a port other than PORT,
#   or binds UDP; a deployment may bind only PORT, over TCP
# - Health check failing (ensure /health endpoint exists)
```

**Problem**: Can't access deployment URL

```bash
# 1. Check deployment status
orama app get my-app

# 2. Verify DNS (may take up to 10 seconds to propagate)
dig my-app.orama.network

# 3. For local development, add to /etc/hosts
echo "127.0.0.1 my-app.orama.network" | sudo tee -a /etc/hosts

# 4. Test with Host header
curl -H "Host: my-app.orama.network" http://localhost:10104/
```

### Database Issues

**Problem**: Database not found

```bash
# List all databases
orama db list

# Ensure database name matches exactly (case-sensitive)
# Databases are namespace-isolated
```

**Problem**: SQL query fails

```bash
# Check table exists
orama db query my-db "SELECT name FROM sqlite_master WHERE type='table'"

# Check table schema (sqlite3 dot-commands like .schema are NOT supported — plain SQL only)
orama db query my-db "SELECT sql FROM sqlite_master WHERE name='users'"
```

### Authentication Issues

```bash
# Re-authenticate. logout ends the session on the gateway as well as here.
orama auth logout
orama auth login

# Ask the gateway whether this credential still works, and what it holds
orama auth whoami
```

### Need Help?

- **Documentation**: Check `/docs` directory
- **Logs**: `orama app logs <app>` for an application. The gateway's own log is at `~/.orama/logs/gateway.log` **on a node**, not on your machine — reach it with `orama node logs`.
- **Issues**: Report bugs at GitHub repository
- **Community**: Join our Discord/Telegram

---

## Best Practices

### Security

1. **Never commit sensitive data**: Keep secrets in environment variables, set with `orama app env set --env-file` rather than a flag so they stay out of shell history. See [Environment Variables](#environment-variables)
2. **Validate inputs**: Always sanitize user input in your backend
3. **HTTPS only**: All deployments automatically use HTTPS in production
4. **CORS**: Configure CORS appropriately for your API

### Performance

1. **Optimize builds**: Minimize bundle sizes (React, Next.js)
2. **Use caching**: Leverage browser caching for static assets
3. **Database indexes**: Add indexes to frequently queried columns
4. **Health checks**: Implement `/health` endpoint for monitoring

### Deployment Workflow

1. **Test locally first**: Ensure your app works before deploying
2. **Use version control**: Track changes in Git
3. **Incremental updates**: Use `--update` flag instead of delete + redeploy
4. **Backup databases**: Regular backups via `orama db backup`
5. **Monitor logs**: Check logs after deployment for errors

---

## Next Steps

- **Every command and flag**: [CLI reference](CLI_REFERENCE.md), generated from the command tree
- **Every gateway route**: [API surface](API_SURFACE.md), with which client owns each one
- **Custom domains**: `orama domain add|verify|list|remove`, and [How Domains Work](#how-domains-work)
- **Production Deployment**: Install nodes with `orama maint node install` for production clusters
- **From code**: the [TypeScript SDK](TS_SDK.md) or the [Go client](GO_CLIENT_SDK.md)

---

**Orama Network** - Decentralized Application Platform

Deploy anywhere. Access everywhere. Own everything.
