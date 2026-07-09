// Package vault provides HTTP handlers for vault proxy operations.
//
// The gateway acts as a smart proxy between RootWallet clients and
// vault guardian nodes on the WireGuard overlay network. It handles
// Shamir split/combine so clients make a single HTTPS call.
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const (
	// VaultGuardianPort is the port vault guardians listen on (client API).
	VaultGuardianPort = 7500

	// guardianTimeout is the per-guardian HTTP request timeout.
	guardianTimeout = 5 * time.Second

	// overallTimeout is the maximum time for the full fan-out operation.
	overallTimeout = 15 * time.Second

	// maxPushBodySize limits push request bodies (1 MiB).
	maxPushBodySize = 1 << 20

	// maxPullBodySize limits pull request bodies (4 KiB).
	maxPullBodySize = 4 << 10
)

// Handlers provides HTTP handlers for vault proxy operations.
type Handlers struct {
	logger        *logging.ColoredLogger
	dbClient      client.NetworkClient
	rateLimiter   *IdentityRateLimiter
	ipRateLimiter *IPRateLimiter
	httpClient    *http.Client
}

// NewHandlers creates vault proxy handlers.
func NewHandlers(logger *logging.ColoredLogger, dbClient client.NetworkClient) *Handlers {
	h := &Handlers{
		logger:   logger,
		dbClient: dbClient,
		rateLimiter: NewIdentityRateLimiter(
			30,  // 30 pushes per hour per identity
			120, // 120 pulls per hour per identity
		),
		// Per-IP limiter: bounds online password/seed guessing that iterates
		// many identities from one source (see ratelimit.go for the rationale).
		ipRateLimiter: NewIPRateLimiter(),
		httpClient: &http.Client{
			Timeout: guardianTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	h.rateLimiter.StartCleanup(10*time.Minute, 1*time.Hour)
	h.ipRateLimiter.StartCleanup(ipCleanupInterval, ipBucketMaxAge)
	return h
}

// guardian represents a reachable vault guardian node.
type guardian struct {
	IP   string
	Port int
}

// discoverGuardians queries dns_nodes for all active nodes.
// Every Orama node runs a vault guardian, so every active node is a guardian.
func (h *Handlers) discoverGuardians(ctx context.Context) ([]guardian, error) {
	db := h.dbClient.Database()
	internalCtx := client.WithInternalAuth(ctx)

	query := "SELECT COALESCE(internal_ip, ip_address) FROM dns_nodes WHERE status = 'active'"
	result, err := db.Query(internalCtx, query)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to query guardian nodes: %w", err)
	}
	if result == nil || len(result.Rows) == 0 {
		return nil, fmt.Errorf("vault: no active guardian nodes found")
	}

	guardians := make([]guardian, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) == 0 {
			continue
		}
		ip := getString(row[0])
		if ip == "" {
			continue
		}
		guardians = append(guardians, guardian{IP: ip, Port: VaultGuardianPort})
	}
	if len(guardians) == 0 {
		return nil, fmt.Errorf("vault: no guardian nodes with valid IPs found")
	}
	return guardians, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func getString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// isValidIdentity checks that identity is exactly 64 hex characters.
func isValidIdentity(identity string) bool {
	if len(identity) != 64 {
		return false
	}
	for _, c := range identity {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
