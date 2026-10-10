# harness/agent: the throwaway RootWallet agent

An e2e run signs everything (archives, SIWE logins, vault SSH keys) through a
wallet that exists only for that run. `agent.Start` creates it and serves it
with `rw-agent-headless`; `Agent.Stop` (or `agent.StopDir` from another
process) ends the agent and shreds its directory. Nothing here ever reads or
writes the operator's `~/.rootwallet`.

## What Start does

1. Refuses to run as root, and refuses a directory that is inside the real
   `~/.rootwallet` or holds it (by resolved name and by file identity up
   every ancestor). The real home comes from the user database,
   not `$HOME`, which the harness overrides.
2. Creates a short private directory, `/tmp/e2e-rw-XXXXXXXX` (a Unix socket
   path is limited to about 100 bytes on macOS). It is the agent's `--home`
   and also the `HOME` the orama CLI runs with, so the wallet is
   `<dir>/.rootwallet`.
3. Generates a random password (32 bytes from `crypto/rand`) and a random
   12-word BIP-39 mnemonic and hands both to `StartConfig.Redact` (the
   provisioner registers them with its redactor) before anything runs. Writes
   the password to `<dir>/pw` (0600, `O_EXCL|O_NOFOLLOW`) and the mnemonic to
   `<dir>/mnemonic.txt`, runs
   `HOME=<dir> ROOTWALLET_PASSWORD=<pw> rw init --mnemonic-file <dir>/mnemonic.txt`
   with no `XDG_DATA_HOME` and no other inherited variable, then shreds the
   mnemonic. The password goes in the environment because `rw init` reads a
   new password only from `ROOTWALLET_PASSWORD` or a terminal prompt, never
   from a pipe (rootwallet `apps/cli/src/lib/password.ts`, `askNewPassword`).
4. Starts `rw-agent-headless --home <dir> --socket <dir>/a.sock --password-file <dir>/pw
   --ready-file <dir>/ready.json --approve <binary>=<cap,...>` for every
   approved binary (the orama CLI under test, and the previous release's CLI
   when one is built), in a session of its own (`setsid`), with its stdout and
   stderr written straight to the log file (`StartConfig.Log`, else
   `<dir>/agent.log`). No pipe of the starting process is in between, so the
   agent outlives `e2e-fleet provision`; errors quote the log's tail.
5. Waits for the ready file (`{"pid","socket","address"}`), checks it names
   this agent's pid and socket and an EVM address, shreds `<dir>/pw` (the
   agent read it once while it prepared, before binding its socket: rootwallet
   `apps/desktop/src-tauri/src/agent_server/headless/mod.rs`), then calls
   `GET /v1/status` over the socket and refuses a locked wallet.

`StopDir` (teardown from another process) signals the ready file's pid only
when that process's command line passes exactly `--home <dir>`.

`Agent.Env()` is `HOME=<dir>` and `RW_AGENT_SOCK=<dir>/a.sock`. The provisioner
also sets `ORAMA_E2E=1`, under which the orama CLI refuses to fall back to the
default `~/.rootwallet/agent.sock` (core/pkg/rwagent/e2eguard.go).

## Dependency on RootWallet task 2857

The orama CLI needs these capabilities pre-approved (`agent.OramaCaps`):
`vault:ssh`, `vault:password`, `wallet:address`, `wallet:sign`,
`wallet:sign:orama-archive`.

- `wallet:sign:orama-archive` cannot be pre-approved by `rw-agent-headless`
  on RootWallet main today; the fix exists only in an uncommitted worktree.
  Until task 2857 lands, `Start` fails with the agent's own refusal message
  and names the task, and `orama maint build` cannot sign an archive headless.
- `wallet:sign:orama-tx` is always refused by a headless agent. It is not in
  `OramaCaps`; chain transactions signed through the wallet wait for task
  2857.

Assumed from the RootWallet investigation and not verifiable here without
running the real binaries: the flag names above, the ready file's JSON
fields, that the agent writes the ready file only once its socket serves, and
that `rw init` reads the password from `ROOTWALLET_PASSWORD`. If task 2857
changes any of them, `Start` fails loudly at that step.
