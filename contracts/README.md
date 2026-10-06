# Gateway contracts

The request and response shapes shared by the gateway and the TypeScript SDK.

Nothing used to check that a body the SDK sends is a body a handler parses. The
unit tests on each side were written against that side alone, and the only thing
exercising both was an end-to-end suite that needs a live cluster — so a field
renamed on one side and not the other reached production.

Each fixture is read twice:

- **Go** decodes `request` into the handler's own struct with unknown fields
  rejected, so the gateway must understand every field and the SDK must send no
  field the gateway silently drops. See `core/pkg/contracttest` and the
  `contract_test.go` next to each handler.
- **TypeScript** drives the SDK method named in `call` and asserts the body it
  sends is exactly this JSON, then feeds `response` back and asserts what the
  method returns. See `sdk/tests/unit/contracts.test.ts`.

Neither side can move without the other failing, and neither test needs a
cluster.

## A fixture

```json
{
  "route": "/v1/rqlite/query",
  "method": "POST",
  "sdk": "db.query()",
  "goStruct": "rqlite.queryRequest",
  "call": { "module": "db", "method": "query", "args": ["SELECT 1", []] },
  "request": { "sql": "SELECT 1", "args": [] },
  "response": { "items": [] },
  "returns": []
}
```

| Field | Meaning |
|-------|---------|
| `route` | The gateway path |
| `method` | HTTP method |
| `sdk` | The client method this belongs to, named in failure messages |
| `goStruct` | The handler struct the request decodes into |
| `call` | How the TypeScript test drives the SDK. `null` when no single call produces the request — a session renewal, say |
| `request` | The body the SDK sends |
| `response` | The body the gateway answers with |
| `returns` | What the SDK method resolves to, given that response. `null` when it resolves to nothing |

## Adding one

Add the JSON, add the route to the `switch` in the owning package's
`contract_test.go`, and the TypeScript side picks it up with no change.

## Running

```bash
make -C core test-contracts   # Go half
cd sdk && pnpm test:unit      # TypeScript half
```

`-count=1` is not optional for the Go half: these fixtures live above the Go
module, so the test cache does not treat them as an input and a fixture-only
change would report a stale pass. The Makefile target and the CI step both pass
it.

## Coverage

The fixtures cover the routes `@debros/orama` calls with a JSON body. Routes
that carry no body (`GET /v1/rqlite/schema`), take multipart form data
(`/v1/storage/upload`) or carry an application-defined payload
(`/v1/invoke/{namespace}/{name}`) have no fixed shape to pin.

Which client owns which route is recorded in
[docs/API_SURFACE.md](../docs/API_SURFACE.md).
