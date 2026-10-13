package gateway

import (
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"go.uber.org/zap"
)

// ACMERequest represents the request body for ACME DNS-01 challenges
// from the lego httpreq provider
type ACMERequest struct {
	FQDN  string `json:"fqdn"`  // e.g., "_acme-challenge.example.com."
	Value string `json:"value"` // The challenge token
}

// acmePresentHandler handles DNS-01 challenge presentation
// POST /v1/internal/acme/present
// Creates a TXT record in the dns_records table for ACME validation
func (g *Gateway) acmePresentHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := g.acmeChallengeRequest(w, r)
	if !ok {
		return
	}
	fqdn := req.FQDN

	g.logger.Info("ACME DNS-01 challenge: presenting TXT record",
		zap.String("fqdn", fqdn),
	)

	// Insert TXT record into dns_records
	db := g.client.Database()
	ctx := client.WithInternalAuth(r.Context())

	// Insert new TXT record (multiple nodes may have concurrent challenges for the same FQDN)
	// ON CONFLICT DO NOTHING: the UNIQUE(fqdn, record_type, value) constraint prevents duplicates
	insertQuery := `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, is_active, created_at, updated_at, created_by)
		VALUES (?, 'TXT', ?, 60, 'acme', TRUE, datetime('now'), datetime('now'), 'system')
		ON CONFLICT(fqdn, record_type, value) DO NOTHING`

	_, err := db.Query(ctx, insertQuery, fqdn, req.Value)
	if err != nil {
		g.logger.Error("Failed to insert ACME TXT record", zap.Error(err))
		http.Error(w, "Failed to create DNS record", http.StatusInternalServerError)
		return
	}

	g.logger.Info("ACME TXT record created",
		zap.String("fqdn", fqdn),
	)

	// Give DNS a moment to propagate (CoreDNS reads from RQLite)
	time.Sleep(100 * time.Millisecond)

	w.WriteHeader(http.StatusOK)
}

// acmeCleanupHandler handles DNS-01 challenge cleanup
// POST /v1/internal/acme/cleanup
// Removes the TXT record after ACME validation completes
func (g *Gateway) acmeCleanupHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := g.acmeChallengeRequest(w, r)
	if !ok {
		return
	}
	fqdn := req.FQDN

	g.logger.Info("ACME DNS-01 challenge: cleaning up TXT record",
		zap.String("fqdn", fqdn),
	)

	// Delete TXT record from dns_records
	db := g.client.Database()
	ctx := client.WithInternalAuth(r.Context())

	// Only delete this node's specific challenge value, not all ACME TXT records for this FQDN
	deleteQuery := `DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'TXT' AND namespace = 'acme' AND value = ?`
	_, err := db.Query(ctx, deleteQuery, fqdn, req.Value)
	if err != nil {
		g.logger.Error("Failed to delete ACME TXT record", zap.Error(err))
		http.Error(w, "Failed to delete DNS record", http.StatusInternalServerError)
		return
	}

	g.logger.Info("ACME TXT record deleted",
		zap.String("fqdn", fqdn),
	)

	w.WriteHeader(http.StatusOK)
}
