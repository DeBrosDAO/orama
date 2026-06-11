package hostfunctions

import (
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// HostFunctionsConfig holds configuration for HostFunctions.
type HostFunctionsConfig struct {
	IPFSAPIURL  string
	HTTPTimeout time.Duration

	// TURN configuration — feat-9. Plumbed in from the gateway so the
	// `turn_credentials` host fn can mint per-namespace TURN credentials
	// without a round-trip back through HTTP. Mirrors the HTTP endpoint
	// at /v1/webrtc/turn/credentials. TURNSecret empty → host fn returns
	// a structured "TURN not configured" envelope (no error).
	TURNDomain       string
	TURNSecret       string
	StealthCDNDomain string // optional; non-empty adds turns:<domain>:443 URI
}

// HostFunctions provides the bridge between WASM functions and Orama services.
// It implements the HostServices interface and is injected into the execution context.
type HostFunctions struct {
	db          rqlite.Client
	cacheClient olriclib.Client
	storage     ipfs.IPFSClient
	ipfsAPIURL  string
	pubsub      *pubsub.ClientAdapter
	wsManager   serverless.WebSocketManager
	secrets     serverless.SecretsManager
	httpClient  *http.Client
	// anyoneHTTPClient routes outbound requests through the Anyone SOCKS5
	// proxy (feat-11). nil when Anyone routing is disabled on this
	// gateway — AnyoneFetch returns a typed error in that case rather
	// than falling back to the direct httpClient (no silent privacy
	// regression).
	anyoneHTTPClient *http.Client
	logger           *zap.Logger

	// pushDispatcher (legacy) and pushManager (per-namespace, bug #220
	// follow-up) provide push send-paths. When pushManager is set, PushSend
	// uses it so per-namespace config takes effect; pushDispatcher is the
	// fallback. Both nil = push not configured anywhere on this gateway,
	// PushSend returns nil silently for portability.
	pushDispatcher *push.PushDispatcher
	pushManager    *push.Manager

	// wsBridge may be nil when the gateway doesn't run a bridge. In that
	// case WSPubSubBridge returns an error rather than silently no-oping
	// — bridging is a deliberate request whose absence should be visible.
	wsBridge *wsbridge.Bridge

	// invoker is set after construction (via SetInvoker) to break the
	// engine ↔ host-functions circular dep. nil means FunctionInvoke
	// returns ErrFunctionInvokeNotAvailable.
	invoker     serverless.FunctionInvoker
	invokerLock sync.RWMutex

	// asyncInvokeSem bounds the number of concurrently-running
	// FunctionInvokeAsync goroutines across the gateway. A buffered channel
	// used as a counting semaphore: a slot is taken before spawning and
	// released when the goroutine finishes. When full, FunctionInvokeAsync
	// rejects (backpressure to the guest) instead of spawning unbounded
	// goroutines under a frame flood. Built in NewHostFunctions; nil only in
	// bare test construction (treated as unbounded there).
	asyncInvokeSem chan struct{}

	// TURN config — feat-9. Cached at NewHostFunctions; immutable for
	// the gateway's lifetime so no lock needed. Empty TURNSecret means
	// `turn_credentials` host fn returns a configured=false envelope
	// instead of an error (same shape as PushSend's silent-noop when
	// push isn't configured — keeps functions portable).
	turnDomain       string
	turnSecret       string
	stealthCDNDomain string

	// triggerDispatcher is set after construction (via SetTriggerDispatcher).
	// When non-nil, PubSubPublish / PubSubPublishBatch synchronously fire
	// wildcard triggers on the local gateway so functions like
	// presence-aggregator with trigger "presence:*" actually receive
	// WASM-published events (bugboard #93, plan-3 wildcard delivery gap).
	// nil leaves the existing behavior (libp2p-only delivery; wildcards
	// silently dropped on WASM publishes).
	triggerDispatcher     *triggers.PubSubDispatcher
	triggerDispatcherLock sync.RWMutex

	// ephemeralStore backs ephemeral_state_set / ephemeral_state_clear
	// (bugboard #710). Constructed in NewHostFunctions when a WS manager is
	// present; nil otherwise (host fns then return an error). The store
	// registers a disconnect hook on the WS manager so a client's owned state
	// auto-clears the instant its WebSocket disconnects.
	ephemeralStore *serverless.EphemeralStore

	// Current invocation context (set per-execution)
	invCtx     *serverless.InvocationContext
	invCtxLock sync.RWMutex

	// Captured logs for this invocation
	logs     []serverless.LogEntry
	logsLock sync.Mutex
}

// Ensure HostFunctions implements HostServices interface.
var _ serverless.HostServices = (*HostFunctions)(nil)

// Cache constants
const cacheDMapName = "serverless_cache"
