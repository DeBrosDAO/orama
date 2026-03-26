# @debros/network-ts-sdk - TypeScript SDK for DeBros Network

A modern, isomorphic TypeScript SDK for the DeBros Network gateway. Works seamlessly in both Node.js and browser environments with support for database operations, pub/sub messaging, and network management.

## Features

- **Isomorphic**: Works in Node.js and browsers (uses fetch and isomorphic-ws)
- **Database ORM-like API**: QueryBuilder, Repository pattern, transactions
- **Pub/Sub Messaging**: WebSocket subscriptions with automatic reconnection
- **Authentication**: API key and JWT support with automatic token management
- **TypeScript First**: Full type safety and IntelliSense
- **Error Handling**: Unified SDKError with HTTP status and code

## Installation

```bash
npm install @debros/network-ts-sdk
```

## Quick Start

### Initialize the Client

```typescript
import { createClient } from "@debros/network-ts-sdk";

const client = createClient({
  baseURL: "http://localhost:6001",
  apiKey: "ak_your_api_key:namespace",
});

// Or with JWT
const client = createClient({
  baseURL: "http://localhost:6001",
  jwt: "your_jwt_token",
});
```

### Database Operations

#### Create a Table

```typescript
await client.db.createTable(
  "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"
);
```

#### Insert Data

```typescript
const result = await client.db.exec(
  "INSERT INTO users (name, email) VALUES (?, ?)",
  ["Alice", "alice@example.com"]
);
console.log(result.last_insert_id);
```

#### Query Data

```typescript
const users = await client.db.query("SELECT * FROM users WHERE email = ?", [
  "alice@example.com",
]);
```

#### Using QueryBuilder

```typescript
const activeUsers = await client.db
  .createQueryBuilder("users")
  .where("active = ?", [1])
  .orderBy("name DESC")
  .limit(10)
  .getMany();

const firstUser = await client.db
  .createQueryBuilder("users")
  .where("id = ?", [1])
  .getOne();
```

#### Using Repository Pattern

```typescript
interface User {
  id?: number;
  name: string;
  email: string;
}

const repo = client.db.repository<User>("users");

// Find
const users = await repo.find({ active: 1 });
const user = await repo.findOne({ email: "alice@example.com" });

// Save (INSERT or UPDATE)
const newUser: User = { name: "Bob", email: "bob@example.com" };
await repo.save(newUser);

// Remove
await repo.remove(newUser);
```

#### Transactions

```typescript
const results = await client.db.transaction([
  {
    kind: "exec",
    sql: "INSERT INTO users (name, email) VALUES (?, ?)",
    args: ["Charlie", "charlie@example.com"],
  },
  {
    kind: "query",
    sql: "SELECT COUNT(*) as count FROM users",
    args: [],
  },
]);
```

### Pub/Sub Messaging

The SDK provides a robust pub/sub client with:

- **Multi-subscriber support**: Multiple connections can subscribe to the same topic
- **Namespace isolation**: Topics are scoped to your authenticated namespace
- **Server timestamps**: Messages preserve server-side timestamps
- **Binary-safe**: Supports both string and binary (`Uint8Array`) payloads
- **Strict envelope validation**: Type-safe message parsing with error handling

#### Publish a Message

```typescript
// Publish a string message
await client.pubsub.publish("notifications", "Hello, Network!");

// Publish binary data
const binaryData = new Uint8Array([1, 2, 3, 4]);
await client.pubsub.publish("binary-topic", binaryData);
```

#### Subscribe to Topics

```typescript
const subscription = await client.pubsub.subscribe("notifications", {
  onMessage: (msg) => {
    console.log("Topic:", msg.topic);
    console.log("Data:", msg.data);
    console.log("Server timestamp:", new Date(msg.timestamp));
  },
  onError: (err) => {
    console.error("Subscription error:", err);
  },
  onClose: () => {
    console.log("Subscription closed");
  },
});

// Later, close the subscription
subscription.close();
```

