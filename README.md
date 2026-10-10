# Orama Network

A decentralized infrastructure platform combining distributed SQL, IPFS storage, caching, serverless WASM execution, and privacy relay — all managed through a unified API gateway.

## Packages

| Package | Language | Description |
|---------|----------|-------------|
| [core/](core/) | Go | API gateway, distributed node, CLI, and client SDK |
| [sdk/](sdk/) | TypeScript | `@debros/orama` — JavaScript/TypeScript SDK ([npm](https://www.npmjs.com/package/@debros/orama)) |
| [website/](website/) | TypeScript | Public website (orama.network), whitepaper and docs |
| [vault/](vault/) | Zig | Distributed secrets vault (Shamir's Secret Sharing) |
| [os/](os/) | Go + Buildroot | OramaOS — hardened minimal Linux for network nodes |

## Quick Start

```bash
# Build the core network binaries
make core-build

# Run tests
make core-test

# Start website dev server
make website-dev

# Build vault
make vault-build
```

## Documentation

| Document | Description |
|----------|-------------|
| [Whitepaper](docs/whitepaper/WHITEPAPER.md) | What Orama is, how it works, what runs on it today |
| [Architecture](website/src/docs/contributor/architecture-reference.mdx) | System architecture and design patterns |
| [Client surface](website/src/docs/developer/getting-started.mdx) | Humans use the CLI; programs use the SDK / HTTP. No dashboard, no Orama MCP |
| [One-VPS eval](website/src/docs/operator/getting-started.mdx) | Single machine: index + tenant, not HA |
| [Deployment Guide](website/src/docs/developer/deployments.mdx) | Deploy apps, databases, and domains |
| [Dev & Deploy](website/src/docs/contributor/deployment.mdx) | Building, deploying to VPS, rolling upgrades |
| [Authentication](docs/whitepaper/technical-reference/vol1/13-identity.md) | Who someone is, what they may do, and how the gateway decides |
| [Security](docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md) | Security hardening and threat model |
| [Monitoring](website/src/docs/operator/monitoring.mdx) | Cluster health monitoring |
| [TypeScript SDK](website/src/docs/developer/sdk-reference.mdx) | `@debros/orama` — the client applications use |
| [Go Client SDK](website/src/docs/developer/go-sdk.mdx) | The Go client for the same gateway |
| [Serverless](website/src/docs/developer/functions.mdx) | WASM serverless functions |
| [API Surface](docs/whitepaper/technical-reference/appendices/i-api-surface.md) | Every gateway route and which client owns it |
| [CLI Reference](docs/whitepaper/technical-reference/appendices/d-cli-reference.md) | Every command and flag, generated from the code |
| [Common Problems](website/src/docs/operator/troubleshooting.mdx) | Troubleshooting known issues |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, development, and PR guidelines.

## License

[AGPL-3.0](LICENSE)
