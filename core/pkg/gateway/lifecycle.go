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
	// Flush PubSub aggregator buffers before tearing down the engine.
	// Pending events are dispatched via the invoker which still needs the
	// engine to be alive, so this MUST happen before the engine close.
	// Aggregator state is local to this node — events not flushed here are
	// lost (intended trade-off for high-frequency lossy streams).
	if g.pubsubDispatcher != nil {
		if agg := g.pubsubDispatcher.Aggregator(); agg != nil {
			// 5s budget — same as the engine close timeout below.
			// In-flight flushes call back into the invoker which still
			// needs the engine to be alive.
			if !agg.Shutdown(5 * time.Second) {
				g.logger.ComponentWarn(logging.ComponentGeneral,
					"PubSub aggregator shutdown timed out; some buffered events may be lost")
			}
		}
	}

	// Stop the cron scheduler before tearing down the engine — pending
	// invocations call back into the invoker which still needs the engine
	// to be alive.
	if g.cronScheduler != nil {
		g.cronScheduler.Stop()
	}

	// Stop the pubsub dispatcher's periodic refresh goroutine. libp2p
	// subscriptions die naturally with the client teardown below.
	if g.pubsubDispatcher != nil {
		g.pubsubDispatcher.Stop()
	}

	// Drain persistent WebSocket instances. Each instance gets a slice of
	// the 30s budget; ws_close on each is best-effort.
	if g.persistentWSManager != nil {
		g.persistentWSManager.ShutdownAll(30 * time.Second)
	}

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

	// Stop background goroutines
	if g.mwCache != nil {
		g.mwCache.Stop()
	}
	if g.rateLimiter != nil {
		g.rateLimiter.Stop()
	}
}
