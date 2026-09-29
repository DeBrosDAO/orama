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
  cmd/e2e-fleet/         the runner: run, provision, test, teardown, sweep, report, coverage, hook
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
fails at once ("the run was interrupted").

### Test names

`Test{Function}_{scenario}`, e.g. `TestVerify_replayedNonceRefused`. The lint
rejects other `Test*` names and flags a `func(*testing.T)` that is not named
`Test...` (it would never run).

---

## Rules

- **No sleeps.** `time.Sleep`, `time.After`, `time.NewTimer` and `time.Tick`
  are banned in `features/**`. Wait for a readiness signal with
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
  `auth approve` (2 + the approver's challenge bucket). Any other command may
  renew its session (refresh or API-key exchange): when the HOME's
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
creator allowlist. This is the only state a test changes and leaves changed;
a test that needs another mode sets it and restores `open` in its cleanup.

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
| `RequireChain(t)` | not-applicable skip when the run has no chain. `scripts/chain-deploy.sh` enables each node's REST API (`[api] enable = true`, `127.0.0.1:31003`, checked by `chain-deploy.sh status`), which the gateway's `/v1/chain/*` and the CLI's `--node` paths read; reach it from a test with `Fleet.Tunnel(t, n, "127.0.0.1:31003")` |
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
| `IPTablesBlock(t, from, to)` | partition `from` from `to` (public + WG addresses; `ip6tables` for IPv6) with rules tagged per call, so two tests blocking the same pair each hold their own | cleanup registered before the insert; deletes its own rules until `iptables -C` says they are gone |
| `Kill(t, n, unit)` | SIGKILL every process of the unit | waits until the unit is active |
| `StopService(t, n, unit)` | stop the unit | starts it, waits until active |
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
| `(*Client).Send(ctx, gw.Req) (*Response, error)` | `Req{Method, Path, Query, Header, Body, Bearer, APIKey, Host}`; header values are sent as given (duplicates allowed), body verbatim |
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
namespace, minus 4 of headroom = **16** on three nodes. A test that creates
several namespaces calls `ns.Hold(t, f, n)` first (all its slots at once; the
next `n` `ns.New` use them), so it never holds some slots while waiting for
the rest. `ns.Reserve(ctx, workDir, count, capacity) (*Slots, error)` and
`(*Slots).Release()` are the primitive. `features/internal/tenancy` keeps its
own per-package cap on top.
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
`go test -tags e2e_fleet -json -count=1 -timeout <stage timeout> ./features/<id>`
for its packages: non-destructive ones in parallel, then each destructive one
alone. A stage's failures never stop later stages. `e2e-fleet test --stage N`
runs one stage against an existing fleet; `--resume` skips the stages
`stages-state.json` records as completed (a completed stage is never re-run,
failed or not). A package run replaces its earlier attempt whole: output and
evidence dir.

Each package runs in a process group of its own. When the run is interrupted
the group gets SIGINT (the go command waits for its test binary, whose
`harness.Main` lets running tests clean up), up to 10 minutes to finish, then
SIGKILL.

Extra servers: `harness.ExtraNode(t, "extra-join", "hel1")` in the test, and
`requires.extra_nodes` in the manifest (an eval cluster from
`harness.ExtraCluster` counts as one). The broker creates them with the
runner's credentials. `e2e-fleet hook provision` names an
extra `extra-<N>` one above the highest in use, so a name is never reused. The core/e2e lifecycle hooks map onto
the runner: `ORAMA_LIFECYCLE_DESTROY="e2e-fleet hook destroy"`,
`ORAMA_LIFECYCLE_BREAK="e2e-fleet hook break"`,
`ORAMA_LIFECYCLE_PROVISION="e2e-fleet hook provision"` (prints the new IP), all
reading `E2E_FLEET_STATE`.

## Evidence and the report

Every CLI invocation (`oramacli`), HTTP/WebSocket/SSE exchange (`gw`), SSH
command, file transfer or port forward (`fleet`) and other subprocess
(`evidence.RunRecorded`) is appended, redacted and bounded, to the
package run's `<artifacts>/evidence/stage-NN-<feature>/<feature>.jsonl`,
attributed to the test (re-runs record under `rerun/evidence/`, which the
report never reads). For a failed test the report shows its last ten records.

Redaction masks the run's secrets, every credential minted during the run,
and recognised shapes: Authorization/X-API-Key/Cookie/Set-Cookie values,
JSON members whose key names a token, secret, password, API or private key,
mnemonic, PSK or swarm key (any case, plain or escaped JSON), `KEY=value` and
YAML forms, `user:password@` in URLs, WireGuard `PrivateKey`, the IPFS swarm
key body, PEM private keys, JWTs and Orama API keys. Credentials a feature
process mints are appended to the run's token registry (`redact-tokens`,
mode 0600, beside `state.json`, never in the artifact dir); the runner reads
it to redact each package's `gotest/` output and stderr after the package
ends, the collected artifacts, and everything the report shows. Before teardown the runner collects
journals of every `orama-*`, `caddy*`, `coredns*`, `wg-quick@*` unit,
`orama node report --json`, listeners, WireGuard (no keys), ufw, disk, clock
and (when the run has a chain) chain status from every node, and `orama monitor report --json` and
`orama inspect` from the runner, into `<artifacts>/collected/`.

