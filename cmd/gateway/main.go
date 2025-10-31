package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
)

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

	// Check if HTTPS is enabled
	if cfg.EnableHTTPS && cfg.DomainName != "" {
		logger.ComponentInfo(logging.ComponentGeneral, "HTTPS enabled with ACME",
			zap.String("domain", cfg.DomainName),
			zap.String("tls_cache_dir", cfg.TLSCacheDir),
		)

		// Set up ACME manager
		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.DomainName),
		}

		// Set cache directory if specified
		if cfg.TLSCacheDir != "" {
			manager.Cache = autocert.DirCache(cfg.TLSCacheDir)
			logger.ComponentInfo(logging.ComponentGeneral, "Using TLS certificate cache",
				zap.String("cache_dir", cfg.TLSCacheDir),
			)
		}

		// Create HTTP server for ACME challenge (port 80)
		httpServer := &http.Server{
			Addr:    ":80",
			Handler: manager.HTTPHandler(nil), // Redirects all HTTP traffic to HTTPS except ACME challenge
		}

		// Create HTTPS server (port 443)
		httpsServer := &http.Server{
			Addr:      ":443",
			Handler:   gw.Routes(),
			TLSConfig: manager.TLSConfig(),
		}

		// Start HTTP server for ACME challenge
		logger.ComponentInfo(logging.ComponentGeneral, "Starting HTTP server for ACME challenge on port 80...")
		httpLn, err := net.Listen("tcp", ":80")
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral, "failed to bind HTTP listen address (port 80)", zap.Error(err))
			os.Exit(1)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "HTTP listener bound", zap.String("listen_addr", httpLn.Addr().String()))

		// Start HTTPS server
		logger.ComponentInfo(logging.ComponentGeneral, "Starting HTTPS server on port 443...")
		httpsLn, err := net.Listen("tcp", ":443")
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral, "failed to bind HTTPS listen address (port 443)", zap.Error(err))
			os.Exit(1)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "HTTPS listener bound", zap.String("listen_addr", httpsLn.Addr().String()))

		// Serve HTTP in a goroutine
		httpServeErrCh := make(chan error, 1)
		go func() {
			if err := httpServer.Serve(httpLn); err != nil && err != http.ErrServerClosed {
				httpServeErrCh <- err
				return
			}
			httpServeErrCh <- nil
		}()

		// Serve HTTPS in a goroutine
		httpsServeErrCh := make(chan error, 1)
		go func() {
			if err := httpsServer.ServeTLS(httpsLn, "", ""); err != nil && err != http.ErrServerClosed {
				httpsServeErrCh <- err
				return
			}
			httpsServeErrCh <- nil
		}()

		// Wait for termination signal or server error
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

		select {
		case sig := <-quit:
			logger.ComponentInfo(logging.ComponentGeneral, "shutdown signal received", zap.String("signal", sig.String()))
		case err := <-httpServeErrCh:
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "HTTP server error", zap.Error(err))
			} else {
				logger.ComponentInfo(logging.ComponentGeneral, "HTTP server exited normally")
			}
		case err := <-httpsServeErrCh:
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "HTTPS server error", zap.Error(err))
			} else {
				logger.ComponentInfo(logging.ComponentGeneral, "HTTPS server exited normally")
			}
		}

		logger.ComponentInfo(logging.ComponentGeneral, "Shutting down gateway servers...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Shutdown HTTPS server
		if err := httpsServer.Shutdown(ctx); err != nil {
			logger.ComponentError(logging.ComponentGeneral, "HTTPS server shutdown error", zap.Error(err))
		} else {
			logger.ComponentInfo(logging.ComponentGeneral, "HTTPS server shutdown complete")
		}

		// Shutdown HTTP server
		if err := httpServer.Shutdown(ctx); err != nil {
			logger.ComponentError(logging.ComponentGeneral, "HTTP server shutdown error", zap.Error(err))
		} else {
			logger.ComponentInfo(logging.ComponentGeneral, "HTTP server shutdown complete")
		}

		logger.ComponentInfo(logging.ComponentGeneral, "Gateway shutdown complete")
		return
	}

	// Standard HTTP server (no HTTPS)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: gw.Routes(),
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
