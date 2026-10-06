# How you talk to the network

Humans use one CLI. Programs use the SDK and the gateway HTTP API. There is no
Orama dashboard and no Orama MCP.

The architecture of the node is in [ARCHITECTURE.md](ARCHITECTURE.md). This
page is only the client surface.

---

## Humans — the `orama` CLI

One binary, two audiences. Both stay in `orama`. We will not split an operator
CLI and we will not remove operator paths.

**Operators** run the node and the fleet: `orama node` (install, enroll, setup,
status, logs, doctor, rollout, wipe, …), plus `inspect`, `invite`, `monitor`,
`rollout`, `push`, `nodes`, `status`, `ssh`, and `sandbox`.

**Tenants** run a namespace: `deploy`, `app`, `function`, `db`, `domain`,
`namespace`, `members`, `auth`, `audit`, `env`.

Every command and flag is in [CLI_REFERENCE.md](CLI_REFERENCE.md). Task-shaped
guides: [deploying apps](DEPLOYMENT_GUIDE.md), [building and rolling
out](DEV_DEPLOY.md), [functions](SERVERLESS.md).

---

## Programs — SDK and gateway HTTP

A deployed application talks to its namespace gateway. It does not shell out to
the CLI.

| Surface | For |
|---------|-----|
| [TypeScript SDK](TS_SDK.md) (`@debros/orama`) | Application code in JS/TS |
| [Go client](GO_CLIENT_SDK.md) | Application code in Go |
| [Gateway HTTP](API_SURFACE.md) | The routes those clients call, and the ones they do not |

Deploying an app, minting a key, and managing nodes are the CLI's job. The SDK
reaches a deliberate subset of the gateway; [API_SURFACE.md](API_SURFACE.md)
records who owns each route.

---

## Not this product

These are not Orama clients and are not being built as one:

- **No Orama dashboard.** The website is a landing page and docs. Tenant web
  and mobile apps are applications you deploy; they call the SDK, they are not
  an Orama control plane.
- **No Orama MCP** for tenants or operators. Agents read the docs (`llms.txt`
  and the markdown it lists). That is documentation, not a product MCP.
- Hosting-provider consoles and the Apple Developer dashboard are other
  people's products. Mentions of those in the operator docs are not an Orama
  dashboard.