The artifact dir then holds `report.html` (self-contained), `report.json`,
`report.junit.xml`, `summary.txt` (the push notification line) and, with
`--bug-drafts`, `bugboard-drafts.json` (drafts only; nothing is filed).
Verdicts: **PASS** (everything passed, nothing skipped, coverage gate OK),
**INCOMPLETE** (nothing failed, something not covered: a skip, a feature that
never ran or executed no test, a coverage gap), **FAIL** (a test, package or
run step failed; a package whose `go test` exited non-zero fails with the tail
of its stderr even when every parsed test passed; a failed teardown is a run
error; a flaky failure is still a failure). Exit codes 0, 3, 1 (2 for usage).

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
- **Feature environment is an allowlist** (`stages.FeatureEnv`): `PATH`,
  `LANG`, `LC_ALL`, `TERM`, `TMPDIR`, `TZ`, the Go variables (`GOFLAGS`,
  `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOTOOLCHAIN`, `GOPROXY`, `GOPRIVATE`,
  `GONOSUMDB`, `GONOPROXY`, `GOSUMDB`), the proxies (`HTTP(S)_PROXY`,
  `NO_PROXY`, either case) and `E2E_*`; never `HOME`, `SSH_AUTH_SOCK`,
  `XDG_*`, `INFISICAL_*` or a variable `secrets.SecretEnvNames` lists. The
  runner resolves `GOPATH`, `GOMODCACHE` and `GOCACHE` with `go env` when
  unset, and adds `E2E_FLEET_STATE`, `E2E_STRICT`, `E2E_EVIDENCE_DIR` and
  `E2E_BROKER_SOCK`.
- **The broker** (`harness/broker`): `e2e-fleet run` and `e2e-fleet test`
  serve the cloud operations a feature legitimately needs on a unix socket,
  `<work dir>/broker/broker.sock` (directory 0700, socket 0600), up exactly
  while the stages run, and pass its path as `E2E_BROKER_SOCK`. It holds the
  credentials; feature processes do not. Operations, each scoped to the run:
  `dns.txt.set`/`dns.txt.delete` (a name inside `e2e-<run>.<zone>` or a
  cluster subdomain `e2e-<run>-<label>.<zone>`; anything else is refused before
  Cloudflare is called), `dns.records.list` (the run's own records),
  `extra.add`/`extra.remove` (extras only, never a core node or probe; one name
  at a time), `cluster.add`/`cluster.remove` (eval clusters it installed).
  In a feature process `provision.AddExtra`, `RemoveExtra`, `DestroyNode`
  (extras only), `AddEvalCluster` and `RemoveEvalCluster` go through it; the
  switch is `E2E_BROKER_SOCK` being set, never a missing token. A runner
  started with `E2E_BROKER_SOCK` set refuses to serve one. Errors it returns
  are redacted. Teardown deletes every record of the run's cluster
  subdomains as well as its own subdomain.
- Guards are allowlists. `CF_ZONE` must be exactly `dbrsteting.bid`; the
  CLI environment must start with `e2e-`; the base domain must be
  `e2e-<run>.dbrsteting.bid`; the chain id must contain `-e2e-`. Run ids,
  zones, environments and base domains containing `testnet`, `mainnet`,
  `devnet` or `stagenet` are refused (chain ids: all but `devnet`, which every
  run chain is), and so is any `RW_AGENT_SOCK` inside the real `~/.rootwallet`
  (found through the user database, not `$HOME`). The guards run in the
  preflight, after provisioning, and on every state a command
  (`test`, `report`, `teardown`, `hook`) or a feature package loads.
- A fleet client refuses plain `http://` and a missing TLS config.
- Teardown runs from a `defer` and after SIGINT/SIGTERM/SIGHUP (a second
  signal does not interrupt it). A failed teardown exits 1 and is written into
  the report. `e2e-fleet provision` tears down whatever it created when
  provisioning fails or is interrupted. `--keep-on-fail` keeps a non-PASS fleet for debugging;
  `e2e-fleet teardown` removes it, `e2e-fleet sweep` removes anything labelled
  e2e older than `--max-age`.
