# Gateway API Documentation

## Overview

The Orama Network Gateway provides a unified HTTP/HTTPS API for all network services. It handles authentication, routing, and service coordination.

**Base URL:** `https://api.orama.network` (production) or `http://localhost:6001` (development)

## Authentication

All API requests (except `/health` and `/v1/auth/*`) require authentication.

### Authentication Methods

1. **API Key** (Recommended for server-to-server)
2. **JWT Token** (Recommended for user sessions)
3. **Wallet Signature** (For blockchain integration)

### Using API Keys

Include your API key in the `Authorization` header:

```bash
curl -H "Authorization: Bearer your-api-key-here" \
     https://api.orama.network/v1/status
```

Or in the `X-API-Key` header:

```bash
curl -H "X-API-Key: your-api-key-here" \
     https://api.orama.network/v1/status
```

### Using JWT Tokens

```bash
curl -H "Authorization: Bearer your-jwt-token-here" \
     https://api.orama.network/v1/status
```

## Base Endpoints

### Health Check

```http
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "timestamp": "2024-01-20T10:30:00Z"
}
```

### Status

```http
GET /v1/status
```

**Response:**
```json
{
  "version": "0.80.0",
  "uptime": "24h30m15s",
  "services": {
    "rqlite": "healthy",
    "ipfs": "healthy",
    "olric": "healthy"
  }
}
```

### Version

```http
GET /v1/version
```

**Response:**
```json
{
  "version": "0.80.0",
  "commit": "abc123...",
  "built": "2024-01-20T00:00:00Z"
}
```

## Authentication API

### Get Challenge (Wallet Auth)

Generate a nonce for wallet signature.

```http
POST /v1/auth/challenge
Content-Type: application/json

{
  "wallet": "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb",
  "purpose": "login",
  "namespace": "default"
}
```

**Response:**
```json
{
  "wallet": "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb",
  "namespace": "default",
  "nonce": "a1b2c3d4e5f6...",
  "purpose": "login",
  "expires_at": "2024-01-20T10:35:00Z"
}
```

### Verify Signature

Verify wallet signature and issue JWT + API key.

```http
POST /v1/auth/verify
Content-Type: application/json

{
  "wallet": "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb",
  "signature": "0x...",
  "nonce": "a1b2c3d4e5f6...",
  "namespace": "default"
}
```

**Response:**
```json
{
  "jwt_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "refresh_abc123...",
  "api_key": "api_xyz789...",
  "expires_in": 900,
  "namespace": "default"
}
```

### Refresh Token

Refresh an expired JWT token.

```http
POST /v1/auth/refresh
Content-Type: application/json

{
  "refresh_token": "refresh_abc123..."
}
```

**Response:**
```json
{
  "jwt_token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_in": 900
}
```

### Logout

Revoke refresh tokens.

```http
POST /v1/auth/logout
Authorization: Bearer your-jwt-token

{
  "all": false
}
```

**Response:**
```json
{
  "message": "logged out successfully"
}
```

### Whoami

Get current authentication info.

```http
GET /v1/auth/whoami
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "authenticated": true,
  "method": "api_key",
  "api_key": "api_xyz789...",
  "namespace": "default"
}
```

## Storage API (IPFS)

### Upload File

```http
POST /v1/storage/upload
Authorization: Bearer your-api-key
Content-Type: multipart/form-data

file: <binary data>
```

Or with JSON:

```http
POST /v1/storage/upload
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "data": "base64-encoded-data",
  "filename": "document.pdf",
  "pin": true,
  "encrypt": false
}
```

**Response:**
```json
{
  "cid": "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
  "size": 1024,
  "filename": "document.pdf"
}
```

### Get File

```http
GET /v1/storage/get/:cid
Authorization: Bearer your-api-key
```

**Response:** Binary file data or JSON (if `Accept: application/json`)

### Pin File

```http
POST /v1/storage/pin
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "cid": "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
  "replication_factor": 3
}
```

**Response:**
```json
{
  "cid": "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
  "status": "pinned"
}
```

### Unpin File

```http
DELETE /v1/storage/unpin/:cid
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "message": "unpinned successfully"
}
```

### Get Pin Status

```http
GET /v1/storage/status/:cid
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "cid": "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
  "status": "pinned",
  "replicas": 3,
  "peers": ["12D3KooW...", "12D3KooW..."]
}
```

## Cache API (Olric)

### Set Value

```http
PUT /v1/cache/put
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "key": "user:123",
  "value": {"name": "Alice", "email": "alice@example.com"},
  "ttl": 300
}
```

**Response:**
```json
{
  "message": "value set successfully"
}
```

### Get Value

```http
GET /v1/cache/get?key=user:123
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "key": "user:123",
  "value": {"name": "Alice", "email": "alice@example.com"}
}
```

### Get Multiple Values

```http
POST /v1/cache/mget
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "keys": ["user:1", "user:2", "user:3"]
}
```

**Response:**
```json
{
  "results": {
    "user:1": {"name": "Alice"},
    "user:2": {"name": "Bob"},
    "user:3": null
  }
}
```

### Delete Value

```http
DELETE /v1/cache/delete?key=user:123
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "message": "deleted successfully"
}
```

### Scan Keys

```http
GET /v1/cache/scan?pattern=user:*&limit=100
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "keys": ["user:1", "user:2", "user:3"],
  "count": 3
}
```

