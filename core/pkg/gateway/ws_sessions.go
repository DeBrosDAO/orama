package gateway

import (
	"github.com/DeBrosOfficial/network/pkg/gateway/wssession"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// startWSSessionSweeper closes the WebSockets whose token expired or was
// revoked.
//
// The function sockets (stateless and persistent) and the pubsub subscriber
// sockets register in g.wsSessions when they open — the one registry
// NewDependencies builds and hands to both handler sets. Each socket was
// authorized once, at its upgrade; the sweeper is what keeps asking
// afterwards. It stops when the gateway closes.
func (g *Gateway) startWSSessionSweeper() {
	// Without an auth service this gateway verifies no token, so no socket is
	// opened with one and there is nothing to sweep.
	if g.authService == nil {
		g.logger.ComponentWarn(logging.ComponentGeneral,
			"no auth service: WebSockets are not held to token expiry or revocation")
		return
	}
	go g.wsSessions.Run(g.shutdownCtx, g.authService, wssession.SweepInterval)
}
