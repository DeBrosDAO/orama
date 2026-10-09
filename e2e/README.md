# Fleet e2e

The release-gate test suite. One command provisions a disposable three-node
Orama fleet on Hetzner with a real Cloudflare delegation, real Let's Encrypt
staging certificates and a throwaway RootWallet agent, runs every feature
package against it stage by stage, collects artifacts, writes a report, and
always tears the fleet down.

```bash
make e2e-fleet                      # the whole run (secrets through infisical run)
make e2e-fleet E2E_FLAGS=--keep-on-fail
make e2e-coverage                   # the coverage gate, no servers
make e2e-lint                       # go vet (with and without the tag) + the contract lint
make e2e-test-unit                  # every pure unit test of the harness
```

This file is the **contract for feature packages**. A feature package that
follows it runs in the right stage, is counted by the coverage gate, and leaves
evidence in the report when it fails.

---

## Layout

```
e2e/
  go.mod                 own module (github.com/DeBrosOfficial/network/e2e), replaces ../core
  cmd/e2e-fleet/         the runner: run, provision, test, teardown, sweep, sweep-namespaces, report, coverage, hook, target
  harness/               what tests import (below); provision/hetzner/cloudflare/agent/sshx provision the fleet
  harness/broker/        the runner's credential broker (DNS TXT, extras, eval clusters) on a unix socket
  harness/monitor/       `orama monitor report --json` types and the cluster predicates
  features/<id>/         ONE feature per directory: feature.yaml + *_test.go (build tag e2e_fleet)
  features/internal/     helper packages shared by features (not features: no manifest, no TestMain)
  stages/stages.yaml     the eleven ordered stages
  waivers.yaml           what has no test yet, with a reason and a trigger
  lint/                  the contract checks `make test` runs
```

## Writing a feature

1. `mkdir features/<id>`; `<id>` is lowercase letters, digits and hyphens, and
   equals the manifest `id`.
2. Write `feature.yaml`, `main_test.go`, and tests. Every `.go` file starts with
   `//go:build e2e_fleet`.
3. `make e2e-lint && make e2e-coverage`, then `go vet -tags e2e_fleet ./features/<id>/`.

Code several features share goes in a package under `features/internal/`
(e.g. `features/internal/nsutil`). The lint does not treat it as a feature (no
manifest, TestMain or tests needed), but its files carry the build tag and
the timer and skip rules below apply to them too.
   Feature tests never run on a laptop: without a fleet, strict mode fails them
   and non-strict mode skips them.

### feature.yaml

```yaml
id: auth-siwe                     # = directory name
title: SIWE sign-in and sessions
area: auth                        # groups failures under "bugs found" in the report
subtasks: [2830, 2831]            # bugboard ids this feature verifies
stage: 3                          # 1..11, see stages/stages.yaml
destructive: false                # true: runs alone, after the stage's other packages
requires:
  extra_nodes: 0                  # 0..3 servers created on demand with harness.ExtraNode
  probe: false                    # needs state.probes (a vantage point in another location)
  chain: false                    # needs the co-hosted chain (harness.RequireChain)
covers:                           # what the tests exercise; unknown keys are rejected
  cli: ["orama auth login", "orama auth sessions revoke"]
  routes: ["/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/sessions/"]
  msgs: ["orama.token.v1.MsgCreateToken"]      # <proto package>.<request type> from tx.proto
  queries: ["orama.token.v1.Params"]           # <proto package>.<rpc> from query.proto
  units: ["orama-namespace-gateway@.service"]  # file names in core/systemd
  config: ["node.yaml:gateway.base_domain"]    # <file>:<key path>
  claims: ["docs/AUTH.md#signing-in: presenting a refresh token twice is a replay"]
```

`covers` ids map onto the coverage universe as `cli:<entry>`, `route:<entry>`,
`msg:<entry>`, `query:<entry>`, `unit:<entry>`:

| Kind | Source of truth | Entry is |
|------|-----------------|----------|
| cli | every `### orama ...` heading of `docs/CLI_REFERENCE.md` (group commands included: test that `orama app` lists its subcommands) | `orama app env set` |
| routes | every route row of `docs/API_SURFACE.md` (paths as written there; the doc has no method column) | `/v1/rqlite/query`, `/v1/auth/sessions/` |
| msgs | `service Msg` of `chain/proto/**/tx.proto` | `orama.token.v1.MsgMint` |
| queries | `service Query` of `chain/proto/**/query.proto` | `orama.token.v1.Params` |
| units | `core/systemd/*.service`, `*.timer` | `orama-turn.service` |
| config, claims | not enumerated; listed in the report | see above |

An entry of an enumerated kind that matches nothing that ships fails the gate
("unknown covers"): a typo cannot cover anything.

### TestMain

```go
//go:build e2e_fleet

package authsiwe

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }
```

`harness.Main` reads `E2E_FLEET_STATE`. Under the runner (`E2E_STRICT=1`) a
package started without it exits 1 instead of skipping everything. It refuses
(exit 1) a state that fails the run guards (see "Secrets and safety"). It opens
the package's evidence file, `<id>.jsonl` in the directory the runner gives
this package run (`E2E_EVIDENCE_DIR`, `<artifacts>/evidence/stage-NN-<id>/`),
or `<artifacts>/evidence/` when run by hand.

On SIGINT or SIGTERM the package does not die: running tests finish and their
cleanups restore the fleet, and a test that calls `harness.Fleet` afterwards
fails at once ("the run was interrupted"). The interrupt also cancels the
package's run-wide context (`harness/runctx`), so the harness's own waits in a
running test (`eventually.Require`/`Eventually`, namespace readiness,
`ExtraNode`, `ExtraCluster`) stop at once and the test reaches its cleanups
inside the stop grace. `eventually.Poll` with a context of your own (what a
cleanup uses) is not affected.

### Test names

`Test{Function}_{scenario}`, e.g. `TestVerify_replayedNonceRefused`. The lint
rejects other `Test*` names and flags a `func(*testing.T)` that is not named
`Test...` (it would never run).

---

## Rules

- **No sleeps.** `time.Sleep`, `time.After`, `time.NewTimer`, `time.Tick`,
  `time.NewTicker` and `time.AfterFunc` are banned in `features/**`. Wait for a readiness signal with
  `eventually.Require` / `eventually.Eventually` / `eventually.Poll`.
- **No bare skips.** `t.Skip`, `t.Skipf`, `t.SkipNow` are banned. Use
  `harness.SkipNotApplicable(t, reason)`; the reason says what would make the
  test apply. **Every skip is "not covered"** in the report and keeps the
  verdict from PASS.
- **Clean up everything.** Every fleet helper that changes a node registers a
  `t.Cleanup` that restores it and verifies the restore (the table below says
  how). Anything you create yourself (a key, a deployment, a member) gets a
  `t.Cleanup` that deletes it. The one exception is the bootstrap contract
  below.
- **One namespace per test** (`ns.New`), never a shared one. Tests call
  `t.Parallel()` unless they are in a destructive package.
- **A namespace lives on three nodes, not on every node** (core
  `DefaultRQLiteNodeCount`). A per-namespace check on a node (units, files,
  ports, DNS records, faults) loops over `tenancy.Members(t, f, name)`, which
  reads the placement from the nodes (`orama monitor namespaces`); a check that
  something is gone (teardown residue) and every fleet-wide check loops over
  `f.State.Nodes`.
- **Real paths only.** Requests go to the public name through DNS, TLS pinned to
  the run's CA, HTTP/1.1. Logins are real signatures. Operator actions go
  through the CLI under test. SSH is for observing nodes and for injecting
  failures, never for setting up state the product should set up.
- **No retries that turn red into green.** A failure is a failure. The runner
  re-runs each failed test once, alone, only to label it deterministic or
  flaky; the verdict ignores the re-run.
- **Destructive tests** (kill, partition, reboot, upgrade) go in a package with
  `destructive: true`, usually in stage 9-11.
- **Cleanups get their own context.** `t.Context()` is cancelled *before*
  `t.Cleanup` functions run, so a request made with it from a cleanup fails at
  once and the cleanup silently does nothing. Inside a cleanup use
  `ctx, cancel := fleet.CleanupContext(t)` (bounded by `fleet.CleanupBudget`),
  or `fleet.ContextFor(t)` in a helper that may run either way. `gw.MustSend`
  does this itself.

### Pacing credential calls

The gateway rate-limits the credential routes (`/v1/auth/challenge`,
`/verify`, `/api-key`, `/token`, `/refresh`, `/device`, `/device/token`,
`/device/approve`, `/devices/approve`) at **30 a minute, burst 10, per client
address, per gateway** (a namespace gateway has a limiter of its own), and
challenges at **10 a minute, burst 5, per wallet**
(`core/pkg/gateway/gateway.go` `configureRateLimiters`,
`core/pkg/gateway/handlers/auth/wallet_rate_limit.go`). The runner is one
address running many packages at once, so the product defaults stay (the
limiter is itself under test) and the harness paces itself client side
(`harness/pace`): a token bucket per (gateway host, bucket), bucket `cred`
(the address) or `challenge:<wallet>`, shared by every feature process and
every CLI invocation of the run through `pace-state.json` beside `state.json`,
guarded by `flock`.

