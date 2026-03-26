# Orama Vault -- API Reference

## Base URL

All endpoints are prefixed with `/v1/vault/` (V1) or `/v2/vault/` (V2). The guardian listens on the configured client port (default: **7500**).

```
http://<guardian-ip>:7500/v1/vault/...
http://<guardian-ip>:7500/v2/vault/...
```

In production, the Orama gateway reverse-proxies these endpoints over TLS (port 443). Direct access to port 7500 is only available within the WireGuard overlay network.

> **Note:** TLS termination is not yet implemented in the guardian itself (Phase 3). Currently plain TCP.

---

## V1 Endpoints

### GET /v1/vault/health

Liveness check. Returns immediately with a static response. No authentication required. Used by load balancers and monitoring.

**Request:**
```
GET /v1/vault/health HTTP/1.1
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "version": "0.1.0"
}
```

This endpoint never returns an error. If the process is running and the TCP listener is accepting connections, it returns 200.

---

### GET /v1/vault/status

Guardian status information. Returns configuration and runtime state. No authentication required.

**Request:**
```
GET /v1/vault/status HTTP/1.1
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "version": "0.1.0",
  "data_dir": "/opt/orama/.orama/data/vault",
  "client_port": 7500,
  "peer_port": 7501
}
```

---

### GET /v1/vault/guardians

List known guardian nodes. In the current MVP, returns only the local node. Phase 3 will query RQLite for the full cluster list.

**Request:**
```
GET /v1/vault/guardians HTTP/1.1
```

**Response (200 OK):**
```json
{
  "guardians": [
    {
      "address": "0.0.0.0",
      "port": 7500
    }
  ],
  "threshold": 3,
  "total": 1
}
```

| Field | Type | Description |
|-------|------|-------------|
| `guardians` | array | List of known guardian nodes |
| `guardians[].address` | string | Node IP address |
| `guardians[].port` | number | Node client port |
| `threshold` | number | Shamir threshold K (minimum shares to reconstruct) |
| `total` | number | Total known guardians |

---

### POST /v1/vault/push

Store an encrypted share for a user. The client has already performed the Shamir split locally and sends one share to each guardian.

**Request:**
```
POST /v1/vault/push HTTP/1.1
Content-Type: application/json
Content-Length: <length>

{
  "identity": "<64 hex chars>",
  "share": "<base64-encoded share data>",
  "version": <uint64>
}
```

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `identity` | string | yes | Exactly 64 lowercase hex characters (SHA-256 of user identity) |
| `share` | string | yes | Base64-encoded encrypted share data. Decoded size must be > 0 and <= 512 KiB |
| `version` | number | yes | Unsigned 64-bit integer. Must be strictly greater than the currently stored version (monotonic counter) |

**Success Response (200 OK):**
```json
{
  "status": "stored"
}
```

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 400 | `{"error":"empty body"}` | Request body is empty |
| 400 | `{"error":"request body too large"}` | Body exceeds 1 MiB |
| 400 | `{"error":"missing identity field"}` | `identity` field not found in JSON |
| 400 | `{"error":"missing share field"}` | `share` field not found in JSON |
| 400 | `{"error":"identity must be exactly 64 hex characters"}` | Identity is not 64 chars |
| 400 | `{"error":"identity must be hex"}` | Identity contains non-hex characters |
| 400 | `{"error":"invalid base64 in share"}` | Share data is not valid base64 |
| 400 | `{"error":"share data too large"}` | Decoded share exceeds 512 KiB |
| 400 | `{"error":"share data is empty"}` | Decoded share is 0 bytes |
| 400 | `{"error":"missing or invalid version field"}` | `version` field missing or not a valid unsigned integer |
| 400 | `{"error":"version must be greater than current stored version"}` | Anti-rollback: version <= stored version |
| 405 | `{"error":"method not allowed"}` | Non-POST method used |
| 500 | `{"error":"internal server error"}` | Disk write failure or allocation error |

