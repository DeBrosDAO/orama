# The CLI and SDKs

> **At a glance.**
>
> - **What:** the ways a human or a program talks to a cluster. `orama` is one Go binary for tenants and operators; it calls gateway routes with a short-lived bearer, reaches machines over SSH with keys that live in the RootWallet vault, and signs through the RootWallet agent. The TypeScript SDK (`@debros/orama`) is the library for tenant applications, and a Go client serves the gateway itself and Go programs.
> - **Key numbers:** 28 visible command groups, 232 commands, 8 exit codes (0 to 7); gateway calls time out at 30 s with an 8 MiB response cap; agent requests wait up to 270 s. TypeScript SDK: 60 s timeout, 3 retries on 408, 429, 500, 502, 503 and 504, 15 min access tokens, WebSocket reconnect up to 10 attempts from 500 ms to 30 s.
> - **Code:** `core/cmd/orama/`, `core/pkg/rwagent/`, `sdk/src/`, `core/pkg/client/`, `contracts/`.

![The CLI: command tree, gateway calls, SSH keys from the wallet](../technical-reference/diagrams/ch35-overview.svg)

## No control panel

An Orama cluster has no dashboard and no MCP server. Every administrative act, from signing in to replacing a dead Raft voter, goes through the `orama` binary or the gateway HTTP API it wraps, and programs use the SDKs.

## The CLI

### One binary, two places

Tenants and operators use the same binary; what separates them is what a command authenticates with. Tenant groups (`deploy`, `function`, `db`, `domain`, `namespace`) use a gateway bearer. Operator groups use the bearer of a wallet on the cluster's operator list, or wallet-derived SSH keys. Local node commands (`install`, `upgrade`, `restart`) need root on the node.

### Structure that scripts can rely on

Handlers return errors and never exit, so deferred cleanup runs; only `runCLI` exits the process. A script can tell why a command failed: 2 is a usage error, 3 authentication, 4 not found, 5 unavailable (retry), 6 conflict (the cluster refused to protect an invariant, usually quorum), and 7 an operator who declined a confirmation. Confirmations accept only the exact string, so a script piping the wrong answer exits 7 rather than 0. `--json` is a root flag, and one printer call writes a table for people or an array of objects for scripts.

Tests, not review, enforce conventions. Cobra owns all flag parsing, because nineteen commands once parsed their own and `--help` reached their handlers as an argument. A test checks every printed `orama ...` hint against the real command tree, and the CLI reference is generated from the tree and compared with the committed file.

### Environments and credentials

There are no built-in environments, so a fresh machine cannot quietly talk to someone's network. `orama env add` accepts only an `https://` gateway (or loopback), and an environment's CA is trusted for that domain only. One function resolves the gateway URL and the credential together, because they once resolved separately and the key stored for one gateway was sent to another. The CLI sends a short-lived access token renewed from a session stored 0600, never an API key as the standing credential. Fleet commands find machines through the network API, then those recorded at setup, then a legacy `nodes.conf`.

### The wallet agent and SSH

The long-lived secrets stay in the RootWallet vault: the seed and every node's SSH key. `rwagent` talks to the desktop app over a Unix socket and refuses a socket that is a symlink, owned by another user or writable by others, checked again at every dial. Its 270 s timeout covers the agent's worst case (120 s approval, 120 s unlock wait, a margin), so the agent's own error arrives instead of a context deadline.

`PrepareNodeKeys` fetches each needed key for the length of one operation. It writes keys 0600 into a 0700 temp directory, touches the agent every 5 min so the 30 min auto-lock cannot lock the wallet mid-rollout, and on exit overwrites and removes the files, including on Ctrl-C. A test fails the build if the package ever loads a key into `ssh-add` or forwards an agent, which would let a key outlive the deploy. Every ssh call sets `IdentitiesOnly`, a 10 s connect timeout and a 60 s dead-session limit; secrets travel on stdin, never argv, and host keys are pinned before a password or bootstrap key is sent.

Three kinds of signature leave the CLI, each bound to a purpose: sign-in, build archives and releases, and chain transactions. The agent decodes a transaction itself and signs only if the user approves that one request; the client verifies the signature before submitting.

## The TypeScript SDK

![The two SDKs and what they reach](../technical-reference/diagrams/ch36-overview.svg)

One `HttpClient` is shared by seven sub-clients (auth, database, pub/sub, cache, storage, functions, network). The core entry has no Node imports and no cryptography; chain support and relayed fetch live behind their own subpaths so browser and React Native bundles pull them in only on request.

### Credentials: one header, one exchange

Every request carries `Authorization: Bearer <token>` and nothing else. A configured JWT is used as is. A configured API key is exchanged once for a 15 min token, renewed 60 s early, with concurrent callers sharing one exchange. A key lives 90 days and would otherwise sit in every access log and devtools trace; now it crosses the wire once per 15 min, to one route. On a 401 the client renews once, single-flight, and replays the request once. Device-bound sessions sign proofs with a key the platform owns, which never enters the SDK. A deployment gets a platform-issued token, not a key, and renews it 5 min before expiry.

### The request pipeline

The loop retries only statuses the gateway returned, waiting the `Retry-After` header (capped at 30 s) or about 1, 2 and 3 s with up to 25% jitter. Transport failures are not retried, a cancel is final, and the 60 s timeout covers the whole call including retries. Failures become typed errors, and `httpStatus` 0 marks "no HTTP response". Error codes and grants are constants checked by tests against the gateway's Go sources. Neither client chooses between gateways: the URL names the namespace, and failover is the application's job through an `onNetworkError` hook.

### WebSockets, storage and relayed fetch

A browser cannot set a header on a WebSocket upgrade, so the exchanged 15 min token goes in `?jwt=`, never the key. A drop is recovered with 10 attempts, 500 ms doubling to 30 s with jitter, and close handlers do not fire meanwhile. The reconnect reuses the original URL, so a socket the gateway closed for token expiry (code 4401) is retried with the same expired token.

Storage reads re-ask on a 404 for 18 s, because a pin takes time to reach every peer. `RelayedFetch` hides the caller's address from the storage node by running TLS to the storage host inside a WebSocket to a relay, which sees the address and size but never the CID. It never falls back to a direct request on failure, because that would send the address to the node the relay exists to hide it from.

### The chain subpath

The chain client has its own fetch wrapper, since the read proxy is open and a wallet has no gateway credential. It builds a `SignDoc` for one signer and hands it to an `OramaSigner` that holds the key elsewhere; `signTx` verifies the returned signature before assembling the transaction. `describeTx` renders approval text with exact amounts, and an unknown message type is shown as sensitive.

## The Go client

`core/pkg/client` has two audiences. Programs outside the cluster reach a gateway over HTTPS with a 60 s timeout, a 64 MiB response cap and no redirects. The gateway reaches its registry through direct RQLite connections in the mesh, at read level `weak` so an auth decision sees every acknowledged write. `Connect` reports success without having spoken to the database, so the gateway runs its own probe query.

## Staying in step with the gateway

Fixtures under `contracts/` pair each route's request, response and SDK call. A Go test decodes each request into the handler's own struct, rejecting unknown fields, and a TypeScript test drives the SDK through a stubbed `fetch` and asserts the exact body. Neither side can change a field alone, and neither test needs a cluster; a fleet test replays them live.

## Trust

A stolen laptop disk yields a session the gateway can revoke, not the fleet, since the seed and node keys stay in the vault. A browser bundle holds whatever credential it is given, so the SDK sends a long-lived key only once per 15 min.

The limit that matters most is that every method retries, so a `POST` can run twice after a gateway 502 or 504; applications need idempotent writes.