| Variable | Default | Product limit |
|----------|---------|---------------|
| `E2E_PACE_CRED_PER_MIN` / `E2E_PACE_CRED_BURST` | 24 / 8 | 30 / 10 |
| `E2E_PACE_CHALLENGE_PER_MIN` / `E2E_PACE_CHALLENGE_BURST` | 8 / 4 | 10 / 5 |

A budget above the product limit (or not a positive integer) is refused when
the package starts. Pacing is automatic:

- `gw`: every request to a credential route (`Challenge`, `Verify`, `APIKey`,
  `Token`, `Refresh`, `SignIn`, `NewUser`, `ns.New`, a `Send`/`JSON` to one of
  those paths, a `Raw` whose request line names one) waits for its tokens
  first; a challenge body naming a wallet also waits on that wallet's bucket.
  A **429 on a paced request is a `*gw.PacingError`** ("pacing exceeded"),
  never retried: the budgets sit under the product's, so it means the limiter
  regressed or something unpaced spent this address's budget.
- `oramacli`: before `auth login` (with a wallet: 2 address tokens + the
  runner's `Wallet` challenge bucket; on a `NoWallet` runner: 1, then 1 per
  `/v1/auth/device/token` poll as the CLI prints its progress dot) and
  `auth approve` (2 + the approver's challenge bucket). A command whose
  environment (`Runner.Env` plus `RunOpts.Env`) gives `ORAMA_TOKEN` an API key
  rather than a token spends 1 beforehand, for the exchange on
  `/v1/auth/token`, which changes no file. Any other command may renew its
  session (refresh or API-key exchange): when the HOME's
  `.orama/credentials.json` changed, one address token is charged afterwards.
  Pacing targets `Runner.GatewayHost` (the env's gateway host) and
  `Runner.Wallet` (the address the runner's agent signs with); `ForState`
  sets both, and a fleet-mode runner without them refuses to run.

**Rate-limiter tests** (floods that expect 429) use `client.Unpaced()` or
`gw.NewUser(t, f, ns, gw.Unpaced())`, and nothing else does: an unpaced
flood spends the budget every other package of the run counts on, so keep it
in a `destructive: true` package, where it runs alone.

### Bootstrap contract

Stage 1 (`bootstrap`) sets the cluster's namespace creation to `open` for the
whole run and does not restore it: the fleet is disposable and torn down after
the run, and every later stage relies on it. `ns.New(t, f, ns.Options{})`
(`ViaUser`) then creates namespaces as fresh wallets without touching the
creator allowlist. It also raises the per-wallet namespace cap
(`max-namespaces-per-wallet`) when the fleet's live-namespace cap plus what the
run's operator already owns is above it, since every `ViaOperator` namespace is
the operator's: on a fresh fleet the live cap is sixteen and the default
per-wallet cap ten. These are the only state a test changes and leaves changed;
a test that needs another value sets it and restores the one it found.

### Edge-case checklist (every feature, every route or command it covers)

- no credential; expired token; revoked token; wrong role (runtime vs admin); wrong namespace; another user's resource
- empty, huge (over the limit), unicode (RTL override, combining, NUL), hostile input (SQL, path traversal, header injection, JSON with duplicate keys, wrong types)
- duplicate headers, malformed body, wrong method, wrong content type (`gw.Req`, `Client.Raw`)
- a dependency down (`Fleet.StopService`), a node down or partitioned (`Fleet.IPTablesBlock`, `Fleet.Kill`)
- concurrency: the same action from N goroutines at once; exactly-once where the product promises it
- state after a service restart, and after the upgrade stage (the `upgrade` stage re-checks the invariants earlier stages created)

---

## API cheat-sheet

```go
import (
	"github.com/DeBrosOfficial/network/e2e/harness"            // entry points
	"github.com/DeBrosOfficial/network/e2e/harness/eventually" // waiting
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"      // nodes
	"github.com/DeBrosOfficial/network/e2e/harness/gw"         // gateway HTTP/WS + auth
	"github.com/DeBrosOfficial/network/e2e/harness/ns"         // namespace per test
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"   // the CLI under test
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"     // keys and signatures
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"   // RunRecorded for other subprocesses
	"github.com/DeBrosOfficial/network/e2e/harness/provision"  // EvalCluster (ExtraCluster's type)
)
```

### harness

| Signature | Does |
|-----------|------|
| `Main(m *testing.M)` | TestMain of every feature package |
| `Fleet(t) *fleet.Fleet` | the run (state + node helpers) |
| `CLI(t) *oramacli.Runner` | the operator's CLI, isolated HOME, throwaway agent, evidence attributed to t |
| `GW(t) *gw.Client` | public gateway client, pinned CA, evidence attributed to t |
| `SkipNotApplicable(t, reason)` | the only allowed skip (counted as not covered) |
| `RequireChain(t)` | not-applicable skip when the run has no chain. `scripts/chain-deploy.sh` enables each node's REST API (`[api] enable = true`, `127.0.0.1:31003`, checked by `chain-deploy.sh status`), which the gateway's `/v1/chain/*` and the CLI's `--node` paths read; reach it from a test with `Fleet.Tunnel(t, n, "127.0.0.1:31003")`. It also installs the chain indexer (`orama-global-indexer.service`, user `orama-indexer`, `127.0.0.1:31015`) beside every node, which the gateway's `/v1/chain/index/` routes read (`status` checks it) |
| `ExtraNode(t, name, location) Extra` | a fresh server (nothing installed), created by the runner's broker and deleted at cleanup; `Extra{fleet.Node, HostKey}` (the node's fields are promoted: `extra.PublicIP`), HostKey the pinned `SHA256:...` fingerprint for `orama node setup --host-key`; `Fleet.Lookup` finds it while it lives |
| `ExtraCluster(t, name) provision.EvalCluster` | a single-node eval cluster (docs/EVAL.md) on its own server: subdomain `e2e-<run>-<name>.<zone>` delegated to it, genesis with the real CLI, Let's Encrypt staging certificate, its own `orama env` entry (`EvalCluster.Env`, never made current) and CA file; `EvalCluster{Name, Env, BaseDomain, GatewayURL, CAFile, Node, HostKey}`; removed (DNS, server, env, CA file) at cleanup. `name`: 1-12 lowercase letters/digits, starting with a letter. Counts as one of `requires.extra_nodes` |
| `CustomDomain(t, label) string` | `e2e-<run>-<label>.<zone>`: a name the run owns that Cloudflare itself serves (the run subdomain is delegated to the fleet, so a record under it is invisible to public resolvers) |
| `DNSTXT(t, name, value)` | a TXT record through the broker, deleted (that value) at cleanup; `name` must be inside the run's subdomain or a `CustomDomain`. Unblocks `orama domain verify`: `harness.DNSTXT(t, "_orama-verify."+domain, token)` |
| `Broker(t) *broker.Client` | the broker itself: `SetTXT`, `DeleteTXT`, `Records(ctx, under)`, `AddExtra`, `RemoveExtra`, `AddCluster`, `RemoveCluster` |
| `WorkTemp(t) string` | a fresh directory under the run's work dir, removed at cleanup: a working directory the CLI may run in (`oramacli.RunOpts.Dir`) |

### fleet

`fleet.State` fields: `RunID, Env, BaseDomain, GatewayURL, CAFile, Nodes, Extras, Probes, OramaBin, Home, PreviousOramaBin, ArchivePath, PreviousArchivePath, RWSock, OperatorAddress, SSHKeyFile, KnownHostsFile, ChainID, ChainRPC, ArtifactDir`.
`fleet.Node`: `Name` (node-1..3, extra-N, probe-N), `Role`, `PublicIP`, `WGIP`, `SSHUser`, `ServerID`, `Location`.
`fleet.HostKeyFingerprint(st, n) (string, error)` is the `SHA256:...` host key pinned for `n` in `st.KnownHostsFile`.

| Method on `*fleet.Fleet` | Does | Cleanup |
|------|------|---------|
| `Node(t, key) Node`, `Lookup(key) (Node, bool)` | by name, public IP or WG IP | — |
| `AllNodes() []Node` | core nodes + extras (the state's and those `ExtraNode` added) | — |
| `SSH(ctx, n) Shell`, `SSHFor(t, n) Shell` | raw shell: `Run(ctx, cmd) (Output, error)`, `Put`, `Get`; every command and transfer recorded (`SSHFor` attributes it to t) | — |
| `Redact(s) string` | masks the run's secrets and credential shapes, for failure messages | — |
| `Exec(t, n, cmd) Output` / `MustExec` | run a command; `MustExec` requires exit 0 | — |
| `Journal(t, n, unit, since) string` | bounded journal excerpt | — |
| `Unit(t, n, unit) string` | `active`, `inactive`, `failed`, ... | — |
| `ReadFile(t, n, path) []byte` | read a file | — |
| `WriteFile(t, n, path, data, mode)` | write a file (existence probed with `test -e`: exit 1 absent, anything else fails the test) | restores the old content/mode or deletes, then reads back content and mode (or checks absence) |
| `Listeners(t, n) []Listener` | `ss -ltnup` parsed; `Listener.Public()` | — |
| `Firewall(t, n) Firewall` | `ufw status` parsed; `Firewall.Allows("443/tcp")` | — |
| `IPTablesBlock(t, from, to)` | partition `from` from `to` (public + WG addresses; `ip6tables` for IPv6; every call with `-w`, waiting for the xtables lock) with rules tagged `e2e-<run>-<tag>` per call, so two tests blocking the same pair each hold their own | cleanup registered before the insert; deletes its own rules until `iptables -C` says they are gone; the runner also sweeps every rule tagged `e2e-<run>-` after each destructive package |
| `Kill(t, n, unit)` | SIGKILL every process of the unit | `systemctl reset-failed`, then back to its state before the test: started and waited until active, or stopped when it was not running |
| `StopService(t, n, unit)` | stop the unit | as `Kill` |
| `ClockSkew(t, n, offset)` | NTP off, clock moved | clock reset, NTP on, drift checked |
| `Tunnel(t, n, remoteAddr) string` | `ssh -L`: a runner loopback port carried to `remoteAddr` as `n` dials it (`"127.0.0.1:31003"`: the node's chain REST API); returns `"127.0.0.1:<port>"`; recorded as evidence | listener and SSH connection closed |
| `ReverseForward(t, n, localAddr) string` | `ssh -R`: `n` listens on a loopback port of its own and carries each connection back to `localAddr` in the test process (a mock APNs/Expo/ntfy upstream); returns the node-side `"127.0.0.1:<port>"` to configure on the node | node listener and SSH connection closed |

`fleet.CleanupContext(t) (ctx, cancel)` is a context independent of the test,
bounded by `CleanupBudget`, for work inside `t.Cleanup`; `fleet.ContextFor(t)`
is `t.Context()` while the test runs and a `CleanupContext` after (see Rules).
`fleet.Cluster` is the eval cluster type (`provision.EvalCluster` is an alias).

### oramacli

`Runner{Bin, Home, AgentSock, Env, Recorder, Test}`; the CLI's environment is
an allowlist: `PATH`, `LANG`, `LC_ALL`, `TERM`, `TMPDIR`, `TZ` from the
runner, then `HOME` (isolated), `RW_AGENT_SOCK` (the throwaway agent),
`ORAMA_E2E=1` and `Env`. Nothing else reaches it (no run secret, no
`SSH_AUTH_SOCK`, no `XDG_*`). It refuses to run when the socket is empty or
inside the real home. `GatewayHost`, `Wallet` and `Pacer` (nil: the run's
pacer) drive credential pacing (see "Pacing credential calls"). Without `SSH_AUTH_SOCK` the CLI's own SSH (node
commands) uses the run's key and must verify hosts against the run's pinned
`known_hosts`, never trust on first use.

Exit codes of the CLI: 0 OK, 1 failure, 2 usage, 3 auth, 4 not found,
5 unavailable, 6 conflict, 7 aborted. A gateway 401/403 surfaces as exit 1
unless the command wraps it as an auth error (3): assert on the exit code the
command documents, and on its stderr.

| Signature | Does |
|-----------|------|
| `(*Runner).Run(ctx, args...) (Result, error)` | `Result{Args, Stdout, Stderr, Exit, Duration}`; non-zero exit is not an error |
| `(*Runner).MustOK(t, args...) Result` | fails the test unless exit 0 (the failure message is redacted) |
| `(*Runner).For(t) *Runner` | evidence attributed to t |
| `(*Runner).Isolated(t) *Runner` | fresh HOME with the environment list and no credentials: use it for anything that switches "the current namespace" (`auth login --namespace`, `namespace delete`, `members`) |
| `DecodeJSON(res, &v) error` | decode JSON stdout. `--json` is not a global output switch: every command accepts it (a persistent root flag), but only commands that print through the CLI's printer honour it (`namespace list`, `monitor report`, `status`, `nodes`, `audit`, `node status`, ...); the others ignore it and print text, which `DecodeJSON` then refuses |
| `ForPreviousRelease(t, st, rec) *Runner` | the N-1 CLI for upgrade tests |
| `(*Runner).Start(ctx, args...) (*Proc, error)` | a long-running command, same checks, pacing, evidence and redaction as `Run`; `Proc.StdoutLines() <-chan string` (closed at EOF or when `Wait` returns; unread lines stay in `Result.Stdout`, and an unread channel never blocks the CLI), `Proc.Wait() (Result, error)` (records once; call it for every `Proc`), `Proc.Kill() error` |
| `(*Runner).NoWallet(t) *Runner` | a machine with no wallet: isolated HOME (as `Isolated`), `RW_AGENT_SOCK` a socket inside it that does not exist (never the real one, never empty), `Wallet` empty; `Check` refuses it if the socket appears. `orama auth login` then falls back to the device login |
| `(*Runner).RunWith(ctx, RunOpts, args...) (Result, error)`, `(*Runner).StartWith(ctx, RunOpts, args...) (*Proc, error)` | `RunOpts{Stdin []byte, StdinReader io.Reader, Dir string, Env []string}`: `Stdin` is written and recorded (redacted) — typed confirmations (`namespace rqlite import`), `y` to `app delete`/`domain remove`/`db delete` prompts; `StdinReader` streams (`StartWith` only, not recorded); `Dir` must exist inside the runner's HOME or the run's work dir (`Runner.WorkDir`, set by `harness.CLI`; `harness.WorkTemp(t)`), symlinks resolved; `Env` adds `KEY=VALUE` after the allowlist and refuses `HOME`, `RW_AGENT_SOCK`, `ORAMA_E2E`, `SSH_AUTH_SOCK`, `XDG_*`, `INFISICAL_*` and every run secret |
| `GoEnv(os.LookupEnv) []string` | the Go toolchain variables (`GOFLAGS`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOTOOLCHAIN`, proxies) for `RunOpts.Env` of `orama build`. `orama build` needs its source as the working directory: copy the tree into `harness.WorkTemp(t)` (the repository itself is outside the allowed directories) |
| `RedactArgs(args) []string` | masks the value of `--key`, `--api-key`, `--mnemonic` and every `--password*`, `--token*`, `--secret*` flag, in `--flag value` and `--flag=value` forms (a secret flag followed by another flag is a boolean and masks nothing); `--key-file` shows its path, whose contents are never read |
| `DeviceLogin(t, noWallet, namespace) *PendingLogin` | starts `orama auth login [--namespace ns]` on a `NoWallet` runner and returns once it printed its code (`Your code:` and the matching `orama auth approve <code>` line; there is no URL); `PendingLogin{UserCode, Namespace, Proc}`, `.Approve(t, approver) Result` / `.Deny(t, approver)` run `orama auth approve <code> [--namespace ns] [--deny]` as a runner with a wallet (`harness.CLI(t)`), `.Wait(t) Result` waits for the login (exit 0 approved, 3 refused), `.ApproveArgs()`; a cleanup kills and reaps it |

### gw

| Signature | Does |
|-----------|------|
| `ForFleet(t, f) *Client`, `(*Client).WithBase(url)`, `NamespaceURL(st, ns)` | clients for the public and the namespace gateway |
| `(*Client).Send(ctx, gw.Req) (*Response, error)` | `Req{Method, Path, Query, Header, Body, Bearer, APIKey, Host}`; header values are sent as given (duplicates allowed), body verbatim; a ctx with no deadline gets `RequestBudget` (30 s) plus the time the body takes at 2 Mbit/s, so a large upload is not timed out by the runner's uplink |
| `(*Client).MustSend(t, gw.Req) *Response` | fails only when the request could not be made; safe inside `t.Cleanup` (sends on `fleet.ContextFor(t)`) |
| `(*Client).Stream(ctx, gw.Req) (*StreamResp, error)` | a streaming (SSE) response once its headers arrive, body left open, no `RequestBudget` (the stream lasts as long as ctx); `Accept: text/event-stream` by default; `StreamResp{Status, Header}`, `.Next() (Event, error)` (`Event{ID, Event, Data, Retry}`, multi-line data joined with `\n`, comments skipped, `io.EOF` at the end), `.Events()`, `.Close()` (always call it: it records the exchange with every event read, once) |
| `(*Client).JSON(ctx, method, path, bearer, in, out) (*Response, error)` | non-2xx returns `*gw.StatusError` |
| `(*Response).Expect(t, status)`, `.Decode(&v)`, `.ErrorCode()` | assertions |
| `(*Client).Raw(ctx, []byte) ([]byte, error)` | bytes on a TLS socket: malformed request lines, CL+TE, bare LF |
| `(*Client).DialWS(ctx, pathAndQuery, token, header) (*websocket.Conn, *http.Response, error)` | gorilla WebSocket, same trust; a pinned client dials its node |
| `(*Client).PinTo(ip) *Client`, `.PinnedIP()` | dial one node's public IP for every request, WebSocket and `Raw`, keeping the URL, SNI and Host the gateway hostname and the CA pinned; evidence says `(pinned to <ip>)`; an address that does not parse fails every request |
| `(*Client).NamespacePinned(st, ns, ip) *Client` | `WithBase(NamespaceURL(st, ns)).PinTo(ip)` |
| `(*Client).Protect(secret) error` | register a credential obtained some other way with the redactor and the run's token registry |
| `(*Client).Unpaced() *Client`, `gw.Unpaced()` (a `NewUser` option) | no pacing and plain 429s, ONLY for rate-limiter tests (see "Pacing credential calls") |
| `(*Client).WithPacer(p) *Client`, `IsCredentialPath(path)`, `*PacingError` | explicit pacer (unit tests); the paced route set; the error of a 429 on a paced request (`errors.As` also finds its `*StatusError`) |
| `(*Client).Challenge / Verify / APIKey / Token / Refresh / Logout / Whoami / Sessions / EndSession / Devices / RevokeDevice` | each auth route, typed |
| `(*Client).SignIn(ctx, w, namespace, dev) (*Session, error)` | challenge → real EIP-191 signature → verify (device-bound when dev != nil) |
| `NewUser(t, f, namespace, gw.WithDevice(wallet.AlgEd25519)) *User` | a fresh wallet signed in (`gw.LobbyNamespace` for the lobby); logs out at cleanup |
| `(*User).Token()`, `(*Session).Stale(now)` | the current access token; a session two-thirds through its lifetime is refreshed first (with a device proof when device-bound), so a test that outlives one access token keeps a valid credential; a failed refresh is in the evidence and the stale token is returned |
| `NewDevice(t, alg) *wallet.Device` | a device key |

### ns

`ns.New(t, f, ns.Options{Via: ns.ViaUser | ns.ViaOperator, Name, DeviceAlg}) *Namespace`
creates a namespace, waits until the provisioning status says ready **and** a
real request through `https://ns-<name>.<base>` succeeds, and deletes it at
cleanup, verifying the teardown (status 404, gateway no longer serving).
Every `ns.New` first takes one **fleet-wide live-namespace slot**, shared by
every package of the run through flock'd files in `<work dir>/ns-slots/`
(a crashed process releases its slots with its descriptors), and releases
it after the namespace's teardown. The cap is `E2E_MAX_LIVE_NAMESPACES`,
default `ns.DefaultMaxLive(len(nodes))`: 20 port blocks per node, 3 nodes per
namespace, minus 4 of headroom = **16** on three nodes. On the stagenet
target the default is `ns.StagenetMaxLive` (**4**): its nodes are shared, small
VPSs that also serve the owner's own namespaces. A test that creates
several namespaces calls `ns.Hold(t, f, n)` first (all its slots at once; the
next `n` `ns.New` use them), so it never holds some slots while waiting for
the rest. **`ns.New` fails a test that already holds a slot and has no held
one left** ("call ns.Hold first"), and so does a second `Hold`: either would
wait for a slot while holding one. Waiters are served in arrival order across
the run (a flock'd ticket per waiter in `ns-slots/`), so a `Hold` of several
slots is never starved by a stream of single ones.
`ns.Reserve(ctx, workDir, count, capacity) (*Slots, error)` and
`(*Slots).Release()` are the primitive. `features/internal/tenancy` keeps its
own per-package cap on top, and its `Reserve`/`Namespaces` take the fleet
slots with `ns.Hold` before anything is created.
**A namespace is never left behind.** A test's own cleanup runs once, and a
cleanup that meets a refusal the gateway says is temporary (`retry shortly`,
`retry the delete`, a sign-in rate limited for a minute, a TLS timeout) used to
give up and leave the namespace holding port blocks and processes on every
node (stagenet, 2026-10-01: nine leaked `e2e-*` namespaces). So three layers
stand behind each other:

1. The teardown (`deleteViaUser`, `deleteViaOperator`) retries until
   `TeardownBudget`, and a delete that answered an error but took effect is
   noticed (status 404 / no longer listed) instead of repeated.
2. **The namespace ledger** (`harness/nsledger`). `ns.UniqueName` (every test
   names its namespaces with it) records the name, with the time, in
   `namespaces.ledger` in the package's evidence directory *before* the namespace
   exists (only `e2e-` names, only harness-generated ones: a name a test picks
   itself through `ns.Options.Name` may be a namespace it does not own, and is not
   tracked; the remover also refuses any name without the `e2e-` prefix, whatever
   a ledger line says). After every package process exits, whatever its exit (a pass, a
   failure, a stage budget cut, a kill), the runner (`Runner.AfterPackage`,
   `cmd/e2e-fleet/nsleftovers.go`) removes each recorded namespace that still
   exists with `orama cluster namespace remove <name> --reason ... --force` as the
   run's operator (it works for a throwaway wallet's namespace too), retrying a
   refusal for 10 minutes per namespace (30 for the package) and marking it gone
   in the ledger. A refusal waiting cannot fix (401, 403 and other 4xx but 404, 408,
   409 and 429) is not retried; a 404 means gone. A torn ledger line is reported
   and skipped, the others are still removed. A
   namespace it cannot remove fails the package's runner error and stays in the
   ledger. Tests that create a namespace without `ns.UniqueName` must record it
   with `nsledger.RecordFromEnv(os.LookupEnv, name, time.Now())`.
3. `e2e-fleet sweep-namespaces [--max-age 4h] [--listed]` removes what ledgers
   still hold of runs whose runner died before layer 2 ran (a SIGKILL of the
   runner, a closed session): on the stagenet target every `stagenet-*` run
   directory next to the state's, otherwise the state's own run. It signs the
   operator in on stagenet, and only touches ledger entries older than
   `--max-age` (default 4h: over the longest stage, 180m, plus the 10m stop
   grace). Every `run` and `test` holds a shared flock on `<run dir>/run.lock`
   while it runs; the sweep skips the run directories whose lock is held, so a
   live package's namespaces are never taken whatever their age, and `--listed` is
   refused while any run directory is held. `--listed` also removes every `e2e-*` namespace the operator's
   wallet owns that no ledger names (leftovers from before the ledger; their age
   is unknown, so run it only when no test run is in progress). Unlike `sweep`
   it destroys no server and works on the stagenet state.

`ViaUser` (default) creates it as a fresh wallet (adding it to the creator
allowlist when the cluster's mode is `allowlist`; failing in `operators` mode);
`Namespace.Owner` is that wallet signed in to the namespace, `Namespace.Client`
aims at the namespace gateway. `ViaOperator` uses `orama namespace create`;
`Namespace.CLI` is signed in to it in an isolated HOME.

### monitor

`orama monitor report --json` read as the operator reads the cluster
(`harness/monitor`; shapes copied from core, pinned by a drift test on a
report the real code wrote).

| Signature | Does |
|-----------|------|
| `Fetch(t, cli, env) *Report`, `Get(ctx, cli, env) (*Report, error)`, `Parse(raw)` | run `orama monitor report --env <env> --json` and decode (`Get` for `eventually` loops) |
| `Report{Meta, Summary, Alerts, Nodes}`, `Node{Host, Role, Status, Error, ReportAgeSec, Report}` | `Report` is the node report subset: rqlite, gateway, wireguard, services, dns, network, system, chain, version |
| `(*Report).Converged(n) error` | n nodes, quorum, one leader, full mesh, no critical alert, no crash loop, fresh reports |
| `(*Report).LeaderAgreement() error` | every responsive node names the same leader (split brain by name) |
| `(*Report).Forgotten(wgIP) error` | gone from the node list and from every node's WireGuard peers |
| `(*Report).Serving() error` | every gateway answers and every nameserver runs CoreDNS, raft aside |

### pace

`(*pace.Pacer).WaitFull(ctx, host, bucket)` collects a whole burst of the
run's shared bucket (draining it as it refills, so a busy run cannot starve
it) and holds it: nothing paced reaches the gateway meanwhile, and because the
pacer's budgets sit under the product's, the product's bucket for this
address has refilled once it returns. A rate-limiter test calls it (through
`edge.Quiesce`) before a flood and from a cleanup after. On ctx's end it gives
the collected tokens back.

### evidence

`evidence.RunRecorded(t, rec, name, cmd *exec.Cmd) (ExecResult, error)` runs a
local subprocess that is not the CLI under test (vitest, `go build`, tsx, a
scanner) and records it (kind `exec`): the command line, working directory,
exit, duration and output, redacted and bounded. `ExecResult{Stdout, Stderr,
Exit, Duration}`; a non-zero exit is not an error. Leave `cmd.Stdout`/`Stderr`
unset. Pass `f.Recorder()`.

### eventually

| Signature | Does |
|-----------|------|
| `Require(t, interval, timeout, "what", func() (bool, error))` | Fatal with the last observation on timeout |
| `Eventually(t, ...) bool` | Error instead of Fatal |
| `Poll(ctx, interval, timeout, "what", fn) error` | the primitive; `*TimeoutError{Last}` |
| `Stop(err)` | return from fn to give up at once (a 403 will not become 200) |

A plain error returned by fn is the current observation ("status=provisioning")
and is reported on timeout.

### wallet

| Signature | Does |
|-----------|------|
| `NewEVM() (*EVM, error)`; `.Address()`; `.Sign(msg) (string, error)` | secp256k1, EIP-191 personal_sign, 0x hex, v=27/28 |
| `RecoverAddress(msg, sig)` | check a signature |
| `ParseSIWE(text)`, `Mutate(text, func(*SIWEMessage))` | the gateway's own SIWE/SIWS grammar; forge domain/time/nonce for negative tests |
| `NewEd25519Device()`, `NewES256Device()` | `.ID()` (RFC 7638), `.PublicJWK()`, `.PrivateJWK()` (Ed25519, for `orama auth login --device-key`), `.Sign`, `.SignDER` |
| `(*Device).NewProof(action, ns, binding)`, `.ProofAt(action, ns, binding, at, id)` | `device_proof` for `wallet.ProofRefresh`/`ProofApprove`/`ProofClaim`/`ProofRevoke`/`ProofEndSession` |
| `NewSolana()`; `.Address()`; `.Sign(msg)` | SIWS: base58 address, base64 Ed25519 signature |

---

## Examples

A CLI test:

```go
func TestNamespaceList_showsNewNamespace(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	res := n.CLI.MustOK(t, "namespace", "list", "--json")
	var rows []struct{ Name, Cluster string }
	if err := oramacli.DecodeJSON(res, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == n.Name && r.Cluster == "ready" {
			return
		}
	}
	t.Fatalf("%s is not listed ready: %s", n.Name, res.Stdout)
}
```

An HTTP negative test:

```go
func TestQuery_otherNamespaceTokenRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	a := ns.New(t, f, ns.Options{})
	b := ns.New(t, f, ns.Options{})
	for name, bearer := range map[string]string{"none": "", "garbage": "x.y.z", "other namespace": b.Owner.Token()} {
		resp := a.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/rqlite/query", Bearer: bearer,
			Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"sql":"SELECT 1"}`)})
		if resp.Status != http.StatusUnauthorized && resp.Status != http.StatusForbidden {
			t.Errorf("%s: want 401/403, got %d: %s", name, resp.Status, resp.Body)
		}
	}
}
```

A node-level test (destructive package) with SSH and automatic cleanup:

```go
func TestOlric_nodeDownCacheStillServes(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	victim := f.Node(t, "node-2")
	f.StopService(t, victim, "orama-namespace-olric@"+n.Name+".service") // restarted at cleanup
	eventually.Require(t, 2*time.Second, 2*time.Minute, "cache put with a node down", func() (bool, error) {
		_, err := n.Client.JSON(t.Context(), http.MethodPost, "/v1/cache/put", n.Owner.Token(),
			map[string]any{"dmap": "e2e", "key": "k", "value": "v"}, nil)
		return err == nil, err
	})
}
```

`features/smoke/` is a complete working package: health and status shape
(no address leak), CLI version against the fleet, TLS chained to the pinned
roots only, HTTP/1.1, and a node file round trip with cleanup.

---

## Stages

`stages/stages.yaml`: 1 bootstrap, 2 namespaces, 3 auth, 4 data-plane,
5 deployments-serverless, 6 realtime, 7 security-audit, 8 chain, 9 ops,
10 upgrade, 11 chaos-soak. Each stage runs
`go test -tags e2e_fleet -json -count=1 -timeout <stage timeout + StopGrace> ./features/<id>`
for its packages: non-destructive ones in parallel, then each destructive one
alone. When the stage timeout is spent the runner sends SIGINT to the package's
process group, so running tests finish and their `t.Cleanup`s restore the fleet,
and SIGKILL `StopGrace` (10 minutes) later; an overrun is reported as an error
and never as a pass. A stage's failures never stop later stages. `e2e-fleet test --stage N`
runs one stage against an existing fleet and replaces only that stage in
`stages-state.json` (the other stages' results stay in the report; each save
re-reads the file under a lock, so runners of one artifact dir running at once
keep each other's results; a full run starts a new timeline with its first
stage);
`--resume` skips the stages it records as completed (a completed stage is
never re-run, failed or not). A package run replaces its earlier attempt
whole: output and evidence dir. After every destructive package, even on an
interrupted run, the runner removes every iptables/ip6tables rule tagged
`e2e-<run>-` from every node and turns NTP back on (clock set to the runner's
time) where a test left it off; a failure there fails that package. An
interrupted run does not re-run failures to label them.

Each package runs in a process group of its own. When the run is interrupted
the group gets SIGINT (the go command waits for its test binary, whose
`harness.Main` lets running tests clean up), up to 10 minutes to finish, then
SIGKILL.

Extra servers: `harness.ExtraNode(t, "extra-join", "hel1")` in the test, and
`requires.extra_nodes` in the manifest (an eval cluster from
`harness.ExtraCluster` counts as one). The broker creates them with the
runner's credentials. `e2e-fleet hook provision` names an extra `extra-<N>`,
one above the highest `extra-<n>` the state lists; extras the broker created
for feature processes are not in the state, and a name removed from the top
can come back, so pass `--name` when that matters (the broker refuses a name
already in use). The core/e2e lifecycle hooks map onto
the runner: `ORAMA_LIFECYCLE_DESTROY="e2e-fleet hook destroy"`,
`ORAMA_LIFECYCLE_BREAK="e2e-fleet hook break"`,
`ORAMA_LIFECYCLE_PROVISION="e2e-fleet hook provision"` (prints the new IP), all
reading `E2E_FLEET_STATE`.

## Running against stagenet

The suite can run stage by stage against the existing stagenet cluster instead
of fresh Hetzner servers. The runner **only tests** there: it never creates,
changes, sweeps or destroys a server, and it holds no cloud credential.

```bash
cd e2e
go run ./cmd/e2e-fleet target stagenet --out /tmp/stagenet-state.json   # read-only: ssh-keyscan + local files
E2E_FLEET_STATE=/tmp/stagenet-state.json go run ./cmd/e2e-fleet test --stage 1
```

`test --features a,b` runs only those packages (of every stage, or of `--stage N`)
and keeps the other packages' results in the artifact dir's timeline and report: a
fix to one package is rechecked in minutes instead of a whole stage. An unknown
package name is a usage error.

`target stagenet` writes the state (`"target": "stagenet"`), five nodes in join
order: `node-1` mew `57.129.166.16` (WG `10.0.0.1`), `node-2` mewtwo `57.129.166.17`
(WG `10.0.0.2`) and `node-3` gengar `161.97.184.199` (WG `10.0.0.3`), all nameservers;
`node-4` magicarp `161.97.184.202` (WG `10.0.0.4`) and `node-5` froakie `161.97.151.255`
(WG `10.0.0.5`), plain nodes. mew and mewtwo are OVH (ASN 16276, Ubuntu 26.04, systemd
259 with BPF_FRAMEWORK, login user `ubuntu`); gengar, magicarp and froakie are Contabo
(ASN 51167, Ubuntu 24.04, systemd 255 without BPF_FRAMEWORK, login user `root`),
so `SocketBindDeny` is enforced on the first two and only accepted on the other
three; the SSH key `~/.ssh/debros-nodes`;
the CA bundle `~/orama-stagenet-handoff/le-roots.pem` (Let's Encrypt production's ISRG Root X1 and X2: stagenet serves production certificates, a fleet the run provisions staging ones); the CLI HOME
`~/orama-stagenet-handoff/cli-home` (its `.orama/environments.json` must hold the
`stagenet` env); `core/bin/orama` as the CLI under test (build it first); the
operator address from the dev RootWallet agent's `~/rwdev/ready.json` (that agent,
`~/rwdev/agent.sock`, must be running and unlocked); the chain id the running
chain reports through the gateway's `/v1/chain/status` (a `--chain-id` given must
match it, since every reset starts a new chain id) and the run id
`stagenet-<yyyymmdd>-<hhmmss>`; artifacts in `e2e/artifacts/stagenet-<ts>`. The
host keys come from `ssh-keyscan` of the five addresses and are written to
`<state>.known_hosts` only when your `~/.ssh/known_hosts` holds the same key for
that address: a changed key, or an address your file has never seen, is refused
(`ssh` to it once and verify the fingerprint first).

**Safety pins.** Every loader of a state (`test`, `report`, `harness.Main`) runs
`fleet.CheckState`, which for this target accepts exactly: env `stagenet`, base
domain `stagenet.dbrsteting.bid`, gateway `https://stagenet.dbrsteting.bid`, a chain id
matching `^orama-stagenet-[0-9]+$`, the five node addresses above and nothing
else (no extra, no probe), the agent socket `$HOME/rwdev/agent.sock` (never
under `~/.rootwallet`), the CLI HOME, CA bundle and SSH key above. Anything else,
including devnet/testnet names and domains or a fleet state carrying
`"target": "stagenet"`, is refused with every mismatch listed. The constants
live in `harness/config/stagenet.go`. `run`, `provision`, `teardown`, `sweep`
and `hook destroy|break|provision` refuse a stagenet state (`sweep-namespaces`
works on it: it removes test namespaces, never a server or cloud resource) when
`E2E_FLEET_STATE` names it; `test` starts no broker (`E2E_BROKER_SOCK` is unset),
so `harness.ExtraNode`, `ExtraCluster`, `DNSTXT`, `CustomDomain` and
`harness.Broker` skip the test with `harness.SkipNotApplicable` (the stagenet
target has no extra server, probe VM or DNS broker), as does the probe vantage of
`external-vantage`; the `provision` functions themselves
(`AddExtra/RemoveExtra/AddEvalCluster/DestroyNode/BreakUpgrade/RestoreUpgrade/UpgradeToHead`)
still return the "not available on the stagenet target" error to any other
caller. After a destructive package the
runner still removes the iptables rules tagged `e2e-<run>-` on every node of the target and
turns NTP back on where a test left it off (`Fleet.RestoreNodes`).

