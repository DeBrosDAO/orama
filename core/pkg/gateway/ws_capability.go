package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/capability"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/hostfunctions"
	"go.uber.org/zap"
)

// Capability-opened function WebSockets (feat-264): the gateway side.

const (
	// capabilityUpgradesPerMinute and capabilityUpgradeBurst bound the
	// capability-opened upgrades one client address may make. Such an upgrade
	// carries no credential, so the address is all there is to limit; a
	// legitimate client opens one socket and keeps it.
	capabilityUpgradesPerMinute = 60
	capabilityUpgradeBurst      = 20

	// capabilityRetryAfterSeconds is what a refused capability upgrade is told
	// to wait: long enough for the bucket to refill a burst.
	capabilityRetryAfterSeconds = 60
)

// isCapabilityUpgrade reports whether a request opens a function's WebSocket
// with a capability rather than a credential: a GET upgrade of exactly
// /v1/functions/{fn}/ws, as the handler parses it, carrying a capability.
// Nothing else under /v1/functions/ becomes anonymous by carrying one.
func isCapabilityUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		isWebSocketUpgrade(r) &&
		serverlesshandlers.IsFunctionAction(r.URL.Path, "ws") &&
		strings.TrimSpace(r.URL.Query().Get(serverlesshandlers.CapabilityQueryParam)) != ""
}

// wireCapabilities gives the WebSocket handlers what checks a capability and
// the host functions what mints and revokes one. Both are keyed from the
// cluster secret; a gateway without one refuses every capability, saying so
// at start and on every attempt.
func wireCapabilities(clusterSecret string, authService *auth.Service,
	handlers *serverlesshandlers.ServerlessHandlers, hostFuncs *hostfunctions.HostFunctions, logger *zap.Logger) error {
	if strings.TrimSpace(clusterSecret) == "" {
		logger.Warn("no cluster secret: this gateway cannot mint or check capabilities, " +
			"and refuses every capability-opened WebSocket with 503")
		return nil
	}
	authority, err := capability.NewAuthority(clusterSecret)
	if err != nil {
		return fmt.Errorf("build the capability authority: %w", err)
	}
	issuer, err := capability.NewIssuer(authority, authService.Revocations())
	if err != nil {
		return fmt.Errorf("build the capability issuer: %w", err)
	}
	handlers.SetCapabilities(authority, authService)
	hostFuncs.SetCapabilityIssuer(issuer)
	return nil
}
