# Serverless Functions

Orama Network runs serverless functions as sandboxed WebAssembly (WASM) modules. Functions are written in Go, compiled to WASM with TinyGo, and executed in an isolated wazero runtime with configurable memory limits and timeouts.

Functions receive input via **stdin** (JSON) and return output via **stdout** (JSON). They can also access Orama services — database, cache, storage, secrets, PubSub, and HTTP — through **host functions** injected by the runtime.

## Quick Start

```bash
# 1. Scaffold a new function
orama function init my-function

# 2. Edit your handler
cd my-function
# edit function.go

# 3. Build to WASM
orama function build

# 4. Deploy
orama function deploy

# 5. Invoke
orama function invoke my-function --data '{"name": "World"}'

# 6. View logs
orama function logs my-function
```

## Project Structure

```
my-function/
├── function.go      # Handler code
└── function.yaml    # Configuration
```

### function.yaml

```yaml
name: my-function       # Required. Letters, digits, hyphens, underscores.
public: false           # Allow unauthenticated invocation (default: false)
memory: 64              # Memory limit in MB (1-256, default: 64)
timeout: 30             # Execution timeout in seconds (1-300, default: 30)
                        # Bump to 60-300 for batch DB ops, schema migrations,
                        # or anything that does many sequential host calls.
                        # Exceeding this timeout on a direct invoke returns
                        # HTTP 429 {ok:false, error:{code:"RATE_LIMITED",...}}
                        # (retryable). The code "TIMEOUT" (HTTP 504) appears
                        # only when the namespace-proxy budget is exceeded.
retry:
  count: 0              # Retry attempts on failure (default: 0)
  delay: 5              # Seconds between retries (default: 5)
env:                    # Environment variables (accessible via get_env)
  MY_VAR: "value"
```

### function.go (minimal)

```go
package main

import (
    "encoding/json"
    "os"
)

func main() {
    // Read JSON input from stdin
    var input []byte
    buf := make([]byte, 4096)
    for {
        n, err := os.Stdin.Read(buf)
        if n > 0 {
            input = append(input, buf[:n]...)
        }
        if err != nil {
            break
        }
    }

    var payload map[string]interface{}
    json.Unmarshal(input, &payload)

    // Process and return JSON output via stdout
    response := map[string]interface{}{
        "result": "Hello!",
    }
    output, _ := json.Marshal(response)
    os.Stdout.Write(output)
}
```

### Building

Functions are compiled to WASM using [TinyGo](https://tinygo.org/):

```bash
# Using the CLI (recommended)
orama function build

# Or manually
tinygo build -o function.wasm -target wasi function.go
```

> ⚠️ **`orama function deploy` reuses an existing `function.wasm`.** It only
> auto-builds when `function.wasm` is **absent**. If you edit `function.go` but a
> stale `function.wasm` from a previous build is still on disk, `deploy` uploads the
> **old** binary and your changes silently never ship — which looks exactly like "the
> gateway isn't picking up my update". After editing source, run `orama function
> build` (which overwrites the wasm) or `rm function.wasm` before `orama function
> deploy`. Redeploys otherwise take effect immediately (new version → new WASM CID →
> the runtime loads it on the next invoke).

> ℹ️ **Deploys are per-gateway.** `orama function deploy` targets the gateway of your
> **active CLI environment** (`orama env`). To deploy into a namespace, point the CLI
> at that namespace's gateway (`orama env add <name> https://ns-<ns>.<domain>` then
> `orama env use <name>`), or set `ORAMA_GATEWAY_URL`. Verify against the same
> namespace host — the bare/main gateway has a different function registry + DB view.

## Host Functions API

Host functions let your WASM code interact with Orama services. They use a pointer/length ABI for string parameters and are registered at runtime under three module-name aliases — all three resolve to the SAME function table:

| Module name | Status | Use |
|---|---|---|
| `env` | **canonical** | Recommended for new code. Matches the WASI / TinyGo convention used by every example in this doc and the `sdk/fn` package. |
| `host` | alias (kept) | Long-standing alternative; supported indefinitely. |
| `orama` | alias (kept) | Brand-name alias; supported indefinitely so existing code that intuited this name keeps working. |

A function may import any host call from any of the three names interchangeably:

```go
//go:wasmimport env   db_query     // canonical (preferred)
//go:wasmimport host  db_query     // identical
//go:wasmimport orama db_query     // identical
```

If you see the runtime error `failed to instantiate module: module[X] not instantiated`, your function imported from a name other than the three above — fix the directive. Most functions written using the [`sdk/fn`](../sdk/fn) package don't need any `//go:wasmimport` directives at all (the SDK uses stdin/stdout for I/O).

### Context

| Function | Description |
|----------|-------------|
| `get_caller_wallet()` → string | Resolved caller wallet (JWT subject if Bearer auth, else namespace pseudo-id when API-key auth). |
| `get_caller_jwt_subject()` → string | JWT `sub` claim explicitly. Empty when the request was not JWT-authenticated. Use this when binding on the JWT-signed identity matters (e.g. signup flows verifying the caller signed for the wallet they're registering). |
| `get_caller_claim(name)` → string | Custom JWT claim by name (tier, subscription, etc.). Empty if missing or non-JWT request. |
| `get_request_id()` → string | Unique invocation ID |
| `get_env(key)` → string | Environment variable from function.yaml |
| `get_secret(name)` → string | Decrypted secret value (see [Managing Secrets](#managing-secrets)) |

### Database (RQLite)

| Function | Description |
|----------|-------------|
| `db_query_v2(sql, argsJSON)` → JSON | **Recommended.** Execute SELECT. Returns `{"rows": [...], "error": "..."}` — distinguishes empty result from query failure. |
| `db_execute_v2(sql, argsJSON)` → JSON | **Recommended.** Execute INSERT/UPDATE/DELETE. Returns `{"rows_affected": N, "last_insert_id": M, "error": "..."}` — distinguishes 0-rows-affected from a real failure. |
| `db_query(sql, argsJSON)` → JSON | Legacy. Execute SELECT, returns JSON array of rows. No way to surface query errors — prefer `db_query_v2`. |
| `db_execute(sql, argsJSON)` → int | Legacy. Returns affected rows ONLY. **Returns 0 for both "0 rows" and "SQL error" — caller can't distinguish.** Prefer `db_execute_v2`. |
| `db_transaction(opsJSON)` → JSON | Atomic batch — see "Database Transactions" below. |

Example v2 usage from WASM:

```go
//go:wasmimport env db_execute_v2
func dbExecuteV2(sqlPtr, sqlLen, argsPtr, argsLen uint32) uint64

resultBytes := callDBExecuteV2(`INSERT INTO event_seq (topic, next_seq) VALUES (?, 0)
                                ON CONFLICT(topic) DO NOTHING`,
                                []any{"user/abc/account"})

var res struct {
    RowsAffected int64  `json:"rows_affected"`
    Error        string `json:"error"`
}
json.Unmarshal(resultBytes, &res)
if res.Error != "" {
    // Real failure — bail out, don't mark migration applied.
    return fmt.Errorf("event_seq INSERT failed: %s", res.Error)
}
// res.RowsAffected may legitimately be 0 (ON CONFLICT DO NOTHING) — that's not an error.
```

The legacy `db_execute` is kept indefinitely so existing functions don't break. New code should use `db_execute_v2` for any path where distinguishing "no rows" from "SQL error" matters — most paths.

### Cache (Olric Distributed Cache)

| Function | Description |
|----------|-------------|
| `cache_get(key)` → bytes | Get cached value by key. Returns empty on miss. |
| `cache_set(key, value, ttl)` | Store value with TTL in seconds. |
| `cache_incr(key)` → int64 | Atomically increment by 1 (init to 0 if missing). |
| `cache_incr_by(key, delta)` → int64 | Atomically increment by delta. |

### HTTP

| Function | Description |
|----------|-------------|
| `http_fetch(method, url, headersJSON, body)` → JSON | Make outbound HTTP request. Headers as JSON object. Returns `{"status": 200, "headers": {...}, "body": "..."}`. Timeout: 30s. |

### Storage (IPFS)

There is **no `storage_put` / `storage_get` host function exposed to WASM** — a
function cannot pin to or read IPFS directly. Storage is done over the gateway HTTP
API (`POST /v1/storage/upload`, `GET /v1/storage/get/:cid`).

> ⚠️ Those storage endpoints require a **logged-in user (JWT)** — an API key alone is
> rejected (`the 'storage' operation requires a logged-in user; an API key alone is
> not sufficient`). So a function cannot upload via `http_fetch` with an API-key
> secret either. The pattern that works: the **client** uploads with its user JWT and
> passes the resulting CID to a function (which records/uses it). If your function
> truly needs to originate IPFS content, it needs a user-JWT credential, not a
> namespace API key.

### PubSub

| Function | Description |
|----------|-------------|
| `pubsub_publish(topic, dataJSON)` → bool | Publish message to a PubSub topic. Returns true on success. |

### Ephemeral State (WS-subscribe-tracked)

Short-lived per-subscriber state (typing indicators, presence, call ringing,
live cursors) that the gateway **auto-clears the moment the owning WebSocket
client disconnects** — no heartbeats, no prune crons. State also expires on a
TTL backstop (default 60 s, max 30 min). The owning client ID and namespace
come from the server-trusted invocation context; functions cannot spoof them.

| Function | Description |
|----------|-------------|
| `ephemeral_state_set(topic, key, payload, ttlMs)` → u32 | Record state owned by the CURRENT invocation's WS client and publish an `ephemeral.set` event on the topic. 1 = ok, 0 = failure (no WS client, empty topic/key, payload > 16 KiB, > 256 keys/client). |
| `ephemeral_state_clear(topic, key)` → u32 | Clear state this client owns; publishes `ephemeral.clear` (reason `explicit`). Idempotent — clearing a missing/non-owned key returns 1. |
| `ephemeral_state_list(topic)` → u64 | Reconnect catch-up read: packed `ptr<<32\|len` of a JSON envelope with the live entries on the topic. Works without a WS client (read-only). 0 on failure. |

Raw import signatures (pointer/length ABI — note `ttlMs` is **i64**):

```go
//go:wasmimport env ephemeral_state_set
func ephemeralStateSet(topicPtr *byte, topicLen uint32, keyPtr *byte, keyLen uint32,
	payloadPtr *byte, payloadLen uint32, ttlMs int64) uint32

//go:wasmimport env ephemeral_state_clear
func ephemeralStateClear(topicPtr *byte, topicLen uint32, keyPtr *byte, keyLen uint32) uint32

//go:wasmimport env ephemeral_state_list
func ephemeralStateList(topicPtr *byte, topicLen uint32) uint64 // ptr<<32|len of JSON
```

Synthetic events are published **on the same topic** the state lives on, with
the `_orama` control-frame discriminator (same dispatch pattern as the
`auth.refresh` frame). Subscribers update their local view from the stream:

```json
{"_orama":"ephemeral.set",  "topic":"typing:room1", "key":"user-7", "client_id":"ws-abc", "payload":"<base64>"}
{"_orama":"ephemeral.clear","topic":"typing:room1", "key":"user-7", "client_id":"ws-abc", "reason":"disconnect"}
```

`reason` is `explicit` (function called clear), `disconnect` (owning WS client
went away — the zero-lag path), or `expired` (TTL backstop). `payload` is
base64 (Go `[]byte` JSON encoding) and present only on `ephemeral.set`.

`ephemeral_state_list` returns:

```json
{"entries":[{"key":"user-7","client_id":"ws-abc","payload":"<base64>","expires_in_ms":48211}]}
```

Typing-indicator shape (called from a `ws_persistent` rpc-router function):

```go
// Client sends {"op":"typing.start","room":"room1","user":"user-7"} → handler:
ephemeralStateSet(ptr("typing:"+room), len32("typing:"+room),
	ptr(userID), len32(userID), nil, 0, 30_000) // 30s TTL backstop

// Client sends typing.stop → handler:
ephemeralStateClear(ptr("typing:"+room), len32("typing:"+room), ptr(userID), len32(userID))

// No typing.stop needed on app kill / network drop: the WS disconnect publishes
// {"_orama":"ephemeral.clear",...,"reason":"disconnect"} to every subscriber
// immediately. On (re)connect, call ephemeral_state_list("typing:"+room) once
// to seed local state, then track the event stream.
```

### Logging

| Function | Description |
|----------|-------------|
| `log_info(message)` | Log info-level message (captured in invocation logs). |
| `log_error(message)` | Log error-level message. |

## Configuring Push Notifications (per-namespace)

Push providers (ntfy / Expo) are configured **per namespace** by the tenant —
no operator involvement, no SSH access required. Set, read, or clear via:

```bash
# Set / update (sensitive credentials are encrypted at rest)
curl -X PUT https://ns-myapp.example.com/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>' \
  -H 'Content-Type: application/json' \
  -d '{
    "ntfy_base_url":   "https://ntfy.sh",
    "ntfy_auth_token": "tk_…"
  }'

# Read (sensitive fields redacted to booleans)
curl https://ns-myapp.example.com/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'

# Clear (push reverts to gateway-wide defaults if any, else 503)
curl -X DELETE https://ns-myapp.example.com/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'
```

### Field semantics

| Field | Sensitive? | Notes |
|---|---|---|
| `ntfy_base_url` | No | URL of the ntfy server. `https://ntfy.sh` works for testing. |
| `ntfy_auth_token` | Yes | Optional bearer token sent to ntfy. Encrypted at rest. |
| `expo_access_token` | Yes | Expo Push API access token. Encrypted at rest. |

PUT semantics are **field-level** — a `null` (or omitted) field leaves the
existing value alone; an explicit empty string clears just that field. To
clear EVERYTHING use DELETE.

After a PUT the next `push_send` (host call) or `POST /v1/push/send` uses
the new providers — the cached dispatcher is invalidated automatically.

If no per-namespace config is set AND the gateway has no YAML defaults, the
push endpoints return **503 SERVICE_UNAVAILABLE** with a message naming the
exact config to set.

## Managing Secrets

Secrets are encrypted at rest (AES-256-GCM) and scoped to your namespace. Functions read them via `get_secret("name")` at runtime.

### CLI Commands

```bash
# Set a secret (inline value)
orama function secrets set APNS_KEY_ID "ABC123DEF"

# Set a secret from a file (useful for PEM keys, certificates)
orama function secrets set APNS_AUTH_KEY --from-file ./AuthKey_ABC123.p8

# List all secret names (values are never shown)
orama function secrets list

# Delete a secret
orama function secrets delete APNS_KEY_ID

# Delete without confirmation
orama function secrets delete APNS_KEY_ID --force
```

### How It Works

1. **You set secrets** via the CLI → encrypted and stored in the database
2. **Functions read secrets** at runtime via `get_secret("name")` → decrypted on demand
3. **Namespace isolation** → each namespace has its own secret store; functions in namespace A cannot read secrets from namespace B

## PubSub Triggers

Triggers let functions react to events automatically. When a message is published to a PubSub topic, all functions with a trigger on that topic are invoked asynchronously.

### CLI Commands

```bash
# Add a trigger: invoke "call-push-handler" when messages hit "calls:invite"
orama function triggers add call-push-handler --topic calls:invite

# List triggers for a function
orama function triggers list call-push-handler

# Delete a trigger
orama function triggers delete call-push-handler <trigger-id>
```

### Trigger Event Payload

When triggered via PubSub, the function receives this JSON via stdin:

```json
{
  "topic": "calls:invite",
  "data": { ... },
  "namespace": "my-namespace",
  "trigger_depth": 1,
  "timestamp": 1708972800
}
```

### Depth Limiting

To prevent infinite loops (function A publishes to topic → triggers function A again), trigger depth is tracked. Maximum depth is **5**. If a function's output triggers another function, `trigger_depth` increments. At depth 5, no further triggers fire.

## Function Lifecycle

### Versioning

Each deploy creates a new version. The WASM binary is stored in **IPFS** (content-addressed) and metadata is stored in **RQLite**.

```bash
# List versions
orama function versions my-function

# Invoke a specific version
curl -X POST .../v1/functions/my-function@2/invoke
```

### Invocation Logging

Every invocation is logged with: request ID, duration, status (success/error/timeout), input/output size, and any `log_info`/`log_error` messages.

```bash
orama function logs my-function
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `orama function init <name>` | Scaffold a new function project |
| `orama function build [dir]` | Compile Go to WASM |
| `orama function deploy [dir]` | Deploy WASM to the network |
| `orama function invoke <name> --data <json>` | Invoke a function |
| `orama function list` | List deployed functions |
| `orama function get <name>` | Get function details |
| `orama function delete <name>` | Delete a function |
| `orama function logs <name>` | View invocation logs |
| `orama function versions <name>` | List function versions |
| `orama function secrets set <name> <value>` | Set an encrypted secret |
| `orama function secrets list` | List secret names |
| `orama function secrets delete <name>` | Delete a secret |
| `orama function triggers add <fn> --topic <t>` | Add PubSub trigger |
| `orama function triggers list <fn>` | List triggers |
| `orama function triggers delete <fn> <id>` | Delete a trigger |

## HTTP API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/v1/functions` | Deploy function (multipart/form-data) |
| GET | `/v1/functions` | List functions |
| GET | `/v1/functions/{name}` | Get function info |
| DELETE | `/v1/functions/{name}` | Delete function |
| POST | `/v1/functions/{name}/invoke` | Invoke function |
| GET | `/v1/functions/{name}/versions` | List versions |
| GET | `/v1/functions/{name}/logs` | Get logs |
| WS | `/v1/functions/{name}/ws` | WebSocket invoke (streaming) |
| PUT | `/v1/functions/secrets` | Set a secret |
| GET | `/v1/functions/secrets` | List secret names |
| DELETE | `/v1/functions/secrets/{name}` | Delete a secret |
| POST | `/v1/functions/{name}/triggers` | Add PubSub trigger |
| GET | `/v1/functions/{name}/triggers` | List triggers |
| DELETE | `/v1/functions/{name}/triggers/{id}` | Delete trigger |
| POST | `/v1/invoke/{namespace}/{name}` | Direct invoke (alt endpoint) |

## Example: Call Push Handler

A real-world function that sends VoIP push notifications when a call invite is published to PubSub:

```yaml
# function.yaml
name: call-push-handler
memory: 128
timeout: 30
```

```go
// function.go — triggered by PubSub on "calls:invite"
package main

import (
    "encoding/json"
    "os"
)

// This function:
// 1. Receives a call invite event from PubSub trigger
// 2. Queries the database for the callee's device info
// 3. Reads push notification credentials from secrets
// 4. Sends a push notification via http_fetch

func main() {
    // Read PubSub trigger event from stdin
    var input []byte
    buf := make([]byte, 4096)
    for {
        n, err := os.Stdin.Read(buf)
        if n > 0 {
            input = append(input, buf[:n]...)
        }
        if err != nil {
            break
        }
    }

    // Parse the trigger event wrapper
    var event struct {
        Topic string          `json:"topic"`
        Data  json.RawMessage `json:"data"`
    }
    json.Unmarshal(input, &event)

    // Parse the actual call invite data
    var invite struct {
        CalleeID   string `json:"calleeId"`
        CallerName string `json:"callerName"`
        CallType   string `json:"callType"`
    }
    json.Unmarshal(event.Data, &invite)

    // At this point, the function would use host functions:
    //
    // 1. db_query("SELECT push_token, device_type FROM devices WHERE user_id = ?",
    //             json.Marshal([]string{invite.CalleeID}))
    //
    // 2. get_secret("FCM_SERVER_KEY") for Android push
    //    get_secret("APNS_KEY_PEM") for iOS push
    //
    // 3. http_fetch("POST", "https://fcm.googleapis.com/v1/...", headers, body)
    //
    // 4. log_info("Push sent to " + invite.CalleeID)
    //
    // Note: Host functions use the WASM ABI (pointer/length).
    // The sdk/fn package wraps them for ergonomic access.

    response := map[string]interface{}{
        "status":  "sent",
        "callee":  invite.CalleeID,
    }
    output, _ := json.Marshal(response)
    os.Stdout.Write(output)
}
```

Deploy and wire the trigger:
```bash
orama function build
orama function deploy

# Set push notification secrets
orama function secrets set FCM_SERVER_KEY "your-fcm-key"
orama function secrets set APNS_KEY_PEM --from-file ./AuthKey.p8
orama function secrets set APNS_KEY_ID "ABC123"
orama function secrets set APNS_TEAM_ID "TEAM456"

# Wire the PubSub trigger
orama function triggers add call-push-handler --topic calls:invite
```
