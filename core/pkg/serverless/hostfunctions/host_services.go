package hostfunctions

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// NewHostFunctions creates a new HostFunctions instance.
//
// pushDispatcher / pushManager / wsBridge may be nil when those features
// aren't configured on this gateway. PushSend prefers pushManager when
// present (per-namespace config takes effect), falls back to pushDispatcher,
// and silently no-ops when both are nil — so functions stay portable
// across deployments with/without push.
//
// WSPubSubBridge returns an explicit error when wsBridge is nil because
// absence of a requested bridge should be visible (callers asked for it).
func NewHostFunctions(
	db rqlite.Client,
	cacheClient olriclib.Client,
	storage ipfs.IPFSClient,
	pubsubAdapter *pubsub.ClientAdapter,
	wsManager serverless.WebSocketManager,
	secrets serverless.SecretsManager,
	pushDispatcher *push.PushDispatcher,
	pushManager *push.Manager,
	wsBridge *wsbridge.Bridge,
	cfg HostFunctionsConfig,
	logger *zap.Logger,
) *HostFunctions {
	httpTimeout := cfg.HTTPTimeout
	if httpTimeout == 0 {
		httpTimeout = 30 * time.Second
	}

	return &HostFunctions{
		db:             db,
		cacheClient:    cacheClient,
		storage:        storage,
		ipfsAPIURL:     cfg.IPFSAPIURL,
		pubsub:         pubsubAdapter,
		wsManager:      wsManager,
		secrets:        secrets,
		pushDispatcher: pushDispatcher,
		pushManager:    pushManager,
		wsBridge:       wsBridge,
		httpClient:     tlsutil.NewHTTPClient(httpTimeout),
		logger:         logger,
		logs:           make([]serverless.LogEntry, 0),
	}
}