**What runs.** Stages 1-7 and 9 are tests of the running cluster. A test whose
premise stagenet lacks by design **skips** with `harness.SkipNotApplicable` (the
report lists it as not covered, with the reason) instead of failing, so a run's
failures are only real ones; on a fleet run every one of them still runs and
asserts. The skips:

- an extra server, the probe VM or the DNS broker (`boot-lifecycle`,
  `chaos-lifecycle`, `install-extra`, `invite-join-destructive`,
  `namespace-backup-chaos`, `release-tuf`, `external-vantage`, the `dns-tls` CLI
  test): skipped in `ExtraNode`, `ExtraCluster`, `DNSTXT`, `CustomDomain`,
  `Broker` and `external-vantage`'s `probes`;
- the release archives, which the stagenet state does not carry
  (`install`, `install-extra`, `release-checks`, `release-tuf`, `rollout-upgrade`,
  `scanners` secrets scan): skipped by `harness.RequireArchive`, called in
  `infra.ReadArchiveFile`, `ArchiveManifest`, `RewriteArchive`, `RunningArchive` and
  the tests that read `State.ArchivePath` directly. `rollout-upgrade`'s
  broken-node test also skips: `provision.BreakUpgrade` never runs on stagenet.
  `cli-env-auth-misc`'s rollout tests pass a dummy archive and run.