**Message Interface:**

```typescript
interface Message {
  data: string; // Decoded message payload (string)
  topic: string; // Topic name
  timestamp: number; // Server timestamp in milliseconds
}
```

#### Debug Raw Envelopes

For debugging, you can inspect raw message envelopes before decoding:

```typescript
const subscription = await client.pubsub.subscribe("notifications", {
  onMessage: (msg) => {
    console.log("Decoded message:", msg.data);
  },
  onRaw: (envelope) => {
    console.log("Raw envelope:", envelope);
    // { data: "base64...", timestamp: 1234567890, topic: "notifications" }
  },
});
```

#### Multi-Subscriber Support

Multiple subscriptions to the same topic are supported. Each receives its own copy of messages:

```typescript
// First subscriber
const sub1 = await client.pubsub.subscribe("events", {
  onMessage: (msg) => console.log("Sub1:", msg.data),
});

// Second subscriber (both receive messages)
const sub2 = await client.pubsub.subscribe("events", {
  onMessage: (msg) => console.log("Sub2:", msg.data),
});

// Unsubscribe independently
sub1.close(); // sub2 still active
sub2.close(); // fully unsubscribed
```

#### List Topics

```typescript
const topics = await client.pubsub.topics();
console.log("Active topics:", topics);
```

### Presence Support

The SDK supports real-time presence tracking, allowing you to see who is currently subscribed to a topic.

#### Subscribe with Presence

Enable presence by providing `presence` options in `subscribe`:

```typescript
const subscription = await client.pubsub.subscribe("room.123", {
  onMessage: (msg) => console.log("Message:", msg.data),
  presence: {
    enabled: true,
    memberId: "user-alice",
    meta: { displayName: "Alice", avatar: "URL" },
    onJoin: (member) => {
      console.log(`${member.memberId} joined at ${new Date(member.joinedAt)}`);
      console.log("Meta:", member.meta);
    },
    onLeave: (member) => {
      console.log(`${member.memberId} left`);
    },
  },
});
```

#### Get Presence for a Topic

Query current members without subscribing:

```typescript
const presence = await client.pubsub.getPresence("room.123");
console.log(`Total members: ${presence.count}`);
presence.members.forEach((member) => {
  console.log(`- ${member.memberId} (joined: ${new Date(member.joinedAt)})`);
});
```

#### Subscription Helpers

Get presence information from an active subscription:

```typescript
if (subscription.hasPresence()) {
  const members = await subscription.getPresence();
  console.log("Current members:", members);
}
```

### Authentication

#### Switch API Key

```typescript
client.auth.setApiKey("ak_new_key:namespace");
```

#### Switch JWT

```typescript
client.auth.setJwt("new_jwt_token");
```

#### Get Current Token

```typescript
const token = client.auth.getToken(); // Returns API key or JWT
```

#### Get Authentication Info

```typescript
const info = await client.auth.whoami();
console.log(info.authenticated, info.namespace);
```

#### Logout

```typescript
await client.auth.logout();
```

### Network Operations

#### Check Health

```typescript
const healthy = await client.network.health();
```

#### Get Network Status

```typescript
const status = await client.network.status();
console.log(status.healthy, status.peers);
```

#### List Peers

```typescript
const peers = await client.network.peers();
peers.forEach((peer) => {
  console.log(peer.id, peer.addresses);
});
```

#### Proxy Requests Through Anyone Network

Make anonymous HTTP requests through the Anyone network:

```typescript
// Simple GET request
const response = await client.network.proxyAnon({
  url: "https://api.example.com/data",
  method: "GET",
  headers: {
    Accept: "application/json",
  },
});

console.log(response.status_code); // 200
console.log(response.body); // Response data as string
console.log(response.headers); // Response headers

// POST request with body
const postResponse = await client.network.proxyAnon({
  url: "https://api.example.com/submit",
  method: "POST",
  headers: {
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ key: "value" }),
});

// Parse JSON response
const data = JSON.parse(postResponse.body);
```

