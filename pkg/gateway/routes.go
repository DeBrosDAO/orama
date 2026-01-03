package gateway

import "net/http"

// Routes returns the http.Handler with all routes and middleware configured
func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	// root and v1 health/status
	mux.HandleFunc("/health", g.healthHandler)
	mux.HandleFunc("/status", g.statusHandler)
	mux.HandleFunc("/v1/health", g.healthHandler)
	mux.HandleFunc("/v1/version", g.versionHandler)
	mux.HandleFunc("/v1/status", g.statusHandler)

	// auth endpoints
	mux.HandleFunc("/v1/auth/jwks", g.authService.JWKSHandler)
	mux.HandleFunc("/.well-known/jwks.json", g.authService.JWKSHandler)
	mux.HandleFunc("/v1/auth/login", g.loginPageHandler)
	mux.HandleFunc("/v1/auth/challenge", g.challengeHandler)
	mux.HandleFunc("/v1/auth/verify", g.verifyHandler)
	// New: issue JWT from API key; new: create or return API key for a wallet after verification
	mux.HandleFunc("/v1/auth/token", g.apiKeyToJWTHandler)
	mux.HandleFunc("/v1/auth/api-key", g.issueAPIKeyHandler)
	mux.HandleFunc("/v1/auth/simple-key", g.simpleAPIKeyHandler)
	mux.HandleFunc("/v1/auth/register", g.registerHandler)
	mux.HandleFunc("/v1/auth/refresh", g.refreshHandler)
	mux.HandleFunc("/v1/auth/logout", g.logoutHandler)
	mux.HandleFunc("/v1/auth/whoami", g.whoamiHandler)

	// rqlite ORM HTTP gateway (mounts /v1/rqlite/* endpoints)
	if g.ormHTTP != nil {
		g.ormHTTP.BasePath = "/v1/rqlite"
		g.ormHTTP.RegisterRoutes(mux)
	}

	// network
	mux.HandleFunc("/v1/network/status", g.networkStatusHandler)
	mux.HandleFunc("/v1/network/peers", g.networkPeersHandler)
	mux.HandleFunc("/v1/network/connect", g.networkConnectHandler)
	mux.HandleFunc("/v1/network/disconnect", g.networkDisconnectHandler)

	// pubsub
	mux.HandleFunc("/v1/pubsub/ws", g.pubsubWebsocketHandler)
	mux.HandleFunc("/v1/pubsub/publish", g.pubsubPublishHandler)
	mux.HandleFunc("/v1/pubsub/topics", g.pubsubTopicsHandler)
	mux.HandleFunc("/v1/pubsub/presence", g.pubsubPresenceHandler)

	// anon proxy (authenticated users only)
	mux.HandleFunc("/v1/proxy/anon", g.anonProxyHandler)

	// cache endpoints (Olric)
	mux.HandleFunc("/v1/cache/health", g.cacheHealthHandler)
	mux.HandleFunc("/v1/cache/get", g.cacheGetHandler)
	mux.HandleFunc("/v1/cache/mget", g.cacheMultiGetHandler)
	mux.HandleFunc("/v1/cache/put", g.cachePutHandler)
	mux.HandleFunc("/v1/cache/delete", g.cacheDeleteHandler)
	mux.HandleFunc("/v1/cache/scan", g.cacheScanHandler)

	// storage endpoints (IPFS)
	mux.HandleFunc("/v1/storage/upload", g.storageUploadHandler)
	mux.HandleFunc("/v1/storage/pin", g.storagePinHandler)
	mux.HandleFunc("/v1/storage/status/", g.storageStatusHandler)
	mux.HandleFunc("/v1/storage/get/", g.storageGetHandler)
	mux.HandleFunc("/v1/storage/unpin/", g.storageUnpinHandler)

	// serverless functions (if enabled)
	if g.serverlessHandlers != nil {
		g.serverlessHandlers.RegisterRoutes(mux)
	}

	return g.withMiddleware(mux)
}