## Database API (RQLite)

### Execute SQL

```http
POST /v1/rqlite/exec
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "sql": "INSERT INTO users (name, email) VALUES (?, ?)",
  "args": ["Alice", "alice@example.com"]
}
```

**Response:**
```json
{
  "last_insert_id": 123,
  "rows_affected": 1
}
```

### Query SQL

```http
POST /v1/rqlite/query
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "sql": "SELECT * FROM users WHERE id = ?",
  "args": [123]
}
```

**Response:**
```json
{
  "columns": ["id", "name", "email"],
  "rows": [
    [123, "Alice", "alice@example.com"]
  ]
}
```

### Get Schema

```http
GET /v1/rqlite/schema
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "tables": [
    {
      "name": "users",
      "schema": "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"
    }
  ]
}
```

## Pub/Sub API

### Publish Message

```http
POST /v1/pubsub/publish
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "topic": "chat",
  "data": "SGVsbG8sIFdvcmxkIQ==",
  "namespace": "default"
}
```

**Response:**
```json
{
  "message": "published successfully"
}
```

### List Topics

```http
GET /v1/pubsub/topics
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "topics": ["chat", "notifications", "events"]
}
```

### Subscribe (WebSocket)

```http
GET /v1/pubsub/ws?topic=chat
Authorization: Bearer your-api-key
Upgrade: websocket
```

**WebSocket Messages:**

Incoming (from server):
```json
{
  "type": "message",
  "topic": "chat",
  "data": "SGVsbG8sIFdvcmxkIQ==",
  "timestamp": "2024-01-20T10:30:00Z"
}
```

Outgoing (to server):
```json
{
  "type": "publish",
  "topic": "chat",
  "data": "SGVsbG8sIFdvcmxkIQ=="
}
```

### Presence

```http
GET /v1/pubsub/presence?topic=chat
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "topic": "chat",
  "members": [
    {"id": "user-123", "joined_at": "2024-01-20T10:00:00Z"},
    {"id": "user-456", "joined_at": "2024-01-20T10:15:00Z"}
  ]
}
```

## Serverless API (WASM)

### Deploy Function

```http
POST /v1/functions
Authorization: Bearer your-api-key
Content-Type: multipart/form-data

name: hello-world
namespace: default
description: Hello world function
wasm: <binary WASM file>
memory_limit: 64
timeout: 30
```

**Response:**
```json
{
  "id": "fn_abc123",
  "name": "hello-world",
  "namespace": "default",
  "wasm_cid": "QmXxx...",
  "version": 1,
  "created_at": "2024-01-20T10:30:00Z"
}
```

### Invoke Function

```http
POST /v1/functions/hello-world/invoke
Authorization: Bearer your-api-key
Content-Type: application/json

{
  "name": "Alice"
}
```

**Response:**
```json
{
  "result": "Hello, Alice!",
  "execution_time_ms": 15,
  "memory_used_mb": 2.5
}
```

### List Functions

```http
GET /v1/functions?namespace=default
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "functions": [
    {
      "name": "hello-world",
      "description": "Hello world function",
      "version": 1,
      "created_at": "2024-01-20T10:30:00Z"
    }
  ]
}
```

### Delete Function

```http
DELETE /v1/functions/hello-world?namespace=default
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "message": "function deleted successfully"
}
```

### Get Function Logs

```http
GET /v1/functions/hello-world/logs?limit=100
Authorization: Bearer your-api-key
```

**Response:**
```json
{
  "logs": [
    {
      "timestamp": "2024-01-20T10:30:00Z",
      "level": "info",
      "message": "Function invoked",
      "invocation_id": "inv_xyz789"
    }
  ]
}
```

## Error Responses

All errors follow a consistent format:

```json
{
  "code": "NOT_FOUND",
  "message": "user with ID '123' not found",
  "details": {
    "resource": "user",
    "id": "123"
  },
  "trace_id": "trace-abc123"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Invalid input |
| `UNAUTHORIZED` | 401 | Authentication required |
| `FORBIDDEN` | 403 | Permission denied |
| `NOT_FOUND` | 404 | Resource not found |
| `CONFLICT` | 409 | Resource already exists |
| `TIMEOUT` | 408 | Operation timeout |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many requests |
| `SERVICE_UNAVAILABLE` | 503 | Service unavailable |
| `INTERNAL` | 500 | Internal server error |

## Rate Limiting

The API implements rate limiting per API key:

- **Default:** 100 requests per minute
- **Burst:** 200 requests

Rate limit headers:
```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1611144000
```

When rate limited:
```json
{
  "code": "RATE_LIMIT_EXCEEDED",
  "message": "rate limit exceeded",
  "details": {
    "limit": 100,
    "retry_after": 60
  }
}
```

## Pagination

List endpoints support pagination:

```http
GET /v1/functions?limit=10&offset=20
```

Response includes pagination metadata:
```json
{
  "data": [...],
  "pagination": {
    "total": 100,
    "limit": 10,
    "offset": 20,
    "has_more": true
  }
}
```

## Webhooks (Future)

Coming soon: webhook support for event notifications.

## Support

- API Issues: https://github.com/DeBrosOfficial/network/issues
- OpenAPI Spec: `openapi/gateway.yaml`
- SDK Documentation: `docs/CLIENT_SDK.md`