**Note:** The proxy endpoint requires authentication (API key or JWT) and only works when the Anyone relay is running on the gateway server.

## Configuration

### ClientConfig

```typescript
interface ClientConfig {
  baseURL: string; // Gateway URL
  apiKey?: string; // API key (optional, if using JWT instead)
  jwt?: string; // JWT token (optional, if using API key instead)
  timeout?: number; // Request timeout in ms (default: 30000)
  maxRetries?: number; // Max retry attempts (default: 3)
  retryDelayMs?: number; // Delay between retries (default: 1000)
  debug?: boolean; // Enable debug logging with full SQL queries (default: false)
  storage?: StorageAdapter; // For persisting JWT/API key (default: MemoryStorage)
  wsConfig?: Partial<WSClientConfig>; // WebSocket configuration
  fetch?: typeof fetch; // Custom fetch implementation
}
```

### Storage Adapters

By default, credentials are stored in memory. For browser apps, use localStorage:

```typescript
import { createClient, LocalStorageAdapter } from "@debros/network-ts-sdk";

const client = createClient({
  baseURL: "http://localhost:6001",
  storage: new LocalStorageAdapter(),
  apiKey: "ak_your_key:namespace",
});
```

### Cache Operations

The SDK provides a distributed cache client backed by Olric. Data is organized into distributed maps (dmaps).

#### Put a Value

```typescript
// Put with optional TTL
await client.cache.put("sessions", "user:alice", { role: "admin" }, "1h");
```

#### Get a Value

```typescript
// Returns null on cache miss (not an error)
const result = await client.cache.get("sessions", "user:alice");
if (result) {
  console.log(result.value); // { role: "admin" }
}
```

#### Delete a Value

```typescript
await client.cache.delete("sessions", "user:alice");
```

#### Multi-Get

```typescript
const results = await client.cache.multiGet("sessions", [
  "user:alice",
  "user:bob",
]);
// Returns Map<string, any | null> — null for misses
results.forEach((value, key) => {
  console.log(key, value);
});
```

#### Scan Keys

```typescript
// Scan all keys in a dmap, optionally matching a regex
const scan = await client.cache.scan("sessions", "user:.*");
console.log(scan.keys); // ["user:alice", "user:bob"]
console.log(scan.count); // 2
```

#### Health Check

```typescript
const health = await client.cache.health();
console.log(health.status); // "ok"
```

### Storage (IPFS)

Upload, pin, and retrieve files from decentralized IPFS storage.

#### Upload a File

```typescript
// Browser
const fileInput = document.querySelector('input[type="file"]');
const file = fileInput.files[0];
const result = await client.storage.upload(file, file.name);
console.log(result.cid); // "Qm..."

// Node.js
import { readFileSync } from "fs";
const buffer = readFileSync("image.jpg");
const result = await client.storage.upload(buffer, "image.jpg", { pin: true });
```

#### Retrieve Content

```typescript
// Get as ReadableStream
const stream = await client.storage.get(cid);
const reader = stream.getReader();
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  // Process chunk
}

// Get full Response (for headers like content-length)
const response = await client.storage.getBinary(cid);
const contentLength = response.headers.get("content-length");
```

#### Pin / Unpin / Status

```typescript
// Pin an existing CID
await client.storage.pin("QmExampleCid", "my-file");

// Check pin status
const status = await client.storage.status("QmExampleCid");
console.log(status.status); // "pinned", "pinning", "queued", "unpinned", "error"

// Unpin
await client.storage.unpin("QmExampleCid");
```

### Serverless Functions (WASM)

Invoke WebAssembly serverless functions deployed on the network.

