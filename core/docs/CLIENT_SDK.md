# Orama Network Client SDK

## Overview

The Orama Network Client SDK provides a clean, type-safe Go interface for interacting with the Orama Network. It abstracts away the complexity of HTTP requests, authentication, and error handling.

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

    "github.com/DeBrosOfficial/network/pkg/client"
)

func main() {
    // Create client configuration
    cfg := client.DefaultClientConfig()
    cfg.GatewayURL = "https://api.orama.network"
    cfg.APIKey = "your-api-key-here"

    // Create client
    c := client.NewNetworkClient(cfg)

    // Use the client
    ctx := context.Background()

    // Upload to storage
    data := []byte("Hello, Orama!")
    resp, err := c.Storage().Upload(ctx, data, "hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Uploaded: CID=%s\n", resp.CID)
}
```

## Configuration

### ClientConfig

```go
type ClientConfig struct {
    // Gateway URL (e.g., "https://api.orama.network")
    GatewayURL string

    // Authentication (choose one)
    APIKey    string  // API key authentication
    JWTToken  string  // JWT token authentication

    // Client options
    Timeout   time.Duration  // Request timeout (default: 30s)
    UserAgent string         // Custom user agent

    // Network client namespace
    Namespace string  // Default namespace for operations
}
```

### Creating a Client

```go
// Default configuration
cfg := client.DefaultClientConfig()
cfg.GatewayURL = "https://api.orama.network"
cfg.APIKey = "your-api-key"

c := client.NewNetworkClient(cfg)
```

## Authentication

### API Key Authentication

```go
cfg := client.DefaultClientConfig()
cfg.APIKey = "your-api-key-here"
c := client.NewNetworkClient(cfg)
```

### JWT Token Authentication

```go
cfg := client.DefaultClientConfig()
cfg.JWTToken = "your-jwt-token-here"
c := client.NewNetworkClient(cfg)
```

### Obtaining Credentials

```go
// 1. Login with wallet signature (not yet implemented in SDK)
// Use the gateway API directly: POST /v1/auth/challenge + /v1/auth/verify

// 2. Issue API key after authentication
// POST /v1/auth/apikey with JWT token
```

## Storage Client

Upload, download, pin, and unpin files to IPFS.

### Upload File

```go
data := []byte("Hello, World!")
resp, err := c.Storage().Upload(ctx, data, "hello.txt")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("CID: %s\n", resp.CID)
```

### Upload with Options

```go
opts := &client.StorageUploadOptions{
    Pin:               true,           // Pin after upload
    Encrypt:           true,           // Encrypt before upload
    ReplicationFactor: 3,              // Number of replicas
}
resp, err := c.Storage().UploadWithOptions(ctx, data, "file.txt", opts)
```

### Get File

```go
cid := "QmXxx..."
data, err := c.Storage().Get(ctx, cid)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Downloaded %d bytes\n", len(data))
```

### Pin File

```go
cid := "QmXxx..."
resp, err := c.Storage().Pin(ctx, cid)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Pinned: %s\n", resp.CID)
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

### Check Pin Status

```go
cid := "QmXxx..."
status, err := c.Storage().Status(ctx, cid)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Status: %s, Replicas: %d\n", status.Status, status.Replicas)
```

## Cache Client

Distributed key-value cache using Olric.

### Set Value

```go
key := "user:123"
value := map[string]interface{}{
    "name": "Alice",
    "email": "alice@example.com",
}
ttl := 5 * time.Minute

err := c.Cache().Set(ctx, key, value, ttl)
if err != nil {
    log.Fatal(err)
}
```

### Get Value

```go
key := "user:123"
var user map[string]interface{}
err := c.Cache().Get(ctx, key, &user)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("User: %+v\n", user)
```

### Delete Value

```go
key := "user:123"
err := c.Cache().Delete(ctx, key)
if err != nil {
    log.Fatal(err)
}
```

### Multi-Get

```go
keys := []string{"user:1", "user:2", "user:3"}
results, err := c.Cache().MGet(ctx, keys)
if err != nil {
    log.Fatal(err)
}
for key, value := range results {
    fmt.Printf("%s: %v\n", key, value)
}
```

## Database Client

Query RQLite distributed SQL database.

### Execute Query (Write)

```go
sql := "INSERT INTO users (name, email) VALUES (?, ?)"
args := []interface{}{"Alice", "alice@example.com"}

result, err := c.Database().Execute(ctx, sql, args...)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Inserted %d rows\n", result.RowsAffected)
```

### Query (Read)

```go
sql := "SELECT id, name, email FROM users WHERE id = ?"
args := []interface{}{123}

rows, err := c.Database().Query(ctx, sql, args...)
if err != nil {
    log.Fatal(err)
}

type User struct {
    ID    int    `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

