# Contributing to Orama Network

Thanks for helping improve the network! This monorepo contains multiple projects — pick the one relevant to your contribution.

## Repository Structure

| Package | Language | Build |
|---------|----------|-------|
| `core/` | Go 1.24+ | `make core-build` |
| `website/` | TypeScript (pnpm) | `make website-build` |
| `vault/` | Zig 0.14+ | `make vault-build` |
| `os/` | Go + Buildroot | `make os-build` |

## Setup

```bash
git clone https://github.com/DeBrosOfficial/network.git
cd network
```

### Core (Go)

```bash
cd core
make deps
make build
make test
```

### Website

```bash
cd website
pnpm install
pnpm dev
```

### Vault (Zig)

```bash
cd vault
zig build
zig build test
```

## Pull Requests

1. Fork and create a topic branch from `main`.
2. Ensure `make test` passes for affected packages.
3. Include tests for new functionality or bug fixes.
4. Keep PRs focused — one concern per PR.
5. Write a clear description: motivation, approach, and how you tested it.
6. Update docs if you're changing user-facing behavior.

## Code Style

### Go (core/, os/)

- Follow standard Go conventions
- Run `make lint` before submitting
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- No magic values — use named constants

### TypeScript (website/)

- TypeScript strict mode
- Follow existing patterns in the codebase

### Zig (vault/)

- Follow standard Zig conventions
- Run `zig build test` before submitting

## Security

If you find a security vulnerability, **do not open a public issue**. Email security@debros.io instead.

Thank you for contributing!