**Tenant deployments in the listener audit (every target).** A deployment binds
whatever address its code picks, on the one port its unit allows. The audit
excuses such a socket only when the process is in an
`orama-deploy-<runtime>@<instance>.service` cgroup, the port is in 10200-19999
(`fleet.DeployPortMin/Max`, pinned to `privhelper.DeployPortMin/Max` by a test)
ufw is active with a default deny of incoming traffic (`ufw status verbose`),
and no ufw allow or limit rule opens it (a bare `Anywhere` rule opens every
port; an `ALLOW FWD` rule opens nothing on the host); it logs each one. docs/SECURITY.md, "Tenant
deployments", says why.

**What the stagenet nodes have that Orama did not install.** The public-edge
audits (`wireguard-firewall`'s listener audit and port scan, `install`'s
firewall rules) assert that only Orama's ports are open. Two things on the
stagenet are legitimately more, and the audits know them differently:

- The **global layer** (`orama global install --colocated`): the chain P2P
  (31000 tcp+udp), the public Kubo swarm (31010 tcp+udp) and the storage
  provider (31013 tcp) are public on purpose, DNAT-ed into the `orama-global`
  namespace and allowed by ufw rules tagged `orama-global`, plus the Tor
  relay's ORPort and a dirauth's DirPort; the ports come from
  `core/pkg/constants`. The port scan (open from the internet) excuses a port a
  tagged rule allows or forwards (`fleet.GlobalPublicPorts`). The listener
  audit, which looks at sockets on the host, excuses only a direct allow
  (`fleet.GlobalHostPorts`, a global-only machine): a forwarded port is served
  inside the namespace, so a host listener on it fails. A tagged rule on any
  other port is not excused.