var users []User
for _, row := range rows {
    var user User
    // Parse row into user struct
    // (manual parsing required, or use ORM layer)
    users = append(users, user)
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

_, err := c.Database().Execute(ctx, schema)
if err != nil {
    log.Fatal(err)
}
```

### Transaction

```go
tx, err := c.Database().Begin(ctx)
if err != nil {
    log.Fatal(err)
}

_, err = tx.Execute(ctx, "INSERT INTO users (name) VALUES (?)", "Alice")
if err != nil {
    tx.Rollback(ctx)
    log.Fatal(err)
}

_, err = tx.Execute(ctx, "INSERT INTO users (name) VALUES (?)", "Bob")
if err != nil {
    tx.Rollback(ctx)
    log.Fatal(err)
}

err = tx.Commit(ctx)
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

```go
topic := "chat"
handler := func(ctx context.Context, msg []byte) error {
    fmt.Printf("Received: %s\n", string(msg))
    return nil
}

unsubscribe, err := c.PubSub().Subscribe(ctx, topic, handler)
if err != nil {
    log.Fatal(err)
}

// Later: unsubscribe
defer unsubscribe()
```

### List Topics

```go
topics, err := c.PubSub().ListTopics(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Topics: %v\n", topics)
```

## Serverless Client

Deploy and invoke WebAssembly functions.

### Deploy Function

```go
// Read WASM file
wasmBytes, err := os.ReadFile("function.wasm")
if err != nil {
    log.Fatal(err)
}

// Function definition
def := &client.FunctionDefinition{
    Name:        "hello-world",
    Namespace:   "default",
    Description: "Hello world function",
    MemoryLimit: 64, // MB
    Timeout:     30, // seconds
}

// Deploy
fn, err := c.Serverless().Deploy(ctx, def, wasmBytes)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Deployed: %s (CID: %s)\n", fn.Name, fn.WASMCID)
```

### Invoke Function

```go
functionName := "hello-world"
input := map[string]interface{}{
    "name": "Alice",
}

output, err := c.Serverless().Invoke(ctx, functionName, input)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Result: %s\n", output)
```

### List Functions

```go
functions, err := c.Serverless().List(ctx)
if err != nil {
    log.Fatal(err)
}
for _, fn := range functions {
    fmt.Printf("- %s: %s\n", fn.Name, fn.Description)
}
```

### Delete Function

```go
functionName := "hello-world"
err := c.Serverless().Delete(ctx, functionName)
if err != nil {
    log.Fatal(err)
}
```

### Get Function Logs

```go
functionName := "hello-world"
logs, err := c.Serverless().GetLogs(ctx, functionName, 100)
if err != nil {
    log.Fatal(err)
}
for _, log := range logs {
    fmt.Printf("[%s] %s: %s\n", log.Timestamp, log.Level, log.Message)
}
```

## Error Handling

All client methods return typed errors that can be checked:

```go
import "github.com/DeBrosOfficial/network/pkg/errors"

resp, err := c.Storage().Upload(ctx, data, "file.txt")
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

resp, err := c.Storage().Upload(ctx, data, "file.txt")
```

### Retry Logic

```go
import "github.com/DeBrosOfficial/network/pkg/errors"

maxRetries := 3
for i := 0; i < maxRetries; i++ {
    resp, err := c.Storage().Upload(ctx, data, "file.txt")
    if err == nil {
        break
    }
    if !errors.ShouldRetry(err) {
        return err
    }
    time.Sleep(time.Second * time.Duration(i+1))
}
```

### Multiple Namespaces

```go
// Default namespace
c1 := client.NewNetworkClient(cfg)
c1.Storage().Upload(ctx, data, "file.txt") // Uses default namespace

// Override namespace per request
opts := &client.StorageUploadOptions{
    Namespace: "custom-namespace",
}
c1.Storage().UploadWithOptions(ctx, data, "file.txt", opts)
```

## Testing

### Mock Client

```go
// Create a mock client for testing
mockClient := &MockNetworkClient{
    StorageClient: &MockStorageClient{
        UploadFunc: func(ctx context.Context, data []byte, filename string) (*UploadResponse, error) {
            return &UploadResponse{CID: "QmMock"}, nil
        },
    },
}

// Use in tests
resp, err := mockClient.Storage().Upload(ctx, data, "test.txt")
assert.NoError(t, err)
assert.Equal(t, "QmMock", resp.CID)
```

## Examples

See the `examples/` directory for complete examples:

- `examples/storage/` - Storage upload/download examples
- `examples/cache/` - Cache operations
- `examples/database/` - Database queries
- `examples/pubsub/` - Pub/sub messaging
- `examples/serverless/` - Serverless functions

## API Reference

Complete API documentation is available at:
- GoDoc: https://pkg.go.dev/github.com/DeBrosOfficial/network/pkg/client
- OpenAPI: `openapi/gateway.yaml`

## Support

- GitHub Issues: https://github.com/DeBrosOfficial/network/issues
- Documentation: https://github.com/DeBrosOfficial/network/tree/main/docs
