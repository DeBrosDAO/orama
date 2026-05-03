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

	// pushDispatcher may be nil when push isn't configured for this gateway.
	// In that case PushSend returns nil silently — see hostfunctions/push.go.
	pushDispatcher *push.PushDispatcher

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
