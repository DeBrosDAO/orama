# Compliance — Zig

> The concrete files every Zig project must have to satisfy [DEBROS.md](../../DEBROS.md).

Zig is the youngest ecosystem in this rules set. The good news: Zig's design avoids most supply-chain attack vectors (no install-time scripts, dependencies are content-addressed by hash). The bad news: there's no mature vulnerability database, no Renovate support, and no convention-defining popular packages to follow. Compliance leans heavily on manual review.

> **Status:** Zig is pre-1.0 (current stable is `0.13.x` as of late 2025). Build APIs change between releases. Treat this document as a moving target — verify the directives below still work on your project's pinned compiler.

---

## Required files

### 1. `build.zig.zon` with explicit hashes for every dependency

**Tier 3 block.** Commits that add a dependency without an explicit hash are rejected.

Every dependency in `build.zig.zon` MUST include:
- `url` — the source tarball
- `hash` — the integrity hash Zig computes

```zig
.{
    .name = "your-project",
    .version = "0.1.0",
    .dependencies = .{
        .zap = .{
            .url = "https://github.com/zigzap/zap/archive/refs/tags/v0.6.0.tar.gz",
            .hash = "1220abc123def456...",  // explicit, required
        },
    },
    .paths = .{
        "build.zig",
        "build.zig.zon",
        "src",
    },
}
```

Zig's `zig build` will refuse to use a dependency whose downloaded content doesn't match the declared hash. This is equivalent to Go's `go.sum` and is the bedrock of Zig's supply-chain story.

**Never** use unhashed `path = ...` references to remote sources. Local path dependencies are fine for in-monorepo modules; remote sources must always be hashed.

### 2. `.zigversion` — pin the compiler

Convention file (read by `zigup`, `mise`, asdf via plugin):

```
0.13.0
```

CI MUST use the pinned compiler version, not "latest" or "master." Pre-1.0 Zig changes language semantics between minor versions; "latest" is not a safe default.

For projects on Zig master (development versions): commit the exact commit SHA, not "master."

### 3. Verify the compiler signature on install

The Zig compiler binary is signed with Andrew Kelley's minisign key, published at https://ziglang.org/download/. Every CI environment and every developer's machine MUST verify the signature when installing the compiler.

In CI:

```yaml
- name: Install Zig with signature verification
  run: |
    ZIG_VERSION=$(cat .zigversion)
    curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-x86_64-${ZIG_VERSION}.tar.xz" -o zig.tar.xz
    curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-x86_64-${ZIG_VERSION}.tar.xz.minisig" -o zig.tar.xz.minisig
    minisign -Vm zig.tar.xz -P RWSGOq2NVecA2UPNdBUZykf1CCb147pkmdtYxgb3Ti+JO/wCYvhbAb/U
    tar -xJf zig.tar.xz
```

The minisign public key above is the canonical one. Treat it as a pinned constant — if it changes, treat that change as a security event and verify out of band (mailing list, official site, multiple sources) before accepting.

### 4. Review every `build.zig`

Zig's `build.zig` is a Zig program. It runs at build time with **full system access** — it can read files, run subprocesses, hit the network. This is intentional (you can build C deps, run codegen, generate manifests) but it is also the equivalent of npm's `postinstall` problem at the build layer.

Rules:

- The project's own `build.zig` MUST be reviewed line by line in PRs (it's not "configuration," it's executable code with full power)
- Dependencies' `build.zig` files MUST be read when adding the dependency. Subprocess invocations (`std.process.Child`), file writes outside the cache, or network calls are red flags
- No dependency may invoke `std.process.Child` to run shell scripts at build time without explicit allowlisting in `debros.json.compliance.exceptions[]` with a one-line justification

This is the single largest supply-chain risk in Zig. The compiler can't tell "legit codegen" from "exfiltrate `~/.ssh/`." Human review is mandatory.

### 5. Lockfile-equivalent in CI

Zig doesn't have a separate lockfile; `build.zig.zon`'s `hash` fields ARE the lockfile. CI MUST refuse to build if `zig build` would update `build.zig.zon`:

```yaml
- name: Verify build.zig.zon is up to date
  run: |
    cp build.zig.zon build.zig.zon.expected
    zig build --fetch
    diff build.zig.zon build.zig.zon.expected
```

`zig build --fetch` resolves dependencies without compiling; if it would mutate `build.zig.zon`, the diff fails.

### 6. Compiler-version pinning in CI

Match the `.zigversion`:

```yaml
- name: Install pinned Zig
  uses: mlugg/setup-zig@v1
  with:
    version-file: .zigversion
```

(`mlugg/setup-zig` is the community-maintained action with signature verification built in.)

---

## File-by-file checklist

| File | Path | Required? | Tier-3 block? |
|---|---|---|---|
| `build.zig.zon` with hashes for every remote dep | repo root | ✅ | ✅ |
| `.zigversion` | repo root | ✅ | — |
| CI workflow with compiler signature verification | `.github/workflows/security.yml` (or equivalent) | ✅ | — |
| CI step verifying `build.zig.zon` is up-to-date | same | ✅ | — |

---

## Code patterns to enforce

### Error handling — Zig's error unions are the friend

Per DEBROS.md §2.2 principle 6: errors carry context. Zig's error types are great but easy to misuse:

```zig
// Good — explicit error set, useful context
pub const ConnectError = error{
    Timeout,
    ConnectionRefused,
    AddrInUse,
};

fn connectOlric(port: u16) ConnectError!Connection {
    return Connection.init(port) catch |err| switch (err) {
        error.Timeout => return error.Timeout,
        error.ConnectionRefused => {
            std.log.err("olric connection refused on port {d}", .{port});
            return error.ConnectionRefused;
        },
        else => return err,
    };
}

// Forbidden — silent swallow
fn connectOlric(port: u16) ?Connection {
    return Connection.init(port) catch null;  // hides why it failed
}
```

The `try` keyword bubbles errors; `catch` MUST handle them meaningfully (log + return, transform to a domain error, etc.) — never `catch unreachable` outside of provably-impossible cases.

### Allocator discipline

Per DEBROS.md §2.2 principle 4 (validate at boundaries, trust internal code): every public function that allocates takes an `std.mem.Allocator` parameter. No global state, no hidden allocations.

```zig
// Good
pub fn parseConfig(allocator: Allocator, source: []const u8) !Config { ... }

// Forbidden
pub fn parseConfig(source: []const u8) !Config {
    const allocator = std.heap.page_allocator;  // hidden global
    ...
}
```

Tests use `std.testing.allocator` (catches leaks). Production uses a configured allocator (general-purpose arena, fixed buffer, etc.).

### `defer` for cleanup; `errdefer` for error paths

Every allocation has a matching `defer free` (always cleanup) OR `errdefer free` (cleanup on error only, transfer ownership on success). Ad-hoc cleanup at the bottom of functions is forbidden.

### File and function sizes

Per DEBROS.md §2.1:
- Functions ≤50 lines
- Files ≤300 lines

There's no widely-used Zig linter for this yet. Enforce via PR review checklist until tooling lands.

### `comptime` discipline

`comptime` is powerful but easy to abuse. Rules:

- Use `comptime` for type-level computation (generic containers, compile-time validation of constants)
- Never use `comptime` for "performance" without measuring
- `comptime` code is subject to the same length and complexity caps as runtime code
- A `comptime` branch that grows past 30 lines is a code smell — extract to a named function

### Testing

Zig's built-in test runner is the standard:

```zig
test "parseCron rejects empty input" {
    try std.testing.expectError(error.EmptyExpression, parseCron(""));
}
```

- Tests live alongside source (`test { ... }` blocks in the same file, OR `*_test.zig` files)
- Run via `zig build test`
- CI MUST run tests on every PR
- Unit suite total runtime <30s (DEBROS.md §2.4)
- No `std.time.sleep` in tests — poll a readiness condition or use a fake clock

---

## Dependency additions

When adding a Zig dependency, the agent MUST:

1. **Pin a tag, not a branch.** `refs/tags/v0.6.0` is OK; `refs/heads/main` is not. Branch refs are mutable; tags should be immutable (verify the tag isn't a moving target on the upstream — some repos rewrite tags).
2. **Read the dep's `build.zig`** for subprocess invocations, network calls, or file writes outside the cache. Each is a red flag that requires justification.
3. **Verify the hash.** After adding the dep, run `zig build --fetch` and confirm the computed hash matches what the upstream advertised.
4. **Check the maintainer's track record.** Single-author, low-star Zig repos are higher risk simply because the language attracts experimental code. Prefer deps with an active community.
5. **Note the lack of Renovate support.** Zig dep updates are manual. Document the upstream tag-tracking process in a comment in `build.zig.zon`.

---

## Migration from a stock Zig project

1. **Pin the compiler.** Add `.zigversion`.
2. **Audit `build.zig.zon`.** Every remote dependency must have a `hash`. Run `zig build --fetch` and copy the computed hashes in.
3. **Read every `build.zig`** in your dependency tree. Flag anything that runs subprocesses or hits the network at build time. Open issues upstream OR find alternatives.
4. **Add CI** with compiler signature verification and `zig build --fetch` lockfile check.
5. **Update `debros.json`** to record Zig compliance is satisfied. Note any `build.zig` exceptions you accepted in `compliance.exceptions[]`.

Expect first migration to take a day for projects with several deps — the `build.zig` review is the slow part.

---

## Notes on Zig's supply-chain story

What Zig protects against (by design):
- **Hash-pinned dependencies.** `build.zig.zon` mutation is loud; a swapped dep fails to build.
- **No install-time scripts.** Dependencies don't run code when fetched (unlike npm postinstall).
- **No package registry to compromise.** Deps are URLs (usually GitHub tarballs); there's no central index to attack. Each upstream's compromise is isolated.
- **Cryptographically-signed compiler releases.** The official ziglang.org binaries are minisigned.

What Zig does NOT protect against:
- **`build.zig` running arbitrary code at build time.** This is the equivalent of npm postinstall, but always-on. Human review of every dep's `build.zig` is the only defense.
- **Compromised upstream repos.** Hash-pinning catches changes to *already-fetched* versions, but a malicious new release still has whatever malicious content it ships with. There's no `pip-audit` / `govulncheck` equivalent yet.
- **Tag rewriting.** Some upstreams rewrite tags. Hash-pinning catches this on re-fetch, but the social signal is missed. Prefer upstreams with a no-tag-rewrite policy.
- **Renovate support.** None yet. Track dep updates manually. Open a Renovate config issue upstream if your CI infra needs auto-PRs.

Zig is the youngest ecosystem here and tooling is still catching up. Until the Zig package registry (or an equivalent) emerges, manual review is the floor.
