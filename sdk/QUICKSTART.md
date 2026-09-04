# Quick Start Guide for @debros/orama

## 5-Minute Setup

### 1. Install

```bash
npm install @debros/orama
```

### 2. Create a Client

```typescript
import { createClient } from "@debros/orama";

const client = createClient({
  baseURL: "https://ns-myapp.orama-devnet.network",
  apiKey: process.env.ORAMA_API_KEY, // ak_<id>:<namespace>
});
```

Mint one with `orama namespace keys create --scope <profile>`. **Which profile
depends on where the code runs:** an `app-runtime` key is safe in a browser
bundle, an `admin` key is not — it carries the whole control plane. See
[Browser and server](./README.md#browser-and-server).

### 3. Use It

**Database:**
```typescript
await client.db.createTable("CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT)");
await client.db.exec("INSERT INTO posts (title) VALUES (?)", ["Hello"]);
const posts = await client.db.query("SELECT * FROM posts");
```

**Pub/Sub:**
```typescript
const sub = await client.pubsub.subscribe("news", {
  onMessage: (msg) => console.log(msg.data),
});

await client.pubsub.publish("news", "Update!");
sub.close();
```

**Network:**
```typescript
const healthy = await client.network.health();
const status = await client.network.status();
```

## Running Tests Locally

The unit tests need nothing:

```bash
cd sdk
pnpm test        # one run, unit + end-to-end
pnpm test:watch  # re-run on change
pnpm test:unit   # unit only
```

`pnpm test` used to be `vitest` with no `run`, so it started watch mode and
never returned — in a terminal or in CI.

### End-to-end tests

These need a gateway and an API key for it:

```bash
export GATEWAY_BASE_URL=https://ns-myapp.orama-devnet.network
export GATEWAY_API_KEY=ak_your_api_key:default
pnpm test:e2e
```

Without `GATEWAY_API_KEY` they are skipped, and reported as skipped. They used
to log "Skipping ..." and then run anyway against a gateway that was not there,
which is where 27 of the suite's 30 failures came from.

## Building for Production

```bash
npm run build
# Output in dist/
```

## Key Classes

| Class | Purpose |
|-------|---------|
| `createClient()` | Factory function, returns `Client` |
| `AuthClient` | Authentication, token management |
| `DBClient` | Database operations (exec, query, etc.) |
| `QueryBuilder` | Fluent SELECT builder |
| `Repository<T>` | Generic entity pattern |
| `PubSubClient` | Pub/sub operations |
| `NetworkClient` | Network status, peers |
| `SDKError` | All errors inherit from this |
| `AuthError` | 401 — the credential was rejected |
| `ScopeError` | 403 — the credential's grants do not cover the operation; `requiredScope` names the one it needed |
| `NotFoundError` | 404 |
| `NetworkError` | The gateway was never reached; `httpStatus` is 0 |

## Common Patterns

### QueryBuilder
```typescript
const items = await client.db
  .createQueryBuilder("items")
  .where("status = ?", ["active"])
  .andWhere("price > ?", [10])
  .orderBy("created_at DESC")
  .limit(20)
  .getMany();
```

### Repository
```typescript
interface User { id?: number; email: string; }
const repo = client.db.repository<User>("users");

// Save (insert or update)
const user: User = { email: "alice@example.com" };
await repo.save(user);

// Find
const found = await repo.findOne({ email: "alice@example.com" });
```

### Transaction
```typescript
await client.db.transaction([
  { kind: "exec", sql: "INSERT INTO logs (msg) VALUES (?)", args: ["Event A"] },
  { kind: "query", sql: "SELECT COUNT(*) FROM logs", args: [] },
]);
```

### Error Handling
```typescript
import { NetworkError, ScopeError, SDKError } from "@debros/orama";

try {
  await client.db.query("SELECT * FROM invalid_table");
} catch (error) {
  if (error instanceof ScopeError) {
    console.error(`this key needs the ${error.requiredScope} grant`);
  } else if (error instanceof NetworkError) {
    console.error("the gateway was never reached");
  } else if (error instanceof SDKError) {
    console.error(`${error.httpStatus}: ${error.message}`);
  }
}
```

The four subclasses all extend `SDKError`, so one `catch` still covers
everything.

## TypeScript Types

Full type safety - use autocomplete in your IDE:
```typescript
const status: NetworkStatus = await client.network.status();
const users: User[] = await repo.find({ active: 1 });
const sub = await client.pubsub.subscribe("news", {
  onMessage: (msg: PubSubMessage) => console.log(msg.data),
});
```

## Next Steps

1. Read the full [README.md](./README.md)
2. Read [docs/TS_SDK.md](../docs/TS_SDK.md) for the module-by-module reference
3. Explore [tests/e2e/](./tests/e2e/) for examples
4. Explore [examples/](./examples/) for runnable code samples

## Troubleshooting

**"Failed to connect to gateway"**
- Check `GATEWAY_BASE_URL` is correct
- Ensure gateway is running
- Verify network connectivity

**"API key invalid"**
- Confirm `apiKey` format: `ak_<id>:<namespace>`
- Get a fresh API key from gateway admin

**"insufficient scope"**
- The key is valid but lacks the grant the operation needs. The error is a
  `ScopeError` and `error.requiredScope` names it.
- Mint a key that has it: `orama namespace keys create --scope <grant list>`

**"WebSocket connection failed"**
- Gateway must support WebSocket at `/v1/pubsub/ws`
- Check firewall settings

**"Tests skip"**
- Set `GATEWAY_API_KEY` environment variable
- End-to-end tests are skipped without it; the unit tests need nothing