- **Operator and image extras**, declared in `harness/config/hostextras.go`
  (`StagenetHostListeners`, `StagenetHostUFWRules`). None are declared on the
  current five nodes: read-only checks found only loopback stubs
  (systemd-resolved, chrony), systemd-networkd's DHCP client on UDP 68, and no
  untagged ufw rule. A declaration names the node (empty = every node), the
  process, the protocol, the port (0 = any), optionally the network the bound
  address must lie in, and why it is there. The audit logs every use of one, and **fails**
  when a declaration no longer matches anything on the node, so the list cannot
  outlive the thing it excuses. A fleet run has no declarations: nothing is
  excused there. To admit a new extra, add a declaration with its reason; an
  Orama process is never declared.

Stages 10 and 11 disturb the live cluster (upgrades, partitions, kills): run
them only on purpose.

**Chain stage (8).** The chain helpers (`features/internal/chain`) run the real
`oramad` on the node as the chain user, exactly as `e2e/scripts/chain-deploy.sh`
lays it out, and stagenet uses the same binary, home, unit and user
(`/usr/lib/orama-global/bin/oramad`, `/var/lib/orama-global/chain`,
`orama-global-chain.service`, `orama-chain`). What differs, and what is target-aware
now: the RPC and REST address. A fleet validator listens on `127.0.0.1:31001/31003`;
stagenet's chain is inside the `orama-global` netns and answers on
`198.18.0.2:31001` (RPC), `:31003` (REST), `:31015` (indexer) from the host.
`Chain.Host()`, `RPC()` and `RPCHTTP()` return the right address for the state's
target, and `Chain.Tunnel` forwards to it; `Chain.OramadCmd` runs `oramad` inside
the `orama-global` netns on stagenet (`ip netns exec … runuser -u orama-chain`),
because the host ruleset lets only root and the cluster's account reach those
ports through the veth; the chain id check accepts a devnet id
on a fleet run and a stagenet id on this target. What a stagenet node does not have, and the chain tests skip
with `SkipNotApplicable` for:

- the validator operator keys in each node's `test` keyring under the chain home
  (`chain-deploy.sh` creates and funds them; a stagenet node's keyring is not
  assumed to hold them): `Chain.Validator`, and so `FundedValidator` and every
  test that signs with a validator key, skips on stagenet;
- a fresh run chain's genesis (zero supply, an empty shielded pool, a validator
  operator with no fee balance, the E2E epoch length): `chain.RequireFreshChain`
  skips `TestEmission_devnetShortEpochParams`, `TestShieldedQueries_poolStartsEmptyAndInvariantsHold`,
  `TestChainQuery_runsAnyModuleQuery` and `TestFeeBalance_queryAnswers`;
- a chain on the host's loopback (the fleet layout, not the netns):
  `chain-global`'s `TestCoHost_p2pOnWireGuardRPCOnLoopback`, the REST probe
  (`requireREST`, 127.0.0.1:31003) and `chain-deploy.sh status|invariants`
  (`runDeployScript`), `open-network-phases` B3 (after its unit-account check) and
  B4 (after the bind), and the chain ports in `wireguard-firewall`'s
  `TestListeners_internalsOnTheirAddress`, which are not asserted on stagenet.

## Evidence and the report

Every CLI invocation (`oramacli`), HTTP/WebSocket/SSE exchange (`gw`), SSH
command, file transfer or port forward (`fleet`) and other subprocess
(`evidence.RunRecorded`) is appended, redacted and bounded, to the
package run's `<artifacts>/evidence/stage-NN-<feature>/<feature>.jsonl`,
attributed to the test (re-runs record under `rerun/evidence/`, which the
report never reads). For a failed test the report shows its last ten records.

