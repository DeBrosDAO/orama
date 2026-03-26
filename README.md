# Orama Network

A decentralized infrastructure platform combining distributed SQL, IPFS storage, caching, serverless WASM execution, and privacy relay — all managed through a unified API gateway.

## Packages

| Package | Language | Description |
|---------|----------|-------------|
| [core/](core/) | Go | API gateway, distributed node, CLI, and client SDK |
| [sdk/](sdk/) | TypeScript | `@debros/orama` — JavaScript/TypeScript SDK ([npm](https://www.npmjs.com/package/@debros/orama)) |
| [website/](website/) | TypeScript | Marketing website and invest portal |
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
| [Architecture](core/docs/ARCHITECTURE.md) | System architecture and design patterns |
| [Deployment Guide](core/docs/DEPLOYMENT_GUIDE.md) | Deploy apps, databases, and domains |
| [Dev & Deploy](core/docs/DEV_DEPLOY.md) | Building, deploying to VPS, rolling upgrades |
| [Security](core/docs/SECURITY.md) | Security hardening and threat model |
| [Monitoring](core/docs/MONITORING.md) | Cluster health monitoring |
| [Client SDK](core/docs/CLIENT_SDK.md) | Go SDK documentation |
| [Serverless](core/docs/SERVERLESS.md) | WASM serverless functions |
| [Common Problems](core/docs/COMMON_PROBLEMS.md) | Troubleshooting known issues |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, development, and PR guidelines.

## License

[AGPL-3.0](LICENSE)
