package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	logger, err := logging.NewColoredLogger(logging.ComponentTURN, true)
	if err != nil {
		panic(err)
	}

	logger.ComponentInfo(logging.ComponentTURN, "Starting TURN server",
		zap.String("version", version),
		zap.String("commit", commit))

	cfg := parseTURNConfig(logger)

	server, err := turn.NewServer(cfg, logger.Logger)
	if err != nil {
		logger.ComponentError(logging.ComponentTURN, "Failed to start TURN server", zap.Error(err))
		os.Exit(1)
	}

	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	sig := <-quit

	logger.ComponentInfo(logging.ComponentTURN, "Shutdown signal received", zap.String("signal", sig.String()))

	if err := server.Close(); err != nil {
		logger.ComponentError(logging.ComponentTURN, "Error during shutdown", zap.Error(err))
	}

	logger.ComponentInfo(logging.ComponentTURN, "TURN server shutdown complete")
}
