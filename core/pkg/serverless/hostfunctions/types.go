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
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// HostFunctionsConfig holds configuration for HostFunctions.
type HostFunctionsConfig struct {
	IPFSAPIURL  string
	HTTPTimeout time.Duration
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
	logger      *zap.Logger

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