**Storage Behavior:**

1. Share data is written atomically: first to `share.bin.tmp`, then renamed to `share.bin`.
2. Version counter is written atomically: first to `version.tmp`, then renamed to `version`.
3. Anti-rollback: if a version file exists for this identity, the new version must be strictly greater. Equal versions are also rejected.
4. Directory path: `<data_dir>/shares/<identity>/share.bin`

**Size Limits:**

| Limit | Value |
|-------|-------|
| Max request body | 1 MiB (1,048,576 bytes) |
| Max decoded share | 512 KiB (524,288 bytes) |
| Identity length | Exactly 64 hex characters |
| Max version value | 2^64 - 1 |

---

### POST /v1/vault/pull

Retrieve an encrypted share for a user. The client contacts multiple guardians to collect K shares for reconstruction.

**Request:**
```
POST /v1/vault/pull HTTP/1.1
Content-Type: application/json
Content-Length: <length>

{
  "identity": "<64 hex chars>"
}
```

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `identity` | string | yes | Exactly 64 lowercase hex characters |

**Success Response (200 OK):**
```json
{
  "share": "<base64-encoded share data>"
}
```

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 400 | `{"error":"empty body"}` | Request body is empty |
| 400 | `{"error":"request body too large"}` | Body exceeds 4 KiB |
| 400 | `{"error":"missing identity field"}` | `identity` not found |
| 400 | `{"error":"identity must be exactly 64 hex characters"}` | Wrong length |
| 400 | `{"error":"identity must be hex"}` | Non-hex characters |
| 404 | `{"error":"share not found"}` | No share stored for this identity |
| 405 | `{"error":"method not allowed"}` | Non-POST method used |
| 500 | `{"error":"internal server error"}` | Disk read failure |

**Size Limits:**

| Limit | Value |
|-------|-------|
| Max request body | 4 KiB (4,096 bytes) |
| Max share read | 1 MiB (1,048,576 bytes) |

---

### POST /v1/vault/auth/challenge

Request a challenge nonce to begin authentication.

**Request:**
```
POST /v1/vault/auth/challenge HTTP/1.1
Content-Type: application/json

{
  "identity": "<64 hex chars>"
}
```

**Response (200 OK):**
```json
{
  "nonce": "<base64 32 bytes>",
  "created_ns": <i128>,
  "tag": "<base64 32 bytes>",
  "expires_in_seconds": 60
}
```

The client must return this exact challenge (nonce + created_ns + tag) along with their identity within 60 seconds.

---

### POST /v1/vault/auth/session

Exchange a verified challenge for a session token.

**Request:**
```
POST /v1/vault/auth/session HTTP/1.1
Content-Type: application/json

{
  "identity": "<64 hex chars>",
  "nonce": "<base64 32 bytes>",
  "created_ns": <i128>,
  "tag": "<base64 32 bytes>"
}
```

**Response (200 OK):**
```json
{
  "session_token": "<base64-encoded token>",
  "expires_in_seconds": 3600
}
```

The session token is valid for 1 hour. It should be included in subsequent requests as a Bearer token in the Authorization header.

---

## V1 Authentication Flow

The authentication flow is challenge-response:

```
Client                              Guardian
  |                                    |
  |  POST /v1/vault/auth/challenge     |
  |  {"identity":"<hex>"}              |
  |----------------------------------->|
  |                                    | Generate 32-byte random nonce
  |                                    | HMAC(server_secret, identity || nonce || timestamp)
  |  {"nonce":"..","tag":".."}         |
  |<-----------------------------------|
  |                                    |
  |  POST /v1/vault/auth/session       |
  |  {"identity":"..","nonce":"..","tag":".."}
  |----------------------------------->|
  |                                    | Verify HMAC tag
  |                                    | Check nonce not expired (60s)
  |  {"session_token":".."}            | Issue HMAC-based session token (1h)
  |<-----------------------------------|
  |                                    |
  |  POST /v1/vault/push               |
  |  Authorization: Bearer <token>     |
  |----------------------------------->|
  |                                    | Verify session token
  |                                    | Process request
```

