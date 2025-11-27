package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// HTTPSGateway extends HTTPGateway with HTTPS/TLS support
type HTTPSGateway struct {
	*HTTPGateway
	httpsConfig *config.HTTPSConfig
	certManager *autocert.Manager
	httpsServer *http.Server
	httpServer  *http.Server // For ACME challenge and redirect
}

// NewHTTPSGateway creates a new HTTPS gateway with Let's Encrypt autocert
func NewHTTPSGateway(logger *logging.ColoredLogger, cfg *config.HTTPGatewayConfig) (*HTTPSGateway, error) {
	// First create the base HTTP gateway
	base, err := NewHTTPGateway(logger, cfg)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, nil
	}

	if !cfg.HTTPS.Enabled {
		// Return base gateway wrapped in HTTPSGateway for consistent interface
		return &HTTPSGateway{HTTPGateway: base}, nil
	}

	gateway := &HTTPSGateway{
		HTTPGateway: base,
		httpsConfig: &cfg.HTTPS,
	}

	// Check if using self-signed certificates or Let's Encrypt
	if cfg.HTTPS.UseSelfSigned || (cfg.HTTPS.CertFile != "" && cfg.HTTPS.KeyFile != "") {
		// Using self-signed or pre-existing certificates
		logger.ComponentInfo(logging.ComponentGeneral, "Using self-signed or pre-configured certificates for HTTPS",
			zap.String("domain", cfg.HTTPS.Domain),
			zap.String("cert_file", cfg.HTTPS.CertFile),
			zap.String("key_file", cfg.HTTPS.KeyFile),
		)
		// Don't set certManager - will use CertFile/KeyFile from config
	} else if cfg.HTTPS.AutoCert {
		// Use Let's Encrypt (existing logic)
		cacheDir := cfg.HTTPS.CacheDir
		if cacheDir == "" {
			cacheDir = "/home/debros/.orama/tls-cache"
		}

		// Check environment for staging mode
		directoryURL := "https://acme-v02.api.letsencrypt.org/directory" // Production
		if os.Getenv("DEBROS_ACME_STAGING") != "" {
			directoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
			logger.ComponentWarn(logging.ComponentGeneral,
				"Using Let's Encrypt STAGING - certificates will not be trusted by production clients",
				zap.String("domain", cfg.HTTPS.Domain),
			)
		}

		gateway.certManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.HTTPS.Domain),
			Cache:      autocert.DirCache(cacheDir),
			Email:      cfg.HTTPS.Email,
			Client: &acme.Client{
				DirectoryURL: directoryURL,
			},
		}

		logger.ComponentInfo(logging.ComponentGeneral, "Let's Encrypt autocert configured",
			zap.String("domain", cfg.HTTPS.Domain),
			zap.String("cache_dir", cacheDir),
			zap.String("acme_environment", map[bool]string{true: "staging", false: "production"}[directoryURL == "https://acme-staging-v02.api.letsencrypt.org/directory"]),
		)
	}

	return gateway, nil
}

// Start starts both HTTP (for ACME) and HTTPS servers
func (g *HTTPSGateway) Start(ctx context.Context) error {
	if g == nil {
		return nil
	}

	// If HTTPS is not enabled, just start the base HTTP gateway
	if !g.httpsConfig.Enabled {
		return g.HTTPGateway.Start(ctx)
	}

	httpPort := g.httpsConfig.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	httpsPort := g.httpsConfig.HTTPSPort
	if httpsPort == 0 {
		httpsPort = 443
	}

	// Start HTTP server for ACME challenge and redirect
	g.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: g.httpHandler(),
	}

	go func() {
		g.logger.ComponentInfo(logging.ComponentGeneral, "HTTP server starting (ACME/redirect)",
			zap.Int("port", httpPort),
		)
		if err := g.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.logger.ComponentError(logging.ComponentGeneral, "HTTP server error", zap.Error(err))
		}
	}()

	// Set up TLS config
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if g.certManager != nil {
		tlsConfig.GetCertificate = g.certManager.GetCertificate
	} else if g.httpsConfig.CertFile != "" && g.httpsConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(g.httpsConfig.CertFile, g.httpsConfig.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	} else {
		return fmt.Errorf("HTTPS enabled but no certificate source configured")
	}

	// Start HTTPS server
	g.httpsServer = &http.Server{
		Addr:      fmt.Sprintf(":%d", httpsPort),
		Handler:   g.router,
		TLSConfig: tlsConfig,
	}

	listener, err := tls.Listen("tcp", g.httpsServer.Addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TLS listener: %w", err)
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "HTTPS Gateway starting",
		zap.String("domain", g.httpsConfig.Domain),
		zap.Int("port", httpsPort),
	)

	go func() {
		if err := g.httpsServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			g.logger.ComponentError(logging.ComponentGeneral, "HTTPS server error", zap.Error(err))
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	return g.Stop()
}

// httpHandler returns a handler for the HTTP server (ACME challenge + redirect)
func (g *HTTPSGateway) httpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle ACME challenge
		if g.certManager != nil && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			g.certManager.HTTPHandler(nil).ServeHTTP(w, r)
			return
		}

		// Redirect HTTP to HTTPS
		httpsPort := g.httpsConfig.HTTPSPort
		if httpsPort == 0 {
			httpsPort = 443
		}

		target := "https://" + r.Host + r.URL.RequestURI()
		if httpsPort != 443 {
			host := r.Host
			if idx := strings.LastIndex(host, ":"); idx > 0 {
				host = host[:idx]
			}
			target = fmt.Sprintf("https://%s:%d%s", host, httpsPort, r.URL.RequestURI())
		}

		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// Stop gracefully stops both HTTP and HTTPS servers
func (g *HTTPSGateway) Stop() error {
	if g == nil {
		return nil
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "HTTPS Gateway shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var errs []error

	if g.httpServer != nil {
		if err := g.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("HTTP server shutdown: %w", err))
		}
	}

	if g.httpsServer != nil {
		if err := g.httpsServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("HTTPS server shutdown: %w", err))
		}
	}

	if g.HTTPGateway.server != nil {
		if err := g.HTTPGateway.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("base gateway shutdown: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "HTTPS Gateway shutdown complete")
	return nil
}
