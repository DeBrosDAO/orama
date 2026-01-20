package hostfunctions

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// NewHostFunctions creates a new HostFunctions instance.
func NewHostFunctions(
	db rqlite.Client,
	cacheClient olriclib.Client,
	storage ipfs.IPFSClient,
	pubsubAdapter *pubsub.ClientAdapter,
	wsManager serverless.WebSocketManager,
	secrets serverless.SecretsManager,
	cfg HostFunctionsConfig,
	logger *zap.Logger,
) *HostFunctions {
	httpTimeout := cfg.HTTPTimeout
	if httpTimeout == 0 {
		httpTimeout = 30 * time.Second
	}

	return &HostFunctions{
		db:          db,
		cacheClient: cacheClient,
		storage:     storage,
		ipfsAPIURL:  cfg.IPFSAPIURL,
		pubsub:      pubsubAdapter,
		wsManager:   wsManager,
		secrets:     secrets,
		httpClient:  tlsutil.NewHTTPClient(httpTimeout),
		logger:      logger,
		logs:        make([]serverless.LogEntry, 0),
	}
}
