// Package auth provides HTTP handlers for wallet-based authentication,
// JWT token management, and API key operations. It supports challenge/response
// flows using cryptographic signatures for Ethereum and other blockchain wallets.
package auth

import (
	"context"
	"database/sql"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// Use shared context keys from ctxkeys package to ensure consistency with middleware
const (
	CtxKeyAPIKey            = ctxkeys.APIKey
	CtxKeyJWT               = ctxkeys.JWT
	CtxKeyNamespaceOverride = ctxkeys.NamespaceOverride
)

// NetworkClient defines the minimal network client interface needed by auth handlers
type NetworkClient interface {
	Database() DatabaseClient
}

// DatabaseClient defines the database query interface
type DatabaseClient interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*QueryResult, error)
}

// QueryResult represents a database query result
type QueryResult struct {
	Count int           `json:"count"`
	Rows  []interface{} `json:"rows"`
}

// ClusterProvisioner defines the interface for namespace cluster provisioning
type ClusterProvisioner interface {
	// CheckNamespaceCluster checks if a namespace has a cluster and returns its status
	// Returns: (clusterID, status, needsProvisioning, error)
	CheckNamespaceCluster(ctx context.Context, namespaceName string) (string, string, bool, error)
	// ProvisionNamespaceCluster triggers provisioning for a new namespace
	// Returns: (clusterID, pollURL, error)
	ProvisionNamespaceCluster(ctx context.Context, namespaceID int, namespaceName, wallet string) (string, string, error)
	// GetClusterStatusByID returns the full status of a cluster by ID
	// Returns a map[string]interface{} with cluster status fields
	GetClusterStatusByID(ctx context.Context, clusterID string) (interface{}, error)
}

// NodeRecoverer handles automatic recovery when nodes die or come back online,
// and manual cluster repair for under-provisioned clusters.
type NodeRecoverer interface {
	HandleDeadNode(ctx context.Context, deadNodeID string)
	HandleRecoveredNode(ctx context.Context, nodeID string)
	HandleSuspectNode(ctx context.Context, suspectNodeID string)
	HandleSuspectRecovery(ctx context.Context, nodeID string)
	RepairCluster(ctx context.Context, namespaceName string) error
}

// WebRTCManager handles enabling/disabling WebRTC services for namespaces.
type WebRTCManager interface {
	EnableWebRTC(ctx context.Context, namespaceName, enabledBy string) error
	DisableWebRTC(ctx context.Context, namespaceName string) error
	// GetWebRTCStatus returns the WebRTC config for a namespace, or nil if not enabled.
	GetWebRTCStatus(ctx context.Context, namespaceName string) (interface{}, error)
	// EnableWebRTCStealth / DisableWebRTCStealth toggle the censorship-
	// resistant TURNS:443 path (feat-124): stealth cert on the TURN servers,
	// stealth DNS records, and the turns:<stealth-host>:443 rung in the
	// turn.credentials URI ladder. Requires WebRTC to already be enabled.
	EnableWebRTCStealth(ctx context.Context, namespaceName string) error
	DisableWebRTCStealth(ctx context.Context, namespaceName string) error
}

// Handlers holds dependencies for authentication HTTP handlers
type Handlers struct {
	logger             *logging.ColoredLogger
	authService        *authsvc.Service
	netClient          NetworkClient
	defaultNS          string
	internalAuthFn     func(context.Context) context.Context
	clusterProvisioner ClusterProvisioner         // Optional: for namespace cluster provisioning
	solanaVerifier     *authsvc.SolanaNFTVerifier // Server-side NFT ownership verifier

	// apiKeyDB is the global/core API-key registry querier, wired via
	// SetAPIKeyDB (see gateway.Gateway.apiKeyDB). When set,
	// APIKeyToJWTHandler's self-query fallback uses it instead of
	// netClient.Database(); both ultimately resolve against the same
	// global registry, so either succeeding keeps main-gateway and
	// namespace-gateway validation in agreement. nil in most unit tests,
	// which then exercise the netClient fallback path directly.
	apiKeyDB DatabaseClient
}

// NewHandlers creates a new authentication handlers instance
func NewHandlers(
	logger *logging.ColoredLogger,
	authService *authsvc.Service,
	netClient NetworkClient,
	defaultNamespace string,
	internalAuthFn func(context.Context) context.Context,
) *Handlers {
	return &Handlers{
		logger:         logger,
		authService:    authService,
		netClient:      netClient,
		defaultNS:      defaultNamespace,
		internalAuthFn: internalAuthFn,
	}
}

// SetClusterProvisioner sets the cluster provisioner for namespace cluster management
func (h *Handlers) SetClusterProvisioner(cp ClusterProvisioner) {
	h.clusterProvisioner = cp
}

// SetSolanaVerifier sets the server-side NFT ownership verifier for Phantom auth
func (h *Handlers) SetSolanaVerifier(verifier *authsvc.SolanaNFTVerifier) {
	h.solanaVerifier = verifier
}

// SetAPIKeyDB wires the global/core API-key registry querier into this
// handler set. See the apiKeyDB field doc for why this exists.
func (h *Handlers) SetAPIKeyDB(db DatabaseClient) {
	h.apiKeyDB = db
}

// markNonceUsed marks a nonce as used in the database
func (h *Handlers) markNonceUsed(ctx context.Context, namespaceID interface{}, wallet, nonce string) {
	if h.netClient == nil {
		return
	}
	db := h.netClient.Database()
	internalCtx := h.internalAuthFn(ctx)
	_, _ = db.Query(internalCtx, "UPDATE nonces SET used_at = datetime('now') WHERE namespace_id = ? AND wallet = ? AND nonce = ?", namespaceID, wallet, nonce)
}

// resolveNamespace resolves namespace ID for nonce marking
func (h *Handlers) resolveNamespace(ctx context.Context, namespace string) (interface{}, error) {
	if h.authService == nil {
		return nil, sql.ErrNoRows
	}
	return h.authService.ResolveNamespaceID(ctx, namespace)
}