Redaction masks the run's secrets, every credential minted during the run,
each node's own secrets (cluster secret, RQLite password and auth file,
secrets encryption key, TURN and API-key HMAC secrets, swarm key: read once
per node before anything is collected; a node whose secrets cannot be read is
not collected from, `nodes/<node>/withheld.txt` says why), and recognised
shapes: Authorization, Cookie/Set-Cookie and any `X-*-Token`/`X-*-Key`/`X-*-Auth`
header value, JSON members whose key names a token, secret, password,
passphrase, API, private, signing, encryption or HMAC key, privkey, mnemonic,
seed, PSK or swarm key (any case, plain, escaped or double-escaped JSON, string,
number or array-of-numbers values; the JWK `"d"` and `"device_code"` only as
quoted keys, so `"id"`, `"node_id"` and `"cid"` are kept), `KEY=value`, YAML
and `--flag value` forms, `-u`/`--user user:password`, mid-line
`cluster secret:`/`rqlite password=` and the like, `?key=`/`&sig=` query
values, `user:password@` in URLs, WireGuard `PrivateKey`, the IPFS swarm key
body, PEM private keys, `0x` + 64 hex digits (an EVM private key; a
transaction hash looks the same and is masked too), a 12-24 word phrase after
a mnemonic/seed/phrase label, JWTs and Orama API keys. Redaction fails
closed: when the run's redactor cannot be loaded, or a file cannot be
rewritten redacted, that package's `gotest/` output and stderr are replaced
by `[withheld: redaction failed]` (the next package loads the redactor
afresh). Over-long lines are cut and marked, never a reason to skip
redaction. Credentials a feature
process mints are appended to the run's token registry (`redact-tokens`,
mode 0600, beside `state.json`, never in the artifact dir). A redactor holds
at most 10000 literal values, and JWTs and Orama API keys apart from them, up
to 100000, replaced in one pass; past either bound it fails closed. The runner reads
it to redact each package's `gotest/` output and stderr after the package
ends, the collected artifacts, and everything the report shows. The
provisioning logs (`provision-NN-*.log`), the errors quoting a command's
output, the broker's answers and log lines and whatever the runner prints go
through the same redaction; the test agent's log stays in the work dir
(`agent.log`; the agent writes it directly) and the artifacts get a redacted
copy, `collected/agent.log`. Before teardown the runner collects
journals of every `orama-*`, `caddy*`, `coredns*`, `wg-quick@*` unit,
`orama node report --json`, listeners, WireGuard (no keys), ufw, disk, clock
and (when the run has a chain) chain status from every node, and `orama monitor report --json` and
`orama inspect` from the runner, into `<artifacts>/collected/`: from every
member of the state and every extra or eval cluster server the broker created
that is still up (found by the run's label).

The artifact dir then holds `report.html` (self-contained), `report.json`,
`report.junit.xml`, `summary.txt` (the push notification line) and, with
`--bug-drafts`, `bugboard-drafts.json` (drafts only; nothing is filed).
Verdicts: **PASS** (everything passed, nothing skipped, coverage gate OK),
**INCOMPLETE** (nothing failed, something not covered: a skip, a skipped
subtest of a passing test included, a feature that never ran or executed no
test, a coverage gap), **FAIL** (a test, package or
run step failed; a package whose `go test` exited non-zero fails with the tail
of its stderr even when every parsed test passed; a failed teardown is a run
error; a flaky failure is still a failure; a package during which the
host running the suite slept for 30s or more fails with "the host running the
suite was asleep for ..." instead of being taken as a verdict on the fleet).
Exit codes 0, 3, 1 (2 for usage).

**The runner's host must stay awake.** A sleeping laptop freezes the tests
while the fleet keeps running: connections drop, deadlines and timings span
the sleep, and the run fills with failures the fleet never had. On macOS
`test` and `run` hold `caffeinate -i -s -w <runner pid>` for their lifetime,
which stops idle and system sleep on AC power; closing the lid on battery
still sleeps. Every package compares wall-clock and monotonic time (the
monotonic clock stops while the host sleeps) and reports any sleep it
could not prevent, as above.

## Scanners

`features/scanners` runs govulncheck, staticcheck and gosec on the `core`,
`chain` and `e2e` modules, plus the secret, audit, fuzz and race scans. A
scanner that is not installed is not covered, never a pass. govulncheck
(`golang.org/x/vuln/cmd/govulncheck`) and staticcheck are not taken from the
runner: both are pinned in `golang_test.go` and run with `go run`, so the
Go toolchain that builds the modules builds the scanner. An installed binary
is as old as its last install and refuses a module whose `go` directive is
newer ("package requires newer Go version").

**Accepted vulnerabilities.** govulncheck runs with `-format json`, and the
vulnerabilities whose function the code reaches (a finding with a function in
its trace; an imported package or a required module alone does not count) are
compared with `features/scanners/govulncheck-accepted.yaml`, embedded in the
test. Each entry names a `module` (`core`, `chain` or `e2e`), an `id`
(`GO-YYYY-NNNN`), a `reason` (why it is unfixable or not exploitable here: the
call path and the mitigation, specifically) and a `review_by` date no more than
90 days away. The test fails on:

- a reachable vulnerability that is not listed: upgrade the dependency, or
  list it with a reason;
- a listed entry govulncheck no longer reports (stale): remove it;
- a listed entry whose `review_by` has passed: fix it, or review the reason and
  move the date;
- a file that does not parse (unknown field, missing reason, bad ID or date,
  a duplicate, a date over 90 days out, a module the scan does not cover).

A govulncheck run that fails or prints no `config` message is a failure, never
a clean scan. The parsing and matching (`Parse`, `Called` and `Judge` in package
`harness/vulnaccept`) have unit tests that need no fleet:
`cd e2e && go test ./harness/vulnaccept/`. The reasons are the same analysis as
"Chain dependency advisories" in `docs/SECURITY.md`; change both together.

## Coverage gate

`make e2e-coverage` enumerates the universe (above), matches it against every
manifest and `waivers.yaml`, and fails on: an uncovered item, an unknown covers
entry, a waiver for a covered or no-longer-shipped item, a waiver without
reason or trigger. `e2e-fleet coverage --json` prints the whole matrix.

`make test` runs `e2e-lint` and `e2e-coverage`. **The gate is not blocking yet**:
the Makefile sets `E2E_COVERAGE_ENFORCE ?= 0` until the feature packages land,
and the command then prints the list and "not blocking". The integrator flips
the default to `1` in the top-level Makefile once every item is covered or
waived; the command itself enforces by default (`E2E_COVERAGE_ENFORCE` unset).

### Waivers

```yaml
waivers:
  - id: msg:orama.token.v1.MsgMint
    reason: chain transactions need RootWallet to sign orama-tx headless
    trigger: Root Wallet task 2857 ships
```

A waiver is for what cannot be tested yet, never for what is inconvenient.
Remove it in the same change that adds the covering test (the gate fails on
stale waivers).

## Secrets and safety

- Secrets come only from `infisical run` (project `orama-e2e`, env `e2e`):
  `HCLOUD_TOKEN`, `CF_API_TOKEN`, `CF_ZONE`. Missing ones are named, never
  printed. Neither the CLI under test nor any feature process sees them.
  **`HCLOUD_TOKEN` must belong to a Hetzner project used only by e2e runs**:
  teardown and sweep delete by label, and every delete is checked against the
  label and name first (below), but a project shared with anything else is
  one bug away from losing it.
- **Sealing.** `e2e-fleet run` and `e2e-fleet test` re-execute themselves at
  start with every secret variable (`secrets.SecretEnvNames`, `INFISICAL_*`)
  removed from their environment; the values cross the `exec` in an
  inherited pipe (`E2E_SEALED_FD`, the fd number only) and live in memory
  (`secrets.Seal`, read with `secrets.LookupEnv`). The runner's
  `/proc/<pid>/environ` (`ps -E` on macOS) and the environment every child
  inherits hold none of them.
- **The broker is a child process.** `run` and `test` start
  `e2e-fleet broker-serve` in a process group of its own; it receives
  `HCLOUD_TOKEN` and `CF_API_TOKEN` over an inherited pipe on fd 3, never in
  argv or its environment (its `/proc/<pid>/environ` holds neither), serves
  `<work dir>/broker/broker.sock`, prints `ready <socket>` and serves until
  the runner closes its stdin. It ignores SIGINT/SIGTERM/SIGHUP: an interrupt
  does not stop it, so features clean up through it during their stop grace;
  the runner stops it once the stages are over. The directory must be the
  runner's own (owner uid checked) and 0700, the socket 0600. Operations,
  each scoped to the run: `dns.txt.set`/`dns.txt.delete` (a name inside
  `e2e-<run>.<zone>` or a cluster subdomain `e2e-<run>-<label>.<zone>`;
  anything else is refused before Cloudflare is called; at most 64 TXT names
  held at once), `dns.records.list` (the run's own records),
  `extra.add`/`extra.remove` (extras only, never a core node or probe; one
  name at a time), `cluster.add`/`cluster.remove` (eval clusters it
  installed). Extras, clusters and adds in flight together are capped at
  `E2E_BROKER_MAX_SERVERS` (default 3, `manifest.MaxExtraNodes`; raise it
  when a stage runs several extra-hungry features in parallel). A connection
  has 10 s to send its request, and 8 are served at once (others wait in the
  backlog). In a feature process `provision.AddExtra`, `RemoveExtra`,
  `DestroyNode` (extras only), `AddEvalCluster` and `RemoveEvalCluster` go
  through it; the switch is `E2E_BROKER_SOCK` being set, never a missing
  token. A process started with `E2E_BROKER_SOCK` set refuses to serve one.
  Its answers and log lines are redacted with the run's secrets and token
  registry (withheld when the registry cannot be read). Teardown deletes
  every record of the run's cluster subdomains as well as its own subdomain.
- **Feature environment is an allowlist** (`stages.FeatureEnv`): `PATH`,
  `LANG`, `LC_ALL`, `TERM`, `TMPDIR`, `TZ`, `GOCACHE`, `GOMODCACHE`, `GOPATH`,
  `GOTOOLCHAIN`, the proxies (`HTTP(S)_PROXY`, `NO_PROXY`, either case, with
  any `user:password@` removed; an unparsable one with an `@` is dropped) and
  these run settings by name: `E2E_STRICT`, `E2E_FLEET_STATE`,
  `E2E_EVIDENCE_DIR`, `E2E_BROKER_SOCK`, `E2E_PACE_*` (the four),
  `E2E_MAX_LIVE_NAMESPACES`, `E2E_REPO_ROOT`, `E2E_SOAK_MINUTES`,
  `E2E_PERF_REGRESSION_PCT`, `E2E_BASELINE_FILE`, `E2E_INSTALL_PREVIOUS`,
  `E2E_ORAMA_TX_SIGNING`, `E2E_ORAMAOS_IMAGE`, `E2E_ORAMAOS_OVMF`. `GOFLAGS`,
  `GOPROXY`, `GOSUMDB`, `GONOSUMDB`, `GOPRIVATE`, `GONOPROXY` and
  `GOINSECURE` pass only with `E2E_ALLOW_GO_ENV=1`; otherwise the go command
  uses its default proxy and checksum database, and the runner runs
  `go mod download` for the e2e module first, so the feature builds find
  their dependencies in the module cache it resolved. Never `SSH_AUTH_SOCK`,
  `XDG_*`, `INFISICAL_*` or a secret variable. The runner resolves `GOPATH`,
  `GOMODCACHE` and `GOCACHE` with `go env` when unset, and adds
  `E2E_FLEET_STATE`, `E2E_STRICT`, `E2E_EVIDENCE_DIR`, `E2E_BROKER_SOCK` and
  **`HOME=<state file without its extension>.feature-home`**: an empty 0700
  directory beside the state, recreated for each `run`/`test`, which is neither
  the owner's home nor the test agent's, so go, git, pnpm and tinygo have a home
  to write to. Two runs with their own state files can run at once.
- `E2E_SANDBOX=1` (Linux, needs `bwrap`) runs every feature package under
  bubblewrap with the owner's real home hidden behind an empty tmpfs and only
  the repository, the work dir and the Go caches bound back. It is
  best-effort (see "Residual risks"); on another OS, or without `bwrap`, the
  run refuses instead of running unsandboxed.
- **SSH source.** The firewall lets SSH in only from `E2E_RUNNER_CIDR` (the
  public address the runner's traffic leaves from, e.g. `203.0.113.7/32`; the
  runner does not ask a third party for it). Without it the run is refused,
  unless `E2E_ALLOW_OPEN_SSH=1` opens SSH to `0.0.0.0/0,::/0` on purpose.
- **Deletes are checked.** Before `DestroyNode` or an extra's removal
  deletes a server, it reads it back and refuses unless it is labelled
  `e2e-run=<this run>` and named `e2e-<run>-...` (exactly
  `e2e-<run>-<extra>` for an extra). Down lists the run's servers, firewalls
  and SSH keys by label once more at the end and fails when anything is left.
  A failed `Up` tears down by label only when it registered something itself
  (`provision.UpError.Owned`, `provision.OwnsResources`): a run id that
  already labels another fleet is refused in the preflight and never torn
  down.
- **TTL and sweep.** The `e2e-ttl` label is the stage plan's worst case (each
  stage's timeout once for its parallel packages plus once per destructive
  package, `stages.WorstCase`) plus 3 h for provisioning, re-runs,
  collection and teardown; `E2E_TTL` may raise it, never lower it. The sweep
  deletes only servers, firewalls and SSH keys named `e2e-...` whose
  `e2e-run` label has a run id's shape, when they are past their own
  `e2e-ttl` (or, without one, older than `--max-age`); a run's DNS records go
  only once the run has no live server. So `--max-age` can never undercut a
  live run. `--max-age` under 1 h is refused without `--force`.
- **Directories.** The work dir (default `$TMPDIR/orama-e2e-<run>`, a
  predictable name) must be a real directory owned by the runner's uid; it is
  made 0700, and one that is a link or someone else's is refused. It is not
  created with `os.MkdirTemp` because the broker socket path under it must
  stay within the 103-byte `sun_path` budget.
- **The test wallet.** Its password file exists only until the agent reports
  ready (`rw-agent-headless` reads it once at start), then it is shredded.
  `rw init` still gets the password in `ROOTWALLET_PASSWORD` for the few
  seconds it runs: it reads a new password only from that variable or a
  terminal. The password and the mnemonic are registered with the
  provisioning redactor as soon as they are generated.
- Guards are allowlists. `CF_ZONE` must be exactly `dbrsteting.bid`; the
  CLI environment must start with `e2e-`; the base domain must be
  `e2e-<run>.dbrsteting.bid`; the chain id must contain `-e2e-`. Run ids,
  zones, environments and base domains containing `testnet`, `mainnet`,
  `devnet` or `stagenet` are refused (chain ids: all but `devnet`, which every
  run chain is), and so is any `RW_AGENT_SOCK` inside the real `~/.rootwallet`
  (found through the user database, not `$HOME`; compared by name and by file
  identity up every ancestor, so a symlink, a hard link or a case-folded path
  cannot pass). The state's CLI `HOME` must be an `e2e-rw-...` directory
  directly under `/tmp` (links resolved) and `RW_AGENT_SOCK` exactly its
  `a.sock`. The guards run in the preflight, after provisioning, and on every
  state a command (`test`, `report`, `teardown`, `hook`) or a feature package
  loads. Under `ORAMA_E2E=1` the CLI re-checks the agent socket at every
  dial, and `orama production unlock` refuses to shell out to `rw decrypt`
  (which would use the real wallet).
- A fleet client refuses plain `http://` and a missing TLS config.
- Every orama CLI invocation runs in a process group of its own: a cancelled
  or killed invocation takes what it started with it, and a helper it left
  holding its output gets 10 s before its group is killed and the pipes
  closed, so no `Run`/`Wait` hangs on it (reported as an error).
- Teardown runs from a `defer` and after SIGINT/SIGTERM/SIGHUP (a second
  signal does not interrupt it). A failed teardown exits 1 and is written into
  the report. `e2e-fleet provision` tears down whatever it created when
  provisioning fails or is interrupted. `--keep-on-fail` keeps a non-PASS fleet for debugging;
  `e2e-fleet teardown` removes it, `e2e-fleet sweep` removes the leftovers
  of crashed runs (above).

### Residual risks

What the harness cannot close, and how to live with it:

- **Same-uid code.** Feature packages run as the owner's OS user. Code in
  them (or in a dependency they build) can still read the owner's real
  `~/.rootwallet`, use an exported `ssh-agent`, and read other processes of
  the same user (`/proc/<pid>/mem` or ptrace where the kernel allows it,
  the broker child's memory included). Run the suite under a dedicated OS
  user or in a VM with no real wallet and no agent; `E2E_SANDBOX=1` hides the
  home directory on Linux but is best-effort. The processes that launch the
  runner keep the tokens too: `infisical run` holds them, and `go run`
  (what `make e2e-fleet` uses) has them in its own environ; run a built
  binary (`go build -o e2e-fleet ./cmd/e2e-fleet && infisical run ... --
  ./e2e-fleet run`) to keep them out of a long-lived `go` process.
- **Host keys through the metadata service.** Each server's pinned SSH host
  key reaches it in its Hetzner user data, which any process on that server
  can read back from the metadata service for the server's life.
- **Port forwards.** `Fleet.Tunnel` and `ReverseForward` open loopback ports
  on the runner and on nodes; any local process of the same user (runner) or
  any process on the node can connect to them while they are up.
- **Supply chain.** The `apps/next-ssr` fixture has no lockfile: `next`,
  `react` and `react-dom` are pinned by version only and installed by the
  deployment's own `npm install` on the fleet, so their integrity rests on
  the npm registry at run time. Reported, not changed by the harness.

### Dependency versions

`github.com/gorilla/websocket` is pinned to the pseudo-version
`v1.5.4-0.20250319132907-e064f32e3674` (a commit after v1.5.3, with no tag
since) and `github.com/pion/*` to the versions `core/go.mod` uses: the e2e
module replaces `github.com/DeBrosOfficial/network` with `../core`, so it
builds the gateway's own WebSocket and WebRTC code paths with the same
modules the product ships.
