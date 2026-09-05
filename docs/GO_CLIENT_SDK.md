# Orama Network Go Client SDK

## Overview

The Orama Network Go Client SDK provides a clean, type-safe Go interface for interacting with the Orama Network. It abstracts away the complexity of peer connections, authentication, and error handling.

For TypeScript, see [TS_SDK.md](TS_SDK.md). Both talk to the same gateway; use
whichever matches the code you are writing.

## Installation

```bash
go get github.com/DeBrosOfficial/network/pkg/client
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "strings"

    "github.com/DeBrosOfficial/network/pkg/client"
)

func main() {
    // Create client configuration
    cfg := client.DefaultClientConfig("my-app")
    cfg.GatewayURL = "https://api.orama.network"
    cfg.APIKey = "your-api-key-here"

    // Create client
    c, err := client.NewClient(cfg)
    if err != nil {
        log.Fatal(err)
    }

    // Connect to the network
    if err := c.Connect(); err != nil {
        log.Fatal(err)
    }
    defer c.Disconnect()

    // Use the client
    ctx := context.Background()

    // Upload to storage
    resp, err := c.Storage().Upload(ctx, strings.NewReader("Hello, Orama!"), "hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Uploaded: CID=%s\n", resp.Cid)
}
```

## Configuration

### ClientConfig

```go
type ClientConfig struct {
    AppName           string        // Application name (required)
    DatabaseName      string        // Database name (default: "<AppName>_db")
    BootstrapPeers    []string      // LibP2P bootstrap peer multiaddresses
    DatabaseEndpoints []string      // RQLite database endpoints
    GatewayURL        string        // Gateway URL for HTTP API access
    ConnectTimeout    time.Duration // Connection timeout (default: 30s)
    RetryAttempts     int           // Retry attempts (default: 3)
    RetryDelay        time.Duration // Delay between retries (default: 5s)
    QuietMode         bool          // Suppress debug/info logs
    APIKey            string        // API key for gateway auth
    JWT               string        // Optional JWT bearer token
    IdentityPath      string        // Path to persistent LibP2P identity key file
}
```

### Creating a Client

```go
// Default configuration (app name is required)
cfg := client.DefaultClientConfig("my-app")
cfg.GatewayURL = "https://api.orama.network"
cfg.APIKey = "your-api-key"

c, err := client.NewClient(cfg)
if err != nil {
    log.Fatal(err)
}
```

`DefaultClientConfig` fills in default bootstrap peers and database endpoints; `NewClient` returns an error if the config is nil or the app name is empty.

## Authentication

### API Key Authentication

```go
cfg := client.DefaultClientConfig("my-app")
cfg.APIKey = "your-api-key-here"
c, err := client.NewClient(cfg)
```

### JWT Token Authentication

```go
cfg := client.DefaultClientConfig("my-app")
cfg.JWT = "your-jwt-token-here"
c, err := client.NewClient(cfg)
```

### Obtaining Credentials

```go
// 1. Login with wallet signature (not yet implemented in SDK)
// Use the gateway API directly: POST /v1/auth/challenge + /v1/auth/verify

// 2. Issue API key after authentication
// POST /v1/auth/api-key with JWT token
```

## Storage Client

Upload, download, pin, and unpin files to IPFS.

### Upload File

Upload takes an `io.Reader`:

```go
resp, err := c.Storage().Upload(ctx, strings.NewReader("Hello, World!"), "hello.txt")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("CID: %s, Size: %d\n", resp.Cid, resp.Size)
```

### Get File

Get returns an `io.ReadCloser`:

```go
cid := "QmXxx..."
rc, err := c.Storage().Get(ctx, cid)
if err != nil {
    log.Fatal(err)
}
defer rc.Close()

data, err := io.ReadAll(rc)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Downloaded %d bytes\n", len(data))
```

### Pin File

Pin takes the CID and a name:

```go
cid := "QmXxx..."
resp, err := c.Storage().Pin(ctx, cid, "hello.txt")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Pinned: %s\n", resp.Cid)
```

### Unpin File

```go
cid := "QmXxx..."
err := c.Storage().Unpin(ctx, cid)
if err != nil {
    log.Fatal(err)
}
fmt.Println("Unpinned successfully")
```

