package hostfunctions

import (
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/anyoneproxy"
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

	// Build the Anyone-routed HTTP client only when Anyone routing is
	// enabled on this gateway (feat-11). When disabled, leave it nil so
	// AnyoneFetch returns a typed error instead of silently using the
	// direct path. anyoneproxy.NewHTTPClient() returns a fresh client
	// with a SOCKS transport when enabled — safe to set Timeout on it
	// (when disabled it returns the shared http.DefaultClient, which we
	// must NOT mutate; the Enabled() guard ensures we never reach that).
	var anyoneHTTPClient *http.Client
	if anyoneproxy.Enabled() {
		anyoneHTTPClient = anyoneproxy.NewHTTPClient()
		anyoneHTTPClient.Timeout = httpTimeout
	}

	return &HostFunctions{
		db:               db,
		cacheClient:      cacheClient,
		storage:          storage,
		ipfsAPIURL:       cfg.IPFSAPIURL,
		pubsub:           pubsubAdapter,
		wsManager:        wsManager,
		secrets:          secrets,
		pushDispatcher:   pushDispatcher,
		pushManager:      pushManager,
		wsBridge:         wsBridge,
		anyoneHTTPClient: anyoneHTTPClient,
		turnDomain:       cfg.TURNDomain,
		turnSecret:       cfg.TURNSecret,
		stealthCDNDomain: cfg.StealthCDNDomain,
		httpClient:       tlsutil.NewHTTPClient(httpTimeout),
		logger:           logger,
		logs:             make([]serverless.LogEntry, 0),
		asyncInvokeSem:   make(chan struct{}, asyncInvokeMaxInFlight),
	}
}
