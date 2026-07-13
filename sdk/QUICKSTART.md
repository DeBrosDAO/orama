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
  baseURL: "http://localhost:6001",
  apiKey: "ak_your_api_key:namespace", // Get from gateway
});
```

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

### Prerequisites
1. A running Orama gateway to test against (REST API on port 6001; RQLite on port 5001 behind it)
2. An API key for that gateway

The SDK lives in the `sdk/` directory of the Orama monorepo (the Go node/gateway code is under `core/`). Point the E2E tests at any running gateway:

```bash
cd sdk
export GATEWAY_BASE_URL=http://localhost:6001
export GATEWAY_API_KEY=ak_your_api_key:default
pnpm run test:e2e
```

Without `GATEWAY_API_KEY` set, the tests skip gracefully instead of failing.

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
import { SDKError } from "@debros/orama";

try {
  await client.db.query("SELECT * FROM invalid_table");
} catch (error) {
  if (error instanceof SDKError) {
    console.error(`${error.httpStatus}: ${error.message}`);
  }
}
```

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
2. Explore [tests/e2e/](./tests/e2e/) for examples
3. Explore [examples/](./examples/) for runnable code samples

## Troubleshooting

**"Failed to connect to gateway"**
- Check `GATEWAY_BASE_URL` is correct
- Ensure gateway is running
- Verify network connectivity

**"API key invalid"**
- Confirm `apiKey` format: `ak_key:namespace`
- Get a fresh API key from gateway admin

**"WebSocket connection failed"**
- Gateway must support WebSocket at `/v1/pubsub/ws`
- Check firewall settings

**"Tests skip"**
- Set `GATEWAY_API_KEY` environment variable
- Tests gracefully skip without it