```typescript
// Configure functions namespace
const client = createClient({
  baseURL: "http://localhost:6001",
  apiKey: "ak_your_key:namespace",
  functionsConfig: {
    namespace: "my-namespace",
  },
});

// Invoke a function with typed input/output
interface PushInput {
  token: string;
  message: string;
}
interface PushOutput {
  success: boolean;
  messageId: string;
}

const result = await client.functions.invoke<PushInput, PushOutput>(
  "send-push",
  { token: "device-token", message: "Hello!" }
);
console.log(result.messageId);
```

### Vault (Distributed Secrets)

The vault client provides Shamir-split secret storage across guardian nodes. Secrets are split into shares, distributed to guardians, and reconstructed only when enough shares are collected (quorum).

```typescript
const client = createClient({
  baseURL: "http://localhost:6001",
  apiKey: "ak_your_key:namespace",
  vaultConfig: {
    guardians: [
      { address: "10.0.0.1", port: 8443 },
      { address: "10.0.0.2", port: 8443 },
      { address: "10.0.0.3", port: 8443 },
    ],
    identityHex: "your-identity-hex",
  },
});

// Store a secret (Shamir-split across guardians)
const data = new TextEncoder().encode("my-secret-data");
const storeResult = await client.vault.store("api-key", data, 1);
console.log(storeResult.quorumMet); // true if enough guardians ACKed

// Retrieve and reconstruct a secret
const retrieved = await client.vault.retrieve("api-key");
console.log(new TextDecoder().decode(retrieved.data)); // "my-secret-data"

// List all secrets for this identity
const secrets = await client.vault.list();
console.log(secrets.secrets);

// Delete a secret from all guardians
await client.vault.delete("api-key");
```

### Wallet-Based Authentication

For wallet-based auth (challenge-response flow):

```typescript
// 1. Request a challenge
const challenge = await client.auth.challenge();

// 2. Sign the challenge with your wallet (external)
const signature = await wallet.signMessage(challenge.message);

// 3. Verify signature and get JWT
const session = await client.auth.verify(challenge.id, signature);
console.log(session.token);

// 4. Get an API key for long-lived access
const apiKey = await client.auth.getApiKey();
```

## Error Handling

The SDK throws `SDKError` for all errors:

```typescript
import { SDKError } from "@debros/network-ts-sdk";

try {
  await client.db.query("SELECT * FROM nonexistent");
} catch (error) {
  if (error instanceof SDKError) {
    console.log(error.httpStatus); // e.g., 400
    console.log(error.code); // e.g., "HTTP_400"
    console.log(error.message); // Error message
    console.log(error.details); // Full error response
  }
}
```

## Browser Usage

The SDK works in browsers with minimal setup:

```typescript
// Browser example
import { createClient } from "@debros/network-ts-sdk";

const client = createClient({
  baseURL: "https://gateway.example.com",
  apiKey: "ak_browser_key:my-app",
});

// Use like any other API client
const data = await client.db.query("SELECT * FROM items");
```

**Note**: For WebSocket connections in browsers with authentication, ensure your gateway supports either header-based auth or query parameter auth.

## Testing

Run E2E tests against a running gateway:

```bash
# Set environment variables
export GATEWAY_BASE_URL=http://localhost:6001
export GATEWAY_API_KEY=ak_test_key:default

# Run tests
npm run test:e2e
```

## Examples

See the `tests/e2e/` directory for complete examples of:

- Authentication (`auth.test.ts`)
- Database operations (`db.test.ts`)
- Transactions (`tx.test.ts`)
- Pub/Sub messaging (`pubsub.test.ts`)
- Network operations (`network.test.ts`)

## Building

```bash
npm run build
```

Output goes to `dist/` with ESM and type declarations.

## Development

```bash
npm run dev      # Watch mode
npm run typecheck # Type checking
npm run lint     # Linting (if configured)
```

## License

MIT

## Support

For issues, questions, or contributions, please open an issue on GitHub or visit [DeBros Network Documentation](https://network.debros.io/docs/).