Key properties:
- Challenge expires in 60 seconds.
- Session tokens expire in 1 hour.
- All HMAC verifications use constant-time comparison to prevent timing attacks.
- Server secret is generated randomly at startup (not persisted -- sessions invalidate on restart).
- Phase 3 adds Ed25519 signature verification for true public-key authentication.

---

## V2 Endpoints

V2 introduces a generic secrets API. Instead of storing a single anonymous share per identity, V2 allows multiple named secrets per identity with full CRUD operations.

All V2 secrets endpoints require mandatory session authentication via the `X-Session-Token` header. The identity is extracted from the session token -- it is never passed in the request body. Authenticate first using the V2 auth endpoints below.

### POST /v2/vault/auth/challenge

Request a challenge nonce to begin authentication. Same protocol as V1 auth/challenge.

**Request:**
```
POST /v2/vault/auth/challenge HTTP/1.1
Content-Type: application/json

{
  "identity": "<64 hex chars>"
}
```

**Response (200 OK):**
```json
{
  "nonce": "<base64 32 bytes>",
  "created_ns": <i128>,
  "tag": "<base64 32 bytes>",
  "expires_in_seconds": 60
}
```

---

### POST /v2/vault/auth/session

Exchange a verified challenge for a session token. Same protocol as V1 auth/session.

**Request:**
```
POST /v2/vault/auth/session HTTP/1.1
Content-Type: application/json

{
  "identity": "<64 hex chars>",
  "nonce": "<base64 32 bytes>",
  "created_ns": <i128>,
  "tag": "<base64 32 bytes>"
}
```

**Response (200 OK):**
```json
{
  "session_token": "<base64-encoded token>",
  "expires_in_seconds": 3600
}
```

The session token is valid for 1 hour. Include it in all subsequent V2 requests as the `X-Session-Token` header.

---

### PUT /v2/vault/secrets/{name}

Store a named secret. Requires session authentication. The identity is extracted from the session token.

**Request:**
```
PUT /v2/vault/secrets/my-api-key HTTP/1.1
Content-Type: application/json
X-Session-Token: <session_token>

{
  "share": "<base64-encoded secret data>",
  "version": <u64>
}
```

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` (URL path) | string | yes | Alphanumeric, `_`, `-`. Max 128 characters |
| `share` | string | yes | Base64-encoded data. Decoded size must be > 0 and <= 512 KiB |
| `version` | number | yes | Unsigned 64-bit integer. Must be strictly greater than the currently stored version (anti-rollback) |

**Success Response (200 OK):**
```json
{
  "status": "stored",
  "name": "my-api-key",
  "version": 1
}
```

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 400 | `{"error":"empty body"}` | Request body is empty |
| 400 | `{"error":"invalid secret name"}` | Name contains disallowed characters or exceeds 128 chars |
| 400 | `{"error":"missing share field"}` | `share` not found in JSON |
| 400 | `{"error":"invalid base64 in share"}` | Share is not valid base64 |
| 400 | `{"error":"share data too large"}` | Decoded share exceeds 512 KiB |
| 400 | `{"error":"share data is empty"}` | Decoded share is 0 bytes |
| 400 | `{"error":"missing or invalid version field"}` | `version` missing or invalid |
| 400 | `{"error":"version must be greater than current stored version"}` | Anti-rollback: version <= stored version |
| 401 | `{"error":"missing session token"}` | `X-Session-Token` header not provided |
| 401 | `{"error":"invalid session token"}` | Token is malformed or expired |
| 409 | `{"error":"too many secrets"}` | Identity has reached the 1000 secret limit |
| 500 | `{"error":"internal server error"}` | Disk write failure |

**Storage Layout:**
```
<data_dir>/vaults/<identity_hex>/<secret_name>/
    share.bin     -- Encrypted share data
    checksum.bin  -- HMAC-SHA256 integrity checksum
    meta.json     -- {"version":1,"created_ns":...,"updated_ns":...,"size":123}
