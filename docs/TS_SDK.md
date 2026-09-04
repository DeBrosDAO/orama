# TypeScript SDK

`@debros/orama` is the programmatic surface of an Orama namespace. Everything a
deployed application does — query its database, publish to a topic, cache a
value, store a file, invoke a function — goes through one client against one
gateway URL.

There is also a Go client, documented in [GO_CLIENT_SDK.md](GO_CLIENT_SDK.md).
It talks to the same gateway. Use whichever matches the code you are writing.

The vault is **not** in this package. See [Secrets](#secrets) below.

---

## Install

```bash
npm install @debros/orama
```

The package ships ESM and CommonJS, so both `import` and `require` work, in
Node and in a browser bundle. Node 20 or later.

---

## The client

```typescript
import { createClient } from "@debros/orama";

const client = createClient({
  baseURL: "https://ns-myapp.orama-devnet.network",
  apiKey: process.env.ORAMA_API_KEY,
});
```

`createClient` returns one object with a member per module:

| Member | What it reaches |
|--------|-----------------|
| `client.auth` | Credentials, wallet login, session renewal |
| `client.db` | The namespace's RQLite database |
| `client.pubsub` | Topics, over HTTP to publish and WebSocket to subscribe |
| `client.cache` | The Olric distributed cache |
| `client.storage` | IPFS upload, pin and retrieve |
| `client.functions` | Serverless WASM function invocation |
| `client.network` | Gateway health, cluster status, peers, and the anonymity proxy |

### ClientConfig

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `baseURL` | `string` | — | Gateway URL. Required. |
| `apiKey` | `string` | — | API key, in the form `ak_<id>:<namespace>` |
| `jwt` | `string` | — | An existing session token |
| `timeout` | `number` | `60000` | Per-request timeout in milliseconds |
| `maxRetries` | `number` | `3` | Attempts for a retryable status (408, 429, 5xx) |
| `retryDelayMs` | `number` | `1000` | Base backoff, multiplied by the attempt and jittered |
| `debug` | `boolean` | `false` | Print diagnostics. With it off the SDK writes nothing to the console. |
| `storage` | `StorageAdapter` | `MemoryStorage` | Where tokens are persisted |
| `wsConfig` | `Partial<WSClientConfig>` | — | WebSocket options, including `reconnect` |
| `functionsConfig` | `FunctionsClientConfig` | — | `namespace`, and an optional separate `gatewayURL` |
| `fetch` | `typeof fetch` | the platform's | Replace the fetch used for every request |
| `onNetworkError` | `NetworkErrorCallback` | — | Called when a request fails to reach the gateway |

---

## Where a key belongs

An API key carries grants, and a key in a browser bundle is a key in the hands
of everyone who loads the page. The split the gateway enforces:

- **Server-side only** — an `admin` key. It reaches the control plane: deploys,
  secrets, migrations, key management, raw RQLite. It belongs in CI and on a
  developer's machine, never in anything a browser downloads.
- **Safe in a client bundle** — an `app-runtime` key. Data-plane grants only,
  and the gateway additionally requires a logged-in user's wallet JWT for
  storage, WebRTC and proxy operations, so an extracted runtime key on its own
  cannot act as anybody.

```bash
orama namespace keys create --scope app-runtime --label web
orama namespace keys create --scope admin --label ci
```

In a browser, obtain the user's JWT with the wallet flow below and let the
runtime key handle the rest. In a server process, read the key from the
environment; do not commit it.

---

## Authentication

### API key

```typescript
const client = createClient({ baseURL, apiKey: process.env.ORAMA_API_KEY });
```

### Wallet login

A user proves ownership of an address by signing a nonce. The result is a JWT
scoped to the namespace.

```typescript
const challenge = await client.auth.challenge({ wallet: "0xYourWallet" });
const signature = await wallet.signMessage(challenge.nonce);

const session = await client.auth.verify({
  wallet: "0xYourWallet",
  nonce: challenge.nonce,
  signature,
  chain_type: "ETH", // or "SOL"
});
```

`verify` stores the access token, the refresh token and the namespace the token
was issued for, so renewal below happens on its own. Nonces are single-use: sign
a fresh challenge for each call.

`client.auth.getApiKey({ wallet, nonce, signature })` mints a long-lived key the
same way, from its own fresh challenge.

### Sessions renew themselves

When the gateway answers 401 and a refresh token is stored, the SDK renews the
session and replays the request once. The application does not see the 401.
Concurrent requests that expire together share a single renewal.

```typescript
await client.auth.logout();                     // tell the gateway, clear everything
await client.auth.logout({ keepApiKey: true }); // the user signs out, the app key stays
await client.auth.logout({ server: false });    // clear local state only
```

`client.auth.whoami()` answers `{ authenticated: false }` for a rejected
credential and **throws** for anything else, so a gateway that is down is not
reported as a signed-out user.

`client.auth.getToken()` returns the credential that would be sent as a Bearer
token: the JWT when one is set, otherwise the API key.

### Persisting tokens in a browser

```typescript
import { createClient, LocalStorageAdapter } from "@debros/orama";

const client = createClient({ baseURL, apiKey, storage: new LocalStorageAdapter() });
```

---

## Database

The namespace's RQLite database, reached four ways depending on how much
structure you want.

```typescript
// Raw SQL
const rows = await client.db.query("SELECT * FROM messages WHERE room = ?", ["general"]);
await client.db.exec("INSERT INTO messages (room, body) VALUES (?, ?)", ["general", "hi"]);

// Criteria
const recent = await client.db.find("messages", { room: "general" }, { limit: 20 });
const one = await client.db.findOne("messages", { id: 42 }); // null when absent

// Query builder
const page = await client.db
  .createQueryBuilder("messages")
  .where("room = ?", ["general"])
  .orderBy("created_at DESC")
  .limit(20)
  .getMany();

// Repository
const messages = client.db.repository<Message>("messages");
await messages.save({ room: "general", body: "hi" });
```

A row that is not there is `null` from both `findOne` methods, never an
exception.

Table, primary-key and column names are validated as plain SQL identifiers.
Values are parameterised; identifiers cannot be, so a name that is not
`[A-Za-z_][A-Za-z0-9_]*` is rejected with `INVALID_IDENTIFIER` rather than
interpolated into the statement.

---

## Pub/sub

```typescript
await client.pubsub.publish("room:general", "hello");
await client.pubsub.publish("room:general", new Uint8Array([1, 2, 3]));

const subscription = await client.pubsub.subscribe("room:general", {
  onMessage: (msg) => {
    msg.data;      // the payload decoded as UTF-8 text
    msg.bytes;     // the payload exactly as published
    msg.timestamp; // server-assigned, so ordering does not depend on client clocks
  },
  onError: (error) => report(error),
  onClose: () => teardown(),
});

subscription.close();
```

`data` is lossy for a payload that is not text: bytes that do not form valid
UTF-8 become U+FFFD. Read `bytes` for anything binary.

A dropped connection is re-established with exponential backoff — ten attempts
by default — and the subscription resumes on the new socket. `onClose` fires
only when the subscription is finished for good, so it is not raised for a drop
that is about to be repaired. Configure or disable it with
`wsConfig.reconnect`.

Presence is opt-in per subscription:

```typescript
await client.pubsub.subscribe("room:general", {
  presence: {
    enabled: true,
    memberId: userId,
    onJoin: (member) => show(member),
    onLeave: (member) => hide(member),
  },
});
```

---

## Cache

```typescript
await client.cache.put("sessions", userId, { seen: Date.now() }, "1h");

const hit = await client.cache.get("sessions", userId); // null on a miss
if (hit) console.log(hit.value);

await client.cache.delete("sessions", userId);
```

The distributed map name is the first argument; keys are scoped to it. The TTL
is a duration string such as `"30m"` or `"1h"`, and is optional.

---

## Storage

```typescript
const { cid } = await client.storage.upload(file, file.name);
const status = await client.storage.status(cid);

const stream = await client.storage.get(cid);       // ReadableStream
const response = await client.storage.getBinary(cid); // Response, with headers

await client.storage.unpin(cid);
```

A read that closely follows a write can legitimately 404 while the pin
propagates across the cluster, so reads retry on 404 for about eighteen seconds
before giving up. Any other failure is returned immediately.

---

## Functions

```typescript
const client = createClient({
  baseURL,
  apiKey,
  functionsConfig: { namespace: "myapp" },
});

const result = await client.functions.invoke("resize", { cid, width: 400 });
```

Set `functionsConfig.gatewayURL` to send invocations to a different origin with
the same credentials. Writing and deploying functions is covered in
[SERVERLESS.md](SERVERLESS.md).

---

## Cancelling work

Every request method takes an `AbortSignal`.

```typescript
const controller = new AbortController();
const search = client.db.query("SELECT * FROM big_table", [], {
  signal: controller.signal,
});

controller.abort(); // rejects with a NetworkError whose code is "ABORTED"
```

A cancelled request is never retried and is not reported through
`onNetworkError`: pressing Cancel is not a network failure.

---

## Errors

Every failure is an `SDKError`, so one `catch` covers the SDK. Four subclasses
name the cases an application usually handles differently:

| Class | When | Extra |
|-------|------|-------|
| `AuthError` | 401 — credential missing, expired, or not enough alone | `requiredScope` |
| `ScopeError` | 403 — the credential's grants do not cover the operation | `requiredScope` |
| `NotFoundError` | 404 | |
| `NetworkError` | No HTTP response at all. `httpStatus` is always 0 | `code` is `NETWORK_ERROR`, `TIMEOUT` or `ABORTED` |

```typescript
import { NetworkError, ScopeError } from "@debros/orama";

try {
  await client.storage.upload(file, file.name);
} catch (error) {
  if (error instanceof ScopeError) {
    console.error(`this key needs the ${error.requiredScope} grant`);
  } else if (error instanceof NetworkError) {
    console.error("the gateway was not reached");
  } else {
    throw error;
  }
}
```

`httpStatus` of 0 is how "never reached the gateway" is told apart from a real
4xx or 5xx.

### Grants

```typescript
import { SCOPES, PROFILE_SCOPES, satisfiesScope } from "@debros/orama";

PROFILE_SCOPES["app-runtime"]; // ["invoke", "storage", "push", "webrtc", "proxy"]
satisfiesScope(["admin"], "storage"); // true — admin is a wildcard
```

The grants are `admin`, `cache`, `invoke`, `proxy`, `pubsub`, `push`, `storage`
and `webrtc`. They mirror the gateway's own list, and a test in the SDK fails if
the two ever disagree.

---

## Retries and failover

A retryable status (408, 429, 500, 502, 503, 504) is retried up to `maxRetries`
times. A `Retry-After` header is honoured, capped at thirty seconds; otherwise
the delay grows with the attempt and carries up to 25% jitter so clients that
failed together do not return together.

Failing over to another gateway is the application's decision, and
`onNetworkError` is where to make it:

```typescript
const client = createClient({
  baseURL: gateways[0],
  apiKey,
  onNetworkError: (error, context) => {
    if (error.httpStatus === 0) rotateGateway(context.path);
  },
});
```

---

## Secrets

The vault client is a separate package, `@debros/orama-vault`. It talks to
guardian daemons on the WireGuard overlay, which an application outside the
cluster cannot reach.

An application reaches the vault through the gateway's `/v1/vault/push` and
`/v1/vault/pull` endpoints over HTTPS, which do the Shamir split server-side and
authenticate each request with a per-request Ed25519 ownership signature. See
[vault/SECURITY_MODEL.md](vault/SECURITY_MODEL.md).

---

## Reaching a gateway with an untrusted certificate

A test cluster may present a certificate your runtime does not trust. Supply a
`fetch` that relaxes verification **for that connection only**:

```typescript
import { Agent, fetch as undiciFetch } from "undici";

const dispatcher = new Agent({ connect: { rejectUnauthorized: false } });
const client = createClient({
  baseURL,
  apiKey,
  fetch: (input, init) =>
    undiciFetch(input, { ...init, dispatcher }) as unknown as Promise<Response>,
});
```

The SDK never changes the process's TLS settings on your behalf: doing so would
disable certificate verification for every other HTTPS client in the same
process.

---

## Testing against a gateway

The SDK's own end-to-end suite runs only when it has a gateway to talk to:

```bash
export GATEWAY_BASE_URL=https://ns-myapp.orama-devnet.network
export GATEWAY_API_KEY=ak_test:myapp
pnpm test:e2e
```

Without `GATEWAY_API_KEY` those tests report as skipped. The unit suite needs
nothing.