By default a successful unpin removes the pin immediately, but the underlying
blocks are physically reclaimed only by the periodic GC sweep (~6h). For a
**privacy-grade delete** ("delete for everyone" / "free up space"), call
`DELETE /v1/storage/unpin/:cid?immediate=true`: when the CID's **last** pin is
removed (no other namespace still references it), the blocks are evicted
cluster-wide right away so the content is no longer fetchable. Use it only for
genuine privacy deletes — it fans out per-node work and should not be set on
routine unpins (e.g. avatar rotation) (bugboard #153).

The response carries an `evicted` field:

| Value | Meaning |
|---|---|
| `"true"` | Every active node confirmed a complete local reclaim. The bytes are gone cluster-wide. |
| `"partial"` | At least one node did not confirm — unreachable, errored, or reported an incomplete reclaim. The pin is still removed everywhere, but the blocks survive on that node until the next GC sweep (≤6h). |
| `"shared"` | Another namespace still pins this CID, so nothing was evicted. Correct and expected for deduplicated content. |
| `"skipped"` | Immediate reclaim was not requested, or the reference check could not run. |

Only `"true"` is a delete-for-everyone guarantee. Do not surface "permanently
deleted" to a user on a `"partial"`; log it, because it means some node still
holds the content.

### Check Pin Status

```go
cid := "QmXxx..."
status, err := c.Storage().Status(ctx, cid)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Status: %s, Replication factor: %d, Peers: %v\n",
    status.Status, status.ReplicationFactor, status.Peers)
```

`StorageStatus` includes `Cid`, `Name`, `Status` (`"pinned"`, `"pinning"`, `"queued"`, `"unpinned"`, `"error"`), `ReplicationMin`, `ReplicationMax`, `ReplicationFactor`, `Peers`, and `Error`.

## Cache Client

**Not yet available.** The SDK does not currently expose the Olric distributed cache — there is no `Cache()` accessor on the client. Caching is used internally by the gateway.

## Database Client

Query RQLite distributed SQL database.

### Write Query

Writes (INSERT, UPDATE, DELETE, CREATE, DROP, ...) go through `Query` as well — the client detects write statements and executes them accordingly:

```go
sql := "INSERT INTO users (name, email) VALUES (?, ?)"

_, err := c.Database().Query(ctx, sql, "Alice", "alice@example.com")
if err != nil {
    log.Fatal(err)
}
```

### Read Query

`Query` returns a `*QueryResult` with `Columns`, `Rows`, and `Count`:

```go
sql := "SELECT id, name, email FROM users WHERE id = ?"

result, err := c.Database().Query(ctx, sql, 123)
if err != nil {
    log.Fatal(err)
}

for _, row := range result.Rows {
    // row is []interface{}, ordered to match result.Columns
    fmt.Println(row)
}
```

### Create Table

```go
schema := `CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`

err := c.Database().CreateTable(ctx, schema)
if err != nil {
    log.Fatal(err)
}
```

There is also `DropTable(ctx, tableName)` and `GetSchema(ctx)` for schema introspection.

### Transaction

Transactions take a slice of SQL statements that are executed atomically:

```go
queries := []string{
    "INSERT INTO users (name) VALUES ('Alice')",
    "INSERT INTO users (name) VALUES ('Bob')",
}

err := c.Database().Transaction(ctx, queries)
if err != nil {
    log.Fatal(err)
}
```

## PubSub Client

Publish and subscribe to topics.

### Publish Message

```go
topic := "chat"
message := []byte("Hello, everyone!")

err := c.PubSub().Publish(ctx, topic, message)
if err != nil {
    log.Fatal(err)
}
```

### Subscribe to Topic

The handler is a `func(topic string, data []byte) error`:

```go
topic := "chat"
handler := func(topic string, data []byte) error {
    fmt.Printf("Received on %s: %s\n", topic, string(data))
    return nil
}

err := c.PubSub().Subscribe(ctx, topic, handler)
if err != nil {
    log.Fatal(err)
}

// Later: unsubscribe
defer c.PubSub().Unsubscribe(ctx, topic)
```

### Batch Publish

`PublishBatch` publishes multiple messages in parallel (one per topic), and `PublishSame` sends the same payload to every topic:

```go
msgs := []client.TopicMessage{
    {Topic: "chat", Data: []byte("hello")},
    {Topic: "alerts", Data: []byte("warning")},
}
err := c.PubSub().PublishBatch(ctx, msgs, client.PublishBatchOptions{})
```

### List Topics

```go
topics, err := c.PubSub().ListTopics(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Topics: %v\n", topics)
```

## Anonymity Proxy

Two endpoints route traffic through the Anyone network so the destination never
learns the end user's IP. Both require the `proxy` grant **and** a genuine
end-user (SIWE wallet) JWT — an app-runtime API key alone is refused, because
these are per-user capabilities and an extracted bundle key must not carry them.

Neither is exposed by the SDK yet; call them over HTTP.

### Request proxy — `POST /v1/proxy/anon`

The gateway performs one HTTP request on the caller's behalf and returns the
response. Simple, but the gateway is inside the TLS boundary: it sees the URL,
the headers and the body in cleartext. Appropriate for fetching a single
document (a link preview, an article); **not** appropriate for general browsing,
where it would make the gateway a complete browsing-history observer.

### Anonymity tunnel — `GET /v1/proxy/tunnel` (WebSocket)

Carries an **opaque TCP stream**. The client negotiates TLS end-to-end with the
destination *through* the tunnel, so the gateway relays ciphertext.

```
wss://ns-<namespace>.<base-domain>/v1/proxy/tunnel?host=example.com&port=443&jwt=<user JWT>
```

- `host` — destination hostname or public IP. Required. Resolved at the exit
  relay, never by the gateway.
- `port` — `80` or `443`. Optional, defaults to `443`.
- Authenticate with `Authorization: Bearer <jwt>`, or `?jwt=` when the client
  cannot set headers on a WebSocket handshake (browsers, React Native).

Once open, every **binary** WebSocket frame is written to the destination and
every chunk read back is delivered as a binary frame. There is no framing of our
own — write TLS records, read TLS records. A text frame is a protocol violation
and closes the tunnel.

**What the gateway can and cannot see.** It cannot see paths, headers, bodies or
any content: that is all inside the TLS session it is relaying. It *can* see the
destination host and port, because it cannot dial a connection without them.
This is true of every tunnelling proxy. Do not tell users the tunnel hides which
sites they visit from the platform — it hides *what they do* there, and hides
*them* from the site.

**Using it as a browser proxy.** Web views take a proxy configuration, not a
WebSocket. Run a small loopback HTTP-CONNECT relay inside the app and point the
web view at it: for each local `CONNECT host:port`, open one tunnel and splice.
The relay is also where the user's JWT is attached, which is required anyway on
the platform that cannot supply proxy credentials from its web view.

**Limits** (per node):

| Limit | Value |
|---|---|
| Destination ports | 80, 443 only |
| Concurrent tunnels per user | 24 |
| Concurrent tunnels per node | 512 |
| Bytes per tunnel, each direction | 256 MiB |
| Idle timeout | 2 minutes |
| Maximum tunnel lifetime | 30 minutes |

Exceeding a concurrency cap returns `429` with `Retry-After`. A refused
destination returns `400`. An unreachable destination returns `502` with a
deliberately generic message — a tunnel that reports exactly why a dial failed
is a port scanner for whatever the exit relay can reach.

**Private destinations are refused**: loopback, RFC1918, link-local,
carrier-grade NAT, multicast and `localhost` are all rejected, so a tunnel
cannot be aimed at the WireGuard mesh or a cloud metadata endpoint.

**Circuit isolation.** Each user gets their own circuit through the anonymity
network, selected by an HMAC of their identity under a node-local secret. Users
on the same node are therefore not linkable to one another at the exit, and the
anonymity client never receives a wallet address.

## Serverless Client

**Not yet available in the SDK.** The Go client does not expose a `Serverless()` accessor. Serverless functions are written in Go, compiled with TinyGo, and deployed/managed via the `orama function` CLI — see `SERVERLESS.md` for the full workflow.

## Error Handling

All client methods return typed errors that can be checked:

```go
import "github.com/DeBrosOfficial/network/pkg/errors"

resp, err := c.Storage().Upload(ctx, reader, "file.txt")
if err != nil {
    if errors.IsNotFound(err) {
        fmt.Println("Resource not found")
    } else if errors.IsUnauthorized(err) {
        fmt.Println("Authentication failed")
    } else if errors.IsValidation(err) {
        fmt.Println("Validation error")
    } else {
        log.Fatal(err)
    }
}
```

## Advanced Usage

### Custom Timeout

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

resp, err := c.Storage().Upload(ctx, reader, "file.txt")
```

### Retry Logic

```go
import "github.com/DeBrosOfficial/network/pkg/errors"

maxRetries := 3
for i := 0; i < maxRetries; i++ {
    resp, err := c.Storage().Upload(ctx, reader, "file.txt")
    if err == nil {
        break
    }
    if !errors.ShouldRetry(err) {
        return err
    }
    time.Sleep(time.Second * time.Duration(i+1))
}
```

### Namespaces

The client's namespace is derived from its credentials (API key or JWT) — there is no per-request namespace override. To operate in multiple namespaces, create separate clients with different credentials:

```go
cfg1 := client.DefaultClientConfig("app-one")
cfg1.APIKey = "api-key-for-namespace-one"
c1, err := client.NewClient(cfg1)

cfg2 := client.DefaultClientConfig("app-two")
cfg2.APIKey = "api-key-for-namespace-two"
c2, err := client.NewClient(cfg2)
```

## Testing

The SDK does not ship mock implementations. `NetworkClient`, `StorageClient`, `DatabaseClient`, and `PubSubClient` are interfaces, so you can write your own mocks (or generate them) for unit tests:

```go
type mockStorage struct {
    client.StorageClient
}

func (m *mockStorage) Upload(ctx context.Context, r io.Reader, name string) (*client.StorageUploadResult, error) {
    return &client.StorageUploadResult{Cid: "QmMock", Name: name}, nil
}
```

## Examples

Serverless function examples (hello, echo, counter) live in `docs/examples/functions/`. There are currently no standalone SDK example programs in the repository.

## API Reference

Complete API documentation is available at:
- GoDoc: https://pkg.go.dev/github.com/DeBrosOfficial/network/pkg/client

## Support

- GitHub Issues: https://github.com/DeBrosOfficial/network/issues
- Documentation: https://github.com/DeBrosOfficial/network/tree/main/docs