```

**Limits:**

| Limit | Value |
|-------|-------|
| Max secrets per identity | 1000 |
| Max decoded share size | 512 KiB (524,288 bytes) |
| Max secret name length | 128 characters |
| Secret name charset | `[a-zA-Z0-9_-]` |
| Max version value | 2^64 - 1 |

---

### GET /v2/vault/secrets/{name}

Retrieve a named secret. Requires session authentication. The identity is extracted from the session token.

**Request:**
```
GET /v2/vault/secrets/my-api-key HTTP/1.1
X-Session-Token: <session_token>
```

**Success Response (200 OK):**
```json
{
  "share": "<base64-encoded secret data>",
  "name": "my-api-key",
  "version": 1,
  "created_ns": 1700000000000000000,
  "updated_ns": 1700000000000000000
}
```

| Field | Type | Description |
|-------|------|-------------|
| `share` | string | Base64-encoded secret data |
| `name` | string | Secret name |
| `version` | number | Current version |
| `created_ns` | number | Creation timestamp in nanoseconds |
| `updated_ns` | number | Last update timestamp in nanoseconds |

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 401 | `{"error":"missing session token"}` | `X-Session-Token` header not provided |
| 401 | `{"error":"invalid session token"}` | Token is malformed or expired |
| 404 | `{"error":"secret not found"}` | No secret with this name for this identity |
| 500 | `{"error":"internal server error"}` | Disk read failure |

---

### DELETE /v2/vault/secrets/{name}

Delete a named secret. Requires session authentication. The identity is extracted from the session token.

**Request:**
```
DELETE /v2/vault/secrets/my-api-key HTTP/1.1
X-Session-Token: <session_token>
```

**Success Response (200 OK):**
```json
{
  "status": "deleted",
  "name": "my-api-key"
}
```

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 401 | `{"error":"missing session token"}` | `X-Session-Token` header not provided |
| 401 | `{"error":"invalid session token"}` | Token is malformed or expired |
| 404 | `{"error":"secret not found"}` | No secret with this name for this identity |
| 500 | `{"error":"internal server error"}` | Disk delete failure |

---

### GET /v2/vault/secrets

List all secrets for the authenticated identity. Requires session authentication. The identity is extracted from the session token.

**Request:**
```
GET /v2/vault/secrets HTTP/1.1
X-Session-Token: <session_token>
```

**Success Response (200 OK):**
```json
{
  "secrets": [
    {
      "name": "my-api-key",
      "version": 1,
      "size": 256
    },
    {
      "name": "db-password",
      "version": 3,
      "size": 48
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `secrets` | array | List of secret metadata entries |
| `secrets[].name` | string | Secret name |
| `secrets[].version` | number | Current version |
| `secrets[].size` | number | Size of stored data in bytes |

**Error Responses:**

| Status | Body | Condition |
|--------|------|-----------|
| 401 | `{"error":"missing session token"}` | `X-Session-Token` header not provided |
| 401 | `{"error":"invalid session token"}` | Token is malformed or expired |
| 500 | `{"error":"internal server error"}` | Disk read failure |

If no secrets exist for the identity, returns an empty array: `{"secrets":[]}`.

---

## V2 Authentication Flow

The V2 authentication flow is identical to V1 but uses V2 path prefixes and the `X-Session-Token` header:

```
Client                              Guardian
  |                                    |
  |  POST /v2/vault/auth/challenge     |
  |  {"identity":"<hex>"}              |
  |----------------------------------->|
  |                                    | Generate 32-byte random nonce
  |                                    | HMAC(server_secret, identity || nonce || timestamp)
  |  {"nonce":"..","tag":".."}         |
  |<-----------------------------------|
  |                                    |
  |  POST /v2/vault/auth/session       |
  |  {"identity":"..","nonce":"..","tag":".."}
  |----------------------------------->|
  |                                    | Verify HMAC tag
  |                                    | Check nonce not expired (60s)
  |  {"session_token":".."}            | Issue HMAC-based session token (1h)
  |<-----------------------------------|
  |                                    |
  |  PUT /v2/vault/secrets/my-key      |
  |  X-Session-Token: <token>          |
  |  {"share":"..","version":1}        |
  |----------------------------------->|
  |                                    | Verify session token
  |                                    | Extract identity from token
  |  {"status":"stored",...}           | Store secret under identity
  |<-----------------------------------|
```

Key differences from V1:
- Session token is sent via `X-Session-Token` header (not Authorization Bearer).
- Identity is extracted from the session token, not from the request body.
- All V2 secrets endpoints require authentication (mandatory, not optional).

---

## Error Response Format

All error responses use a consistent JSON format:

```json
{
  "error": "<human-readable error message>"
}
```

Standard HTTP status codes:

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Client error (bad request, validation failure) |
| 401 | Authentication required or token invalid |
| 404 | Resource not found (share/secret not found, unknown endpoint) |
| 405 | Method not allowed |
| 409 | Conflict (e.g., too many secrets) |
| 500 | Internal server error |

All responses include `Connection: close` and `Content-Type: application/json` headers.

---

## Rate Limiting (not yet implemented)

> **Status:** Rate limiting is planned for Phase 3. Currently there are no request rate limits.

Planned behavior:
- Per-identity rate limiting on push/pull endpoints.
- Health and status endpoints exempt from rate limiting.
- Rate limit headers in responses (`X-RateLimit-Limit`, `X-RateLimit-Remaining`).

---

## Request Size Limits

| Endpoint | Max Body Size |
|----------|---------------|
| `/v1/vault/push` | 1 MiB |
| `/v1/vault/pull` | 4 KiB |
| `/v2/vault/secrets/{name}` (PUT) | 1 MiB |
| All others | 64 KiB (read buffer size) |

The HTTP listener uses a 64 KiB read buffer. Requests larger than this may be truncated. The push handler and V2 PUT handler have an explicit 1 MiB limit check before processing.

---

## Peer Protocol (Port 7501)

The guardian-to-guardian protocol is a binary TCP protocol, **not** HTTP. It runs on port 7501 and is restricted to the WireGuard overlay network (10.0.0.x).

### Wire Format

```
[version:1 byte][msg_type:1 byte][payload_length:4 bytes big-endian][payload:N bytes]
```

- **version**: Protocol version (currently 1). Messages with wrong version are silently dropped.
- **msg_type**: One of the defined message types.
- **payload_length**: 32-bit big-endian unsigned integer. Maximum 1 MiB.

### Message Types

| Code | Name | Direction | Payload Size |
|------|------|-----------|--------------|
| 0x01 | heartbeat | initiator -> peer | 18 bytes |
| 0x02 | heartbeat_ack | peer -> initiator | 0 bytes |
| 0x03 | verify_request | initiator -> peer | 65 bytes |
| 0x04 | verify_response | peer -> initiator | 98 bytes |
| 0x05 | repair_offer | leader -> all | (Phase 2) |
| 0x06 | repair_accept | peer -> leader | (Phase 2) |

### Heartbeat Payload (18 bytes)

```
[sender_ip:4 bytes][sender_port:2 bytes BE][share_count:4 bytes BE][timestamp:8 bytes BE]
```

### Verify Request Payload (65 bytes)

```
[identity:64 bytes][identity_len:1 byte]
```

### Verify Response Payload (98 bytes)

```
[identity:64 bytes][identity_len:1 byte][has_share:1 byte (0/1)][commitment_root:32 bytes SHA-256]
```