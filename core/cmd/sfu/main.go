package main

import (
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	logger, err := logging.NewColoredLogger(logging.ComponentSFU, true)
	if err != nil {
		panic(err)
	}

	logger.ComponentInfo(logging.ComponentSFU, "Starting SFU server",
		zap.String("version", version),
		zap.String("commit", commit))

	cfg := parseSFUConfig(logger)

	server, err := newSFUServer(cfg, logger.Logger)
	if err != nil {
		logger.ComponentError(logging.ComponentSFU, "Failed to create SFU server", zap.Error(err))
		os.Exit(1)
	}

	// Start HTTP server in background
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ComponentError(logging.ComponentSFU, "SFU server error", zap.Error(err))
			os.Exit(1)
		}
	}()

	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	sig := <-quit

	logger.ComponentInfo(logging.ComponentSFU, "Shutdown signal received", zap.String("signal", sig.String()))

	// Graceful drain: notify peers and wait
	server.Drain(30 * time.Second)

	if err := server.Close(); err != nil {
		logger.ComponentError(logging.ComponentSFU, "Error during shutdown", zap.Error(err))
	}

	logger.ComponentInfo(logging.ComponentSFU, "SFU server shutdown complete")
}
