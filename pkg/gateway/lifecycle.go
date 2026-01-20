package gateway

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Close gracefully shuts down the gateway and all its dependencies.
// It closes the serverless engine, network client, database connections,
// Olric cache client, and IPFS client in sequence.
func (g *Gateway) Close() {
	// Close serverless engine first
	if g.serverlessEngine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := g.serverlessEngine.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during serverless engine close", zap.Error(err))
		}
		cancel()
	}

	// Disconnect network client
	if g.client != nil {
		if err := g.client.Disconnect(); err != nil {
			g.logger.ComponentWarn(logging.ComponentClient, "error during client disconnect", zap.Error(err))
		}
	}

	// Close SQL database connection
	if g.sqlDB != nil {
		_ = g.sqlDB.Close()
	}

	// Close Olric cache client
	if client := g.getOlricClient(); client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during Olric client close", zap.Error(err))
		}
	}

	// Close IPFS client
	if g.ipfsClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.ipfsClient.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during IPFS client close", zap.Error(err))
		}
	}
}
