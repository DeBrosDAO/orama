package deployments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// DomainHandler handles custom domain management
type DomainHandler struct {
	service *DeploymentService
	logger  *zap.Logger
}

// NewDomainHandler creates a new domain handler
func NewDomainHandler(service *DeploymentService, logger *zap.Logger) *DomainHandler {
	return &DomainHandler{
		service: service,
		logger:  logger,
	}
}

// HandleAddDomain adds a custom domain to a deployment
func (h *DomainHandler) HandleAddDomain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var req struct {
		DeploymentName string `json:"deployment_name"`
		Domain         string `json:"domain"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DeploymentName == "" || req.Domain == "" {
		http.Error(w, "deployment_name and domain are required", http.StatusBadRequest)
		return
	}

	// Normalize domain
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimSuffix(domain, "/")

	// Validate domain format
	if !isValidDomain(domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	// Check if domain is reserved (using configured base domain)
	baseDomain := h.service.BaseDomain()
	if strings.HasSuffix(domain, "."+baseDomain) {
		http.Error(w, fmt.Sprintf("Cannot use .%s domains as custom domains", baseDomain), http.StatusBadRequest)
		return
	}

	h.logger.Info("Adding custom domain",
		zap.String("namespace", namespace),
		zap.String("deployment", req.DeploymentName),
		zap.String("domain", domain),
	)

	// Get deployment
	deployment, err := h.service.GetDeployment(ctx, namespace, req.DeploymentName)
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	// Generate verification token
	token := generateVerificationToken()

	// Check if domain already exists
	var existingCount int
	checkQuery := `SELECT COUNT(*) FROM deployment_domains WHERE domain = ?`
	var counts []struct {
		Count int `db:"count"`
	}
	err = h.service.db.Query(ctx, &counts, checkQuery, domain)
	if err == nil && len(counts) > 0 {
		existingCount = counts[0].Count
	}

	if existingCount > 0 {
		http.Error(w, "Domain already in use", http.StatusConflict)
		return
	}

	// Insert domain record
	query := `
		INSERT INTO deployment_domains (deployment_id, domain, verification_token, verification_status, created_at)
		VALUES (?, ?, ?, 'pending', ?)
	`

	_, err = h.service.db.Exec(ctx, query, deployment.ID, domain, token, time.Now())
	if err != nil {
		h.logger.Error("Failed to insert domain", zap.Error(err))
		http.Error(w, "Failed to add domain", http.StatusInternalServerError)
		return
	}

	h.logger.Info("Custom domain added, awaiting verification",
		zap.String("domain", domain),
		zap.String("deployment", deployment.Name),
	)

	// Return verification instructions
	resp := map[string]interface{}{
		"deployment_name":    deployment.Name,
		"domain":             domain,
		"verification_token": token,
		"status":             "pending",
		"instructions": map[string]string{
			"step_1": "Add a TXT record to your DNS:",
			"record": fmt.Sprintf("_orama-verify.%s", domain),
			"value":  token,
			"step_2": "Once added, call POST /v1/deployments/domains/verify with the domain",
			"step_3": "After verification, point your domain's A record to your deployment's node IP",
		},
		"created_at": time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// HandleVerifyDomain verifies domain ownership via TXT record
func (h *DomainHandler) HandleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var req struct {
		Domain string `json:"domain"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	domain := strings.ToLower(strings.TrimSpace(req.Domain))

	h.logger.Info("Verifying domain",
		zap.String("namespace", namespace),
		zap.String("domain", domain),
	)

	// Get domain record
	type domainRow struct {
		DeploymentID       string `db:"deployment_id"`
		VerificationToken  string `db:"verification_token"`
		VerificationStatus string `db:"verification_status"`
	}

	var rows []domainRow
	query := `
		SELECT dd.deployment_id, dd.verification_token, dd.verification_status
		FROM deployment_domains dd
		JOIN deployments d ON dd.deployment_id = d.id
		WHERE dd.domain = ? AND d.namespace = ?
	`

	err := h.service.db.Query(ctx, &rows, query, domain, namespace)
	if err != nil || len(rows) == 0 {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}

	domainRecord := rows[0]

	if domainRecord.VerificationStatus == "verified" {
		resp := map[string]interface{}{
			"domain":  domain,
			"status":  "verified",
			"message": "Domain already verified",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Verify TXT record
	txtRecord := fmt.Sprintf("_orama-verify.%s", domain)
	verified := h.verifyTXTRecord(txtRecord, domainRecord.VerificationToken)

	if !verified {
		http.Error(w, "Verification failed: TXT record not found or doesn't match", http.StatusBadRequest)
		return
	}

	// Update status
	updateQuery := `
		UPDATE deployment_domains
		SET verification_status = 'verified', verified_at = ?
		WHERE domain = ?
	`

	_, err = h.service.db.Exec(ctx, updateQuery, time.Now(), domain)
	if err != nil {
		h.logger.Error("Failed to update verification status", zap.Error(err))
		http.Error(w, "Failed to update verification status", http.StatusInternalServerError)
		return
	}

	// Create DNS record for the domain
	go h.createDNSRecord(ctx, domain, domainRecord.DeploymentID)

	h.logger.Info("Domain verified successfully",
		zap.String("domain", domain),
	)

	resp := map[string]interface{}{
		"domain":      domain,
		"status":      "verified",
		"message":     "Domain verified successfully",
		"verified_at": time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleListDomains lists all domains for a deployment
func (h *DomainHandler) HandleListDomains(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}
	deploymentName := r.URL.Query().Get("deployment_name")

	if deploymentName == "" {
		http.Error(w, "deployment_name query parameter is required", http.StatusBadRequest)
		return
	}

	// Get deployment
	deployment, err := h.service.GetDeployment(ctx, namespace, deploymentName)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Query domains
	type domainRow struct {
		Domain             string     `db:"domain"`
		VerificationStatus string     `db:"verification_status"`
		CreatedAt          time.Time  `db:"created_at"`
		VerifiedAt         *time.Time `db:"verified_at"`
	}

	var rows []domainRow
	query := `
		SELECT domain, verification_status, created_at, verified_at
		FROM deployment_domains
		WHERE deployment_id = ?
		ORDER BY created_at DESC
	`

	err = h.service.db.Query(ctx, &rows, query, deployment.ID)
	if err != nil {
		h.logger.Error("Failed to query domains", zap.Error(err))
		http.Error(w, "Failed to query domains", http.StatusInternalServerError)
		return
	}

	domains := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		domains[i] = map[string]interface{}{
			"domain":              row.Domain,
			"verification_status": row.VerificationStatus,
			"created_at":          row.CreatedAt,
		}
		if row.VerifiedAt != nil {
			domains[i]["verified_at"] = row.VerifiedAt
		}
	}

	resp := map[string]interface{}{
		"deployment_name": deploymentName,
		"domains":         domains,
		"total":           len(domains),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleRemoveDomain removes a custom domain
func (h *DomainHandler) HandleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}
	domain := r.URL.Query().Get("domain")

	if domain == "" {
		http.Error(w, "domain query parameter is required", http.StatusBadRequest)
		return
	}

	domain = strings.ToLower(strings.TrimSpace(domain))

	h.logger.Info("Removing domain",
		zap.String("namespace", namespace),
		zap.String("domain", domain),
	)

	// Verify ownership
	var deploymentID string
	checkQuery := `
		SELECT dd.deployment_id
		FROM deployment_domains dd
		JOIN deployments d ON dd.deployment_id = d.id
		WHERE dd.domain = ? AND d.namespace = ?
	`

	type idRow struct {
		DeploymentID string `db:"deployment_id"`
	}
	var rows []idRow
	err := h.service.db.Query(ctx, &rows, checkQuery, domain, namespace)
	if err != nil || len(rows) == 0 {
		http.Error(w, "Domain not found", http.StatusNotFound)
		return
	}
	deploymentID = rows[0].DeploymentID

	// Delete domain
	deleteQuery := `DELETE FROM deployment_domains WHERE domain = ?`
	_, err = h.service.db.Exec(ctx, deleteQuery, domain)
	if err != nil {
		h.logger.Error("Failed to delete domain", zap.Error(err))
		http.Error(w, "Failed to delete domain", http.StatusInternalServerError)
		return
	}

	// Delete DNS record
	dnsQuery := `DELETE FROM dns_records WHERE fqdn = ? AND deployment_id = ?`
	h.service.db.Exec(ctx, dnsQuery, domain+".", deploymentID)

	h.logger.Info("Domain removed",
		zap.String("domain", domain),
	)

	resp := map[string]interface{}{
		"message": "Domain removed successfully",
		"domain":  domain,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Helper functions

func generateVerificationToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "orama-verify-" + hex.EncodeToString(bytes)
}

func isValidDomain(domain string) bool {
	// Basic domain validation
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	if strings.Contains(domain, "..") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return false
	}
	return true
}

func (h *DomainHandler) verifyTXTRecord(record, expectedValue string) bool {
	txtRecords, err := net.LookupTXT(record)
	if err != nil {
		h.logger.Warn("Failed to lookup TXT record",
			zap.String("record", record),
			zap.Error(err),
		)
		return false
	}

	for _, txt := range txtRecords {
		if txt == expectedValue {
			return true
		}
	}

	return false
}

func (h *DomainHandler) createDNSRecord(ctx context.Context, domain, deploymentID string) {
	// Get deployment node IP
	type deploymentRow struct {
		HomeNodeID string `db:"home_node_id"`
	}

	var rows []deploymentRow
	query := `SELECT home_node_id FROM deployments WHERE id = ?`
	err := h.service.db.Query(ctx, &rows, query, deploymentID)
	if err != nil || len(rows) == 0 {
		h.logger.Error("Failed to get deployment node", zap.Error(err))
		return
	}

	homeNodeID := rows[0].HomeNodeID

	// Get node IP
	type nodeRow struct {
		IPAddress string `db:"ip_address"`
	}

	var nodeRows []nodeRow
	nodeQuery := `SELECT ip_address FROM dns_nodes WHERE id = ? AND status = 'active'`
	err = h.service.db.Query(ctx, &nodeRows, nodeQuery, homeNodeID)
	if err != nil || len(nodeRows) == 0 {
		h.logger.Error("Failed to get node IP", zap.Error(err))
		return
	}

	nodeIP := nodeRows[0].IPAddress

	// Create DNS A record
	dnsQuery := `
		INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, deployment_id, node_id, created_by, is_active, created_at, updated_at)
		VALUES (?, 'A', ?, 300, ?, ?, ?, 'system', TRUE, ?, ?)
		ON CONFLICT(fqdn, record_type, value) DO UPDATE SET
			deployment_id = excluded.deployment_id,
			node_id = excluded.node_id,
			is_active = TRUE,
			updated_at = excluded.updated_at
	`

	fqdn := domain + "."
	now := time.Now()

	_, err = h.service.db.Exec(ctx, dnsQuery, fqdn, nodeIP, "", deploymentID, homeNodeID, now, now)
	if err != nil {
		h.logger.Error("Failed to create DNS record", zap.Error(err))
		return
	}

	h.logger.Info("DNS record created for custom domain",
		zap.String("domain", domain),
		zap.String("ip", nodeIP),
	)
}
