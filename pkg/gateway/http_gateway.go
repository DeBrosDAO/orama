package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// HTTPGateway is the main reverse proxy router
type HTTPGateway struct {
	logger         *logging.ColoredLogger
	config         *config.HTTPGatewayConfig
	router         chi.Router
	reverseProxies map[string]*httputil.ReverseProxy
	mu             sync.RWMutex
	server         *http.Server
}

// NewHTTPGateway creates a new HTTP reverse proxy gateway
func NewHTTPGateway(logger *logging.ColoredLogger, cfg *config.HTTPGatewayConfig) (*HTTPGateway, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if logger == nil {
		var err error
		logger, err = logging.NewColoredLogger(logging.ComponentGeneral, true)
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
	}

	gateway := &HTTPGateway{
		logger:         logger,
		config:         cfg,
		router:         chi.NewRouter(),
		reverseProxies: make(map[string]*httputil.ReverseProxy),
	}

	// Set up router middleware
	gateway.router.Use(middleware.RequestID)
	gateway.router.Use(middleware.Logger)
	gateway.router.Use(middleware.Recoverer)
	gateway.router.Use(middleware.Timeout(30 * time.Second))

	// Add health check endpoint
	gateway.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","node":"%s"}`, cfg.NodeName)
	})

	// Initialize reverse proxies and routes
	if err := gateway.initializeRoutes(); err != nil {
		return nil, fmt.Errorf("failed to initialize routes: %w", err)
	}

	gateway.logger.ComponentInfo(logging.ComponentGeneral, "HTTP Gateway initialized",
		zap.String("node_name", cfg.NodeName),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.Int("routes", len(cfg.Routes)),
	)

	return gateway, nil
}

// initializeRoutes sets up all reverse proxy routes
func (hg *HTTPGateway) initializeRoutes() error {
	hg.mu.Lock()
	defer hg.mu.Unlock()

	for routeName, routeConfig := range hg.config.Routes {
		// Validate backend URL
		_, err := url.Parse(routeConfig.BackendURL)
		if err != nil {
			return fmt.Errorf("invalid backend URL for route %s: %w", routeName, err)
		}

		// Create reverse proxy with custom transport
		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				// Keep original host for Host header
				r.Out.Host = r.In.Host
				// Set X-Forwarded-For header for logging
				r.Out.Header.Set("X-Forwarded-For", getClientIP(r.In))
			},
			ErrorHandler: hg.proxyErrorHandler(routeName),
		}

		// Set timeout on transport
		if routeConfig.Timeout > 0 {
			proxy.Transport = &http.Transport{
				Dial: (&net.Dialer{
					Timeout: routeConfig.Timeout,
				}).Dial,
				ResponseHeaderTimeout: routeConfig.Timeout,
			}
		}

		hg.reverseProxies[routeName] = proxy

		// Register route handler
		hg.registerRouteHandler(routeName, routeConfig, proxy)

		hg.logger.ComponentInfo(logging.ComponentGeneral, "Route initialized",
			zap.String("name", routeName),
			zap.String("path", routeConfig.PathPrefix),
			zap.String("backend", routeConfig.BackendURL),
		)
	}

	return nil
}

// registerRouteHandler registers a route handler with the router
func (hg *HTTPGateway) registerRouteHandler(name string, routeConfig config.RouteConfig, proxy *httputil.ReverseProxy) {
	pathPrefix := strings.TrimSuffix(routeConfig.PathPrefix, "/")
	
	// Use Mount instead of Route for wildcard path handling
	hg.router.Mount(pathPrefix, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hg.handleProxyRequest(w, req, routeConfig, proxy)
	}))
}

// handleProxyRequest handles a reverse proxy request
func (hg *HTTPGateway) handleProxyRequest(w http.ResponseWriter, req *http.Request, routeConfig config.RouteConfig, proxy *httputil.ReverseProxy) {
	// Strip path prefix before forwarding
	originalPath := req.URL.Path
	pathPrefix := strings.TrimSuffix(routeConfig.PathPrefix, "/")

	if strings.HasPrefix(req.URL.Path, pathPrefix) {
		// Remove the prefix but keep leading slash
		strippedPath := strings.TrimPrefix(req.URL.Path, pathPrefix)
		if strippedPath == "" {
			strippedPath = "/"
		}
		req.URL.Path = strippedPath
	}

	// Update request URL to point to backend
	backendURL, _ := url.Parse(routeConfig.BackendURL)
	req.URL.Scheme = backendURL.Scheme
	req.URL.Host = backendURL.Host

	// Log the proxy request
	hg.logger.ComponentInfo(logging.ComponentGeneral, "Proxy request",
		zap.String("original_path", originalPath),
		zap.String("stripped_path", req.URL.Path),
		zap.String("backend", routeConfig.BackendURL),
		zap.String("method", req.Method),
		zap.String("client_ip", getClientIP(req)),
	)

	// Handle WebSocket upgrades if configured
	if routeConfig.WebSocket && isWebSocketRequest(req) {
		hg.logger.ComponentInfo(logging.ComponentGeneral, "WebSocket upgrade detected",
			zap.String("path", originalPath),
		)
	}

	// Forward the request
	proxy.ServeHTTP(w, req)
}

// proxyErrorHandler returns an error handler for the reverse proxy
func (hg *HTTPGateway) proxyErrorHandler(routeName string) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		hg.logger.ComponentError(logging.ComponentGeneral, "Proxy error",
			zap.String("route", routeName),
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.Error(err),
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"gateway error","route":"%s","detail":"%s"}`, routeName, err.Error())
	}
}

// Start starts the HTTP gateway server
func (hg *HTTPGateway) Start(ctx context.Context) error {
	if hg == nil || !hg.config.Enabled {
		return nil
	}

	hg.server = &http.Server{
		Addr:    hg.config.ListenAddr,
		Handler: hg.router,
	}

	// Listen for connections
	listener, err := net.Listen("tcp", hg.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", hg.config.ListenAddr, err)
	}

	hg.logger.ComponentInfo(logging.ComponentGeneral, "HTTP Gateway server starting",
		zap.String("node_name", hg.config.NodeName),
		zap.String("listen_addr", hg.config.ListenAddr),
	)

	// Serve in a goroutine
	go func() {
		if err := hg.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			hg.logger.ComponentError(logging.ComponentGeneral, "HTTP Gateway server error", zap.Error(err))
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	return hg.Stop()
}

// Stop gracefully stops the HTTP gateway server
func (hg *HTTPGateway) Stop() error {
	if hg == nil || hg.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hg.logger.ComponentInfo(logging.ComponentGeneral, "HTTP Gateway shutting down")

	if err := hg.server.Shutdown(ctx); err != nil {
		hg.logger.ComponentError(logging.ComponentGeneral, "HTTP Gateway shutdown error", zap.Error(err))
		return err
	}

	hg.logger.ComponentInfo(logging.ComponentGeneral, "HTTP Gateway shutdown complete")
	return nil
}

// Router returns the chi router for testing or extension
func (hg *HTTPGateway) Router() chi.Router {
	return hg.router
}

// isWebSocketRequest checks if a request is a WebSocket upgrade request
func isWebSocketRequest(r *http.Request) bool {
	return r.Header.Get("Connection") == "Upgrade" &&
		r.Header.Get("Upgrade") == "websocket"
}

