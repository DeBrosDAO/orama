# Orama demo

A small app you can put on the network in a few minutes and show: three serverless
functions that use the platform's own services, and a static web page that calls them.

| Function | What it shows | Host functions it calls |
|---|---|---|
| `hello` | The function knows who is calling | `get_caller_wallet`, `log_info` |
| `visits` | An atomic counter, shared by every visitor | `cache_incr_by`, `log_info` |
| `guestbook` | Rows in the namespace's own database | `db_execute_v2`, `db_query_v2`, `get_caller_wallet`, `log_info` |

```
demo/
  functions/hello, visits, guestbook/   function.go + function.yaml each (Go, built with TinyGo)
  host/                                 the functions' view of the runtime: real on the gateway, in-memory in tests
  web/                                  the page: index.html, app.js, style.css, config.js (no build step)
  build.sh                              builds every function to WASM
  test.sh                               runs the tests
```

The functions are public (`public: true` in each `function.yaml`), so the page calls
them without a credential and the demo needs no API key in the browser. A public
function is invoked by naming its namespace: `POST /v1/invoke/<namespace>/<name>`.

## Before you start

You need the `orama` CLI, [TinyGo](https://tinygo.org/getting-started/install/)
(macOS: `brew install tinygo`), and a network to put the demo on.

```bash
orama network list              # the networks this CLI knows
orama network use stagenet      # choose one
orama auth login                # sign in with your RootWallet
orama namespace create demo     # your own namespace: a database, a cache and a gateway
orama auth login --namespace demo
```

Run everything below from this directory.

## 1. Build the functions

```bash
./build.sh
```

That runs `tinygo build -target wasi` in each `functions/<name>/` and checks the output
is a WASM module; `orama function build functions/hello` does the same for one.
`orama function deploy` builds a function that has no `function.wasm` yet, but it never
rebuilds one that has: after editing a function, build it again before you deploy.

## 2. Deploy the functions

```bash
orama function deploy functions/hello
orama function deploy functions/visits
orama function deploy functions/guestbook
orama function list
```

## 3. Call them

From the CLI:

```bash
orama function invoke hello --data '{"name": "Ada"}'
orama function invoke visits --data '{"page": "home"}'
orama function invoke visits --data '{"page": "home", "action": "peek"}'
orama function invoke guestbook --data '{"action": "sign", "name": "Ada", "message": "I was here"}'
orama function invoke guestbook
```

Or over HTTP, the way the page does. A function lives in its namespace's gateway,
`https://ns-<namespace>.<base domain>` (`orama network current` shows the gateway of
the cluster; the namespace gateway is the `ns-demo.` name under the same base domain):

```bash
curl -X POST https://ns-demo.<base domain>/v1/invoke/demo/visits \
  -H 'Content-Type: application/json' -d '{"page": "home"}'
```

What each function takes and answers:

| Function | Input | Answer |
|---|---|---|
| `hello` | `{"name": "Ada"}` (optional) | `{"greeting": "Hello, Ada!", "caller": "anonymous"}` |
| `visits` | `{"page": "home", "action": "hit"\|"peek"}`, both optional | `{"page": "home", "visits": 3}` |
| `guestbook` | `{"action": "list"}`, or `{"action": "sign", "name": ..., "message": ...}` | `{"entries": [...], "signed": {...}}`, newest first, at most 20 |

A request the function refuses (a page name with spaces, an empty message) is answered
with `{"error": "..."}` and status 200: it is an answer, not a failure of the function.
`caller` is `anonymous` for a call with no credential; a signed-in caller's wallet
appears there when you call it with a session or a key.

Each call is in the invocation log: `orama function logs visits`.

## 4. Put the web page up

```bash
orama deploy static ./web --name demo
orama app get demo
```

`orama deploy static` prints the page's URL, a name under the cluster's base domain, and
`orama app get demo` shows it again. Open it. The page asks which gateway and namespace
the functions are on (`https://ns-demo.<base domain>` and `demo`), remembers the answer in
that browser, and then counts your visit, lists the guestbook and lets you say hello.

To skip the question, put them in `web/config.js` before you deploy, then update the
deployment:

```bash
sed -i.bak 's#gateway: ""#gateway: "https://ns-demo.<base domain>"#; s#namespace: ""#namespace: "demo"#' web/config.js
rm web/config.js.bak
orama deploy static ./web --name demo --update
```

The gateway allows a page served from a name under its own base domain to call it, which
is why no other setup is needed. A page on any other domain would be refused by the browser.

## Take it down

```bash
orama function delete hello
orama function delete visits
orama function delete guestbook
orama app delete demo
```

The `guestbook_entries` table stays in the namespace's database, and the `visits:` keys in
its cache; deleting the namespace's data is a separate step (`orama db`, `orama namespace`).

## Develop it

```bash
./test.sh
```

runs the functions' logic under `go test` against an in-memory host (`host.Fake`), and the
page's helpers under Node. `host.New()` is the real runtime when the function is built for
WASI and the in-memory one anywhere else, so the same code is vetted, tested and compiled to
WASM. The real host functions the gateway exports to every function are listed in
`core/pkg/serverless/engine.go` (`registerHostModule`) and described under "Host Functions
API" in the Functions page of the developer docs.
