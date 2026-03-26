# Quick Start Guide for @debros/network-ts-sdk

## 5-Minute Setup

### 1. Install

```bash
npm install @debros/network-ts-sdk
```

### 2. Create a Client

```typescript
import { createClient } from "@debros/network-ts-sdk";

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
1. Bootstrap node must be running (provides database on port 5001)
2. Gateway must be running (provides REST API on port 6001)

```bash
# Terminal 1: Start bootstrap node
cd ../network
make run-node

# Terminal 2: Start gateway (after bootstrap is ready)
cd ../network
make run-gateway

# Terminal 3: Run E2E tests
cd ../network-ts-sdk
export GATEWAY_BASE_URL=http://localhost:6001
export GATEWAY_API_KEY=ak_your_api_key:default
pnpm run test:e2e
```

**Note**: The gateway configuration now correctly uses port 5001 for RQLite (not 4001 which is P2P).

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
import { SDKError } from "@debros/network-ts-sdk";

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
const msg: Message = await subscription.onMessage((m) => m);
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
