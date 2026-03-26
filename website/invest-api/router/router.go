package router

import (
	"database/sql"
	"net/http"

	"github.com/debros/orama-website/invest-api/auth"
	"github.com/debros/orama-website/invest-api/handler"
	"github.com/debros/orama-website/invest-api/helius"
	mw "github.com/debros/orama-website/invest-api/middleware"
)

func New(database *sql.DB, jwtSecret string, heliusClient *helius.Client) http.Handler {
	mux := http.NewServeMux()
	requireAuth := mw.RequireAuth(jwtSecret)

	// Public endpoints
	mux.HandleFunc("GET /api/stats", handler.StatsHandler(database))
	mux.HandleFunc("GET /api/activity", handler.ActivityHandler(database))
	mux.HandleFunc("GET /api/price", handler.PriceHandler())

	// Auth
	mux.HandleFunc("POST /api/auth/login", auth.LoginHandler(database, jwtSecret))

	// Authenticated endpoints
	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/me", handler.MeHandler(database))
	authed.HandleFunc("POST /api/purchase/token", handler.TokenPurchaseHandler(database))
	authed.HandleFunc("POST /api/purchase/license", handler.LicensePurchaseHandler(database, heliusClient))
	authed.HandleFunc("POST /api/whitelist/join", handler.WhitelistJoinHandler(database))
	authed.HandleFunc("GET /api/nft/status", handler.NftStatusHandler(heliusClient))
	authed.HandleFunc("GET /api/anchat/balance", handler.AnchatBalanceHandler(database, heliusClient))
	authed.HandleFunc("POST /api/anchat/claim", handler.AnchatClaimHandler(database, heliusClient))

	// Wire authenticated routes through auth middleware
	mux.Handle("GET /api/me", requireAuth(authed))
	mux.Handle("POST /api/purchase/", requireAuth(authed))
	mux.Handle("POST /api/whitelist/", requireAuth(authed))
	mux.Handle("GET /api/nft/", requireAuth(authed))
	mux.Handle("GET /api/anchat/", requireAuth(authed))
	mux.Handle("POST /api/anchat/", requireAuth(authed))

	return mw.CORS(mux)
}
