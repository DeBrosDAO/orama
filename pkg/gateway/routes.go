package gateway

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// Routes returns the http.Handler with all routes and middleware configured
func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	// root and v1 health/status
	mux.HandleFunc("/health", g.healthHandler)
	mux.HandleFunc("/status", g.statusHandler)
	mux.HandleFunc("/v1/health", g.healthHandler)
	mux.HandleFunc("/v1/version", g.versionHandler)
	mux.HandleFunc("/v1/status", g.statusHandler)

	// TLS check endpoint for Caddy on-demand TLS
	mux.HandleFunc("/v1/internal/tls/check", g.tlsCheckHandler)

	// ACME DNS-01 challenge endpoints (for Caddy httpreq DNS provider)
	mux.HandleFunc("/v1/internal/acme/present", g.acmePresentHandler)
	mux.HandleFunc("/v1/internal/acme/cleanup", g.acmeCleanupHandler)

	// auth endpoints
	mux.HandleFunc("/v1/auth/jwks", g.authService.JWKSHandler)
	mux.HandleFunc("/.well-known/jwks.json", g.authService.JWKSHandler)
	if g.authHandlers != nil {
		mux.HandleFunc("/v1/auth/login", g.authHandlers.LoginPageHandler)
		mux.HandleFunc("/v1/auth/challenge", g.authHandlers.ChallengeHandler)
		mux.HandleFunc("/v1/auth/verify", g.authHandlers.VerifyHandler)
		// New: issue JWT from API key; new: create or return API key for a wallet after verification
		mux.HandleFunc("/v1/auth/token", g.authHandlers.APIKeyToJWTHandler)
		mux.HandleFunc("/v1/auth/api-key", g.authHandlers.IssueAPIKeyHandler)
		mux.HandleFunc("/v1/auth/simple-key", g.authHandlers.SimpleAPIKeyHandler)
		mux.HandleFunc("/v1/auth/register", g.authHandlers.RegisterHandler)
		mux.HandleFunc("/v1/auth/refresh", g.authHandlers.RefreshHandler)
		mux.HandleFunc("/v1/auth/logout", g.authHandlers.LogoutHandler)
		mux.HandleFunc("/v1/auth/whoami", g.authHandlers.WhoamiHandler)
	}

	// rqlite ORM HTTP gateway (mounts /v1/rqlite/* endpoints)
	if g.ormHTTP != nil {
		g.ormHTTP.BasePath = "/v1/rqlite"
		g.ormHTTP.RegisterRoutes(mux)
	}

	// namespace cluster status (public endpoint for polling during provisioning)
	mux.HandleFunc("/v1/namespace/status", g.namespaceClusterStatusHandler)

	// network
	mux.HandleFunc("/v1/network/status", g.networkStatusHandler)
	mux.HandleFunc("/v1/network/peers", g.networkPeersHandler)
	mux.HandleFunc("/v1/network/connect", g.networkConnectHandler)
	mux.HandleFunc("/v1/network/disconnect", g.networkDisconnectHandler)

	// pubsub
	if g.pubsubHandlers != nil {
		mux.HandleFunc("/v1/pubsub/ws", g.pubsubHandlers.WebsocketHandler)
		mux.HandleFunc("/v1/pubsub/publish", g.pubsubHandlers.PublishHandler)
		mux.HandleFunc("/v1/pubsub/topics", g.pubsubHandlers.TopicsHandler)
		mux.HandleFunc("/v1/pubsub/presence", g.pubsubHandlers.PresenceHandler)
	}

	// anon proxy (authenticated users only)
	mux.HandleFunc("/v1/proxy/anon", g.anonProxyHandler)

	// cache endpoints (Olric)
	if g.cacheHandlers != nil {
		mux.HandleFunc("/v1/cache/health", g.cacheHandlers.HealthHandler)
		mux.HandleFunc("/v1/cache/get", g.cacheHandlers.GetHandler)
		mux.HandleFunc("/v1/cache/mget", g.cacheHandlers.MultiGetHandler)
		mux.HandleFunc("/v1/cache/put", g.cacheHandlers.SetHandler)
		mux.HandleFunc("/v1/cache/delete", g.cacheHandlers.DeleteHandler)
		mux.HandleFunc("/v1/cache/scan", g.cacheHandlers.ScanHandler)
	}

	// storage endpoints (IPFS)
	if g.storageHandlers != nil {
		mux.HandleFunc("/v1/storage/upload", g.storageHandlers.UploadHandler)
		mux.HandleFunc("/v1/storage/pin", g.storageHandlers.PinHandler)
		mux.HandleFunc("/v1/storage/status/", g.storageHandlers.StatusHandler)
		mux.HandleFunc("/v1/storage/get/", g.storageHandlers.DownloadHandler)
		mux.HandleFunc("/v1/storage/unpin/", g.storageHandlers.UnpinHandler)
	}

	// serverless functions (if enabled)
	if g.serverlessHandlers != nil {
		g.serverlessHandlers.RegisterRoutes(mux)
	}

	// deployment endpoints
	if g.deploymentService != nil {
		// Static deployments
		mux.HandleFunc("/v1/deployments/static/upload", g.staticHandler.HandleUpload)
		mux.HandleFunc("/v1/deployments/static/update", g.withHomeNodeProxy(g.updateHandler.HandleUpdate))

		// Next.js deployments
		mux.HandleFunc("/v1/deployments/nextjs/upload", g.nextjsHandler.HandleUpload)
		mux.HandleFunc("/v1/deployments/nextjs/update", g.withHomeNodeProxy(g.updateHandler.HandleUpdate))

		// Go backend deployments
		if g.goHandler != nil {
			mux.HandleFunc("/v1/deployments/go/upload", g.goHandler.HandleUpload)
			mux.HandleFunc("/v1/deployments/go/update", g.withHomeNodeProxy(g.updateHandler.HandleUpdate))
		}

		// Node.js backend deployments
		if g.nodejsHandler != nil {
			mux.HandleFunc("/v1/deployments/nodejs/upload", g.nodejsHandler.HandleUpload)
			mux.HandleFunc("/v1/deployments/nodejs/update", g.withHomeNodeProxy(g.updateHandler.HandleUpdate))
		}

		// Deployment management
		mux.HandleFunc("/v1/deployments/list", g.listHandler.HandleList)
		mux.HandleFunc("/v1/deployments/get", g.listHandler.HandleGet)
		mux.HandleFunc("/v1/deployments/delete", g.withHomeNodeProxy(g.listHandler.HandleDelete))
		mux.HandleFunc("/v1/deployments/rollback", g.withHomeNodeProxy(g.rollbackHandler.HandleRollback))
		mux.HandleFunc("/v1/deployments/versions", g.rollbackHandler.HandleListVersions)
		mux.HandleFunc("/v1/deployments/logs", g.withHomeNodeProxy(g.logsHandler.HandleLogs))
		mux.HandleFunc("/v1/deployments/stats", g.withHomeNodeProxy(g.statsHandler.HandleStats))
		mux.HandleFunc("/v1/deployments/events", g.logsHandler.HandleGetEvents)

		// Internal replica coordination endpoints
		if g.replicaHandler != nil {
			mux.HandleFunc("/v1/internal/deployments/replica/setup", g.replicaHandler.HandleSetup)
			mux.HandleFunc("/v1/internal/deployments/replica/update", g.replicaHandler.HandleUpdate)
			mux.HandleFunc("/v1/internal/deployments/replica/rollback", g.replicaHandler.HandleRollback)
			mux.HandleFunc("/v1/internal/deployments/replica/teardown", g.replicaHandler.HandleTeardown)
		}

		// Custom domains
		mux.HandleFunc("/v1/deployments/domains/add", g.domainHandler.HandleAddDomain)
		mux.HandleFunc("/v1/deployments/domains/verify", g.domainHandler.HandleVerifyDomain)
		mux.HandleFunc("/v1/deployments/domains/list", g.domainHandler.HandleListDomains)
		mux.HandleFunc("/v1/deployments/domains/remove", g.domainHandler.HandleRemoveDomain)
	}

	// SQLite database endpoints
	if g.sqliteHandler != nil {
		mux.HandleFunc("/v1/db/sqlite/create", g.sqliteHandler.CreateDatabase)
		mux.HandleFunc("/v1/db/sqlite/query", g.sqliteHandler.QueryDatabase)
		mux.HandleFunc("/v1/db/sqlite/list", g.sqliteHandler.ListDatabases)
		mux.HandleFunc("/v1/db/sqlite/backup", g.sqliteBackupHandler.BackupDatabase)
		mux.HandleFunc("/v1/db/sqlite/backups", g.sqliteBackupHandler.ListBackups)
	}

	return g.withMiddleware(mux)
}

// withHomeNodeProxy wraps a deployment handler to proxy requests to the home node
// if the current node is not the home node for the deployment.
func (g *Gateway) withHomeNodeProxy(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Already proxied — prevent loops
		if r.Header.Get("X-Orama-Proxy-Node") != "" {
			handler(w, r)
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			handler(w, r)
			return
		}
		ctx := r.Context()
		namespace, _ := ctx.Value(ctxkeys.NamespaceOverride).(string)
		if namespace == "" {
			handler(w, r)
			return
		}
		deployment, err := g.deploymentService.GetDeployment(ctx, namespace, name)
		if err != nil {
			handler(w, r) // let handler return proper error
			return
		}
		if g.nodePeerID != "" && deployment.HomeNodeID != "" &&
			deployment.HomeNodeID != g.nodePeerID {
			if g.proxyCrossNode(w, r, deployment) {
				return
			}
		}
		handler(w, r)
	}
}
