# Compliance — Go

> The concrete files every Go project must have to satisfy [DEBROS.md](../../DEBROS.md).

Go has a stronger built-in supply-chain story than npm — `go.sum` records cryptographic hashes for every module version and `go mod verify` enforces them. There are still gaps that need attention.

---

## Required files

### 1. `go.mod` with `toolchain` directive

Pin the Go version explicitly:

```go
module github.com/example/project

go 1.22

toolchain go1.22.5
```

The `toolchain` directive locks the exact Go version. CI MUST use that version, not the OS default.

### 2. `go.sum` committed

**Tier 3 block.** `go.sum` MUST be committed. Commits to `go.mod` without a corresponding `go.sum` change are rejected.

CI MUST run `go mod verify` to check that downloaded modules match the hashes in `go.sum`.

### 3. `GOFLAGS` for reproducibility

In CI, set:

```bash
export GOFLAGS="-mod=readonly -trimpath"
```

`-mod=readonly` prevents `go build` from mutating `go.mod` or `go.sum`. `-trimpath` removes absolute filesystem paths from binaries for reproducible builds.

### 4. `renovate.json` with 30-day cooldown for Go modules

Renovate supports Go modules via the `gomod` manager. Copy [`templates/renovate.json`](https://github.com/DeBrosDAO/rules/blob/main/templates/renovate.json) — the same file works across ecosystems.

Key config:

```jsonc
{
  "gomod": {
    "enabled": true
  },
  "minimumReleaseAge": "30 days",
  "automerge": false
}
```

### 5. `govulncheck` in CI

`govulncheck` is the official Go vulnerability scanner — it analyzes call graphs to report only vulnerabilities that the project actually reaches, not just any imported module.

Add to your CI workflow:

```yaml
- name: govulncheck
  run: |
    go install golang.org/x/vuln/cmd/govulncheck@latest
    govulncheck ./...
```

Findings at severity HIGH or higher fail the build.

### 6. `staticcheck` in CI

`go vet` is the floor; `staticcheck` is the canonical extended linter. Either via `golangci-lint` (which bundles it) or directly:

```yaml
- name: staticcheck
  run: |
    go install honnef.co/go/tools/cmd/staticcheck@latest
    staticcheck ./...
```

### 7. `.tool-versions` (or equivalent)

```
# .tool-versions
golang 1.22.5
```

CI uses the pinned version:

```yaml
- uses: actions/setup-go@v5
  with:
    go-version-file: 'go.mod'   # reads `toolchain` directive
```

---

## File-by-file checklist

| File | Path | Required? | Tier-3 block? |
|---|---|---|---|
| `go.mod` with `toolchain` directive | repo root | ✅ | — |
| `go.sum` | repo root | ✅ | ✅ |
| `renovate.json` | repo root | ✅ | — |
| `.github/workflows/security.yml` running `govulncheck` | `.github/workflows/` | ✅ | — |
| `.tool-versions` or equivalent | repo root | ✅ | — |
| `.golangci.yml` (config for `golangci-lint`) | repo root | ✅ | — |

---

## Code patterns to enforce

### Error handling

Per DEBROS.md §2.2 principle 6: errors carry actionable context.

```go
// Good
if err != nil {
    return fmt.Errorf("connect to olric on port %d: %w", port, err)
}

// Bad
if err != nil {
    return err
}

// Forbidden — swallows the error silently
if err != nil {
    log.Println("warning:", err)
    return nil
}
```

Every non-trivial `if err != nil` MUST wrap the error with `fmt.Errorf("...: %w", err)` and name the operation that failed.

### Concurrency

Per DEBROS.md §2.2 principle 8: no premature concurrency.

- Default: write sequential code
- Add goroutines only after benchmarking shows a bottleneck
- All goroutines MUST have a clear lifecycle — who spawns them, who waits for them, how they shut down
- All shared state MUST be protected by `sync.Mutex` or channels — there's no third option
- `go test -race` MUST run in CI for any package using goroutines

### Context handling

Every function that does I/O takes a `context.Context` as its first parameter:

```go
// Good
func GetUser(ctx context.Context, id string) (*User, error)

// Bad (no context)
func GetUser(id string) (*User, error)
```

`context.Background()` is allowed at the top of `main()` and in tests; nowhere else.

### Magic values

Per DEBROS.md §2.1: no magic numbers/strings.

```go
// Good
const (
    defaultTimeout    = 30 * time.Second
    maxConcurrentRequests = 100
)

// Bad
client.Timeout = 30 * time.Second  // magic 30
```

### File and function sizes

Per DEBROS.md §2.1:
- Functions ≤50 lines
- Files ≤300 lines

Use `gocyclo` or `golangci-lint`'s `funlen` linter to enforce.

### Testing

- Unit tests use the standard `testing` package (no third-party assert libraries unless project has a strong existing convention)
- Table-driven tests with named subtests: `t.Run("when X, returns Y", ...)`
- Race detector enabled: CI runs `go test -race ./...`
- Coverage tracked: `go test -coverprofile=coverage.out ./...`, reviewed for regressions in PRs
- Integration tests in `*_integration_test.go` files with a build tag, runnable separately from unit tests

---

## Dependency additions

When adding a Go module dependency, the agent MUST verify:

1. The module version was published ≥30 days ago (rule §1.1)
2. The module is sourced from a trusted host (golang.org, github.com, gopkg.in, gitlab.com, bitbucket.org — not random URLs)
3. The module has more than one contributor in its commit history
4. The `LICENSE` file is present and compatible with the project's license

`go list -m -u all` shows current vs available versions. Use `go mod why <module>` to confirm a transitively-pulled module is actually needed.

---

## Migration from a stock Go project

1. Add `toolchain` directive to `go.mod`
2. Run `go mod tidy` and commit the result
3. Add `.tool-versions` matching the toolchain version
4. Add the CI workflow with `govulncheck` and `staticcheck`
5. Fix anything the linters catch (often a half-day for a mid-size project)
6. Add `renovate.json`
7. Update `debros.json` to record Go compliance is satisfied

---

## Notes specific to Go's supply-chain story

Go has stronger supply-chain defaults than npm/PyPI by design:

- **`go.sum` records cryptographic hashes.** A module version can't be silently swapped — the hash check fails.
- **`GOPROXY` defaults to `proxy.golang.org`,** which caches and verifies modules. Direct fetches from VCS are disabled by default via `GOSUMDB`.
- **No install scripts.** Go modules don't have a postinstall equivalent. The blast radius of a compromised module is limited to "code I import and call."

Things Go does NOT protect against:

- A compromised module publishing a malicious version that passes hash verification (because the hash is computed from the malicious source). 30-day cooldown helps here.
- A module author transferring ownership to a malicious party. Check for recent ownership changes on the source repo before upgrading.
- Typo-squatting (e.g. `github.com/user/cool` vs `github.com/user/cooi`). Code review catches this — agents must read every new import and confirm it's the intended module.
