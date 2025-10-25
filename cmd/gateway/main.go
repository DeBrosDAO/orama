package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/caddyserver/certmagic"
	"go.uber.org/zap"
)

const acmeEmail = "dev@debros.io"

func setupLogger() *logging.ColoredLogger {
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, true)
	if err != nil {
		panic(err)
	}
	return logger
}

func main() {
	logger := setupLogger()

	// Load gateway config (flags/env)
	cfg := parseGatewayConfig(logger)

	logger.ComponentInfo(logging.ComponentGeneral, "Starting gateway initialization...")

	// Initialize gateway (connect client, prepare routes)
	gw, err := gateway.New(logger, cfg)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "failed to initialize gateway", zap.Error(err))
		os.Exit(1)
	}
	defer gw.Close()

	logger.ComponentInfo(logging.ComponentGeneral, "Gateway initialization completed successfully")

	logger.ComponentInfo(logging.ComponentGeneral, "Creating HTTP server and routes...")

	// Wrap handler with host enforcement if domain is set
	handler := gw.Routes()
	if cfg.Domain != "" {
		d := cfg.Domain
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if i := strings.IndexByte(host, ':'); i >= 0 {
				host = host[:i]
			}
			if !strings.EqualFold(host, d) {
				http.NotFound(w, r)
				return
			}
			gw.Routes().ServeHTTP(w, r)
		})
	}

	// If domain is configured, use ACME TLS on :443 and :80 for challenges
	if cfg.Domain != "" {
		logger.ComponentInfo(logging.ComponentGeneral, "Production ACME TLS enabled",
			zap.String("domain", cfg.Domain),
			zap.String("acme_email", acmeEmail),
		)

		// Setup CertMagic with file storage
		certDir := filepath.Join(os.ExpandEnv("$HOME"), ".debros", "certmagic")
		if home, err := os.UserHomeDir(); err == nil {
			certDir = filepath.Join(home, ".debros", "certmagic")
		}

		if err := os.MkdirAll(certDir, 0700); err != nil {
			logger.ComponentError(logging.ComponentGeneral, "failed to create certmagic directory", zap.Error(err))
			os.Exit(1)
		}

		// Configure CertMagic for ACME
		logger.ComponentInfo(logging.ComponentGeneral, "Provisioning ACME certificate...",
			zap.String("domain", cfg.Domain),
		)

		// Use the default CertMagic instance and configure storage
		certmagic.Default.Storage = &certmagic.FileStorage{Path: certDir}

		// Setup ACME issuer
		acmeIssuer := certmagic.ACMEIssuer{
			CA:     certmagic.LetsEncryptProductionCA,
			Email:  acmeEmail,
			Agreed: true,
		}
		certmagic.Default.Issuers = []certmagic.Issuer{&acmeIssuer}

		// Manage the domain
		if err := certmagic.ManageSync(context.Background(), []string{cfg.Domain}); err != nil {
			logger.ComponentError(logging.ComponentGeneral, "ACME ManageSync failed", zap.Error(err))
			os.Exit(1)
		}

		// Get TLS config
		tlsCfg := certmagic.Default.TLSConfig()

		// Start HTTP server on :80 for ACME challenges and redirect to HTTPS
		logger.ComponentInfo(logging.ComponentGeneral, "Starting HTTP server on :80 for ACME challenges")
		go func() {
			httpMux := http.NewServeMux()
			httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				// Redirect all HTTP to HTTPS
				u := *r.URL
				u.Scheme = "https"
				u.Host = cfg.Domain
				http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
			})
			// HTTP server for ACME challenges and redirects
			if err := http.ListenAndServe(":80", httpMux); err != nil && err != http.ErrServerClosed {
				logger.ComponentError(logging.ComponentGeneral, "HTTP :80 server error", zap.Error(err))
			}
			logger.ComponentInfo(logging.ComponentGeneral, "HTTP :80 server stopped")
		}()

		// Start HTTPS server on :443
		logger.ComponentInfo(logging.ComponentGeneral, "Starting HTTPS server on :443 with ACME certificate",
			zap.String("domain", cfg.Domain),
		)

		httpsServer := &http.Server{
			Addr:      ":443",
			Handler:   handler,
			TLSConfig: tlsCfg,
		}

		ln, err := net.Listen("tcp", httpsServer.Addr)
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral, "failed to bind HTTPS listen address", zap.Error(err))
			os.Exit(1)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "HTTPS listener bound", zap.String("listen_addr", ln.Addr().String()))

		// Serve in a goroutine so we can handle graceful shutdown on signals.
		serveErrCh := make(chan error, 1)
		go func() {
			if err := httpsServer.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
				serveErrCh <- err
				return
			}
			serveErrCh <- nil
		}()

		// Wait for termination signal or server error
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

		select {
		case sig := <-quit:
			logger.ComponentInfo(logging.ComponentGeneral, "shutdown signal received", zap.String("signal", sig.String()))
		case err := <-serveErrCh:
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "HTTPS server error", zap.Error(err))
			} else {
				logger.ComponentInfo(logging.ComponentGeneral, "HTTPS server exited normally")
			}
		}

		logger.ComponentInfo(logging.ComponentGeneral, "Shutting down gateway HTTPS server...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpsServer.Shutdown(ctx); err != nil {
			logger.ComponentError(logging.ComponentGeneral, "HTTPS server shutdown error", zap.Error(err))
		} else {
			logger.ComponentInfo(logging.ComponentGeneral, "Gateway shutdown complete")
		}
		return
	}

	// Fallback: HTTP server on configured listen_addr when no domain
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	// Try to bind listener explicitly so binding failures are visible immediately.
	logger.ComponentInfo(logging.ComponentGeneral, "Gateway HTTP server starting",
		zap.String("addr", cfg.ListenAddr),
		zap.String("namespace", cfg.ClientNamespace),
		zap.Int("bootstrap_peer_count", len(cfg.BootstrapPeers)),
	)

	logger.ComponentInfo(logging.ComponentGeneral, "Attempting to bind HTTP listener...")

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "failed to bind HTTP listen address", zap.Error(err))
		// exit because server cannot function without a listener
		os.Exit(1)
	}
	logger.ComponentInfo(logging.ComponentGeneral, "HTTP listener bound", zap.String("listen_addr", ln.Addr().String()))

	// Serve in a goroutine so we can handle graceful shutdown on signals.
	serveErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}()

	// Wait for termination signal or server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.ComponentInfo(logging.ComponentGeneral, "shutdown signal received", zap.String("signal", sig.String()))
	case err := <-serveErrCh:
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral, "HTTP server error", zap.Error(err))
			// continue to shutdown path so we close resources cleanly
		} else {
			logger.ComponentInfo(logging.ComponentGeneral, "HTTP server exited normally")
		}
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Shutting down gateway HTTP server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.ComponentError(logging.ComponentGeneral, "HTTP server shutdown error", zap.Error(err))
	} else {
		logger.ComponentInfo(logging.ComponentGeneral, "Gateway shutdown complete")
	}
}
