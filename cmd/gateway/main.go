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
