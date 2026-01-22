package rqlite

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// DNSRecord represents a DNS record from RQLite
type DNSRecord struct {
	FQDN        string
	Type        uint16
	Value       string
	TTL         int
	ParsedValue interface{} // Parsed IP or string value
}

// Backend handles RQLite connections and queries
type Backend struct {
	dsn         string
	client      *RQLiteClient
	logger      *zap.Logger
	refreshRate time.Duration
	mu          sync.RWMutex
	healthy     bool
}

// NewBackend creates a new RQLite backend
func NewBackend(dsn string, refreshRate time.Duration, logger *zap.Logger) (*Backend, error) {
	client, err := NewRQLiteClient(dsn, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create RQLite client: %w", err)
	}

	b := &Backend{
		dsn:         dsn,
		client:      client,
		logger:      logger,
		refreshRate: refreshRate,
		healthy:     false,
	}

	// Test connection
	if err := b.ping(); err != nil {
		return nil, fmt.Errorf("failed to ping RQLite: %w", err)
	}

	b.healthy = true

	// Start health check goroutine
	go b.healthCheck()

	return b, nil
}

// Query retrieves DNS records from RQLite
func (b *Backend) Query(ctx context.Context, fqdn string, qtype uint16) ([]*DNSRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Normalize FQDN
	fqdn = dns.Fqdn(strings.ToLower(fqdn))

	// Map DNS query type to string
	recordType := qTypeToString(qtype)

	// Query active records matching FQDN and type
	query := `
		SELECT fqdn, record_type, value, ttl
		FROM dns_records
		WHERE fqdn = ? AND record_type = ? AND is_active = TRUE
	`

	rows, err := b.client.Query(ctx, query, fqdn, recordType)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	records := make([]*DNSRecord, 0)
	for _, row := range rows {
		if len(row) < 4 {
			continue
		}

		fqdnVal, _ := row[0].(string)
		typeVal, _ := row[1].(string)
		valueVal, _ := row[2].(string)
		ttlVal, _ := row[3].(float64)

		// Parse the value based on record type
		parsedValue, err := b.parseValue(typeVal, valueVal)
		if err != nil {
			b.logger.Warn("Failed to parse record value",
				zap.String("fqdn", fqdnVal),
				zap.String("type", typeVal),
				zap.String("value", valueVal),
				zap.Error(err),
			)
			continue
		}

		record := &DNSRecord{
			FQDN:        fqdnVal,
			Type:        stringToQType(typeVal),
			Value:       valueVal,
			TTL:         int(ttlVal),
			ParsedValue: parsedValue,
		}

		records = append(records, record)
	}

	return records, nil
}

// parseValue parses a DNS record value based on its type
func (b *Backend) parseValue(recordType, value string) (interface{}, error) {
	switch strings.ToUpper(recordType) {
	case "A":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 address: %s", value)
		}
		return &dns.A{A: ip.To4()}, nil

	case "AAAA":
		ip := net.ParseIP(value)
		if ip == nil || ip.To16() == nil {
			return nil, fmt.Errorf("invalid IPv6 address: %s", value)
		}
		return &dns.AAAA{AAAA: ip.To16()}, nil

	case "CNAME":
		return dns.Fqdn(value), nil

	case "TXT":
		return []string{value}, nil

	default:
		return nil, fmt.Errorf("unsupported record type: %s", recordType)
	}
}

// ping tests the RQLite connection
func (b *Backend) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := "SELECT 1"
	_, err := b.client.Query(ctx, query)
	return err
}

// healthCheck periodically checks RQLite health
func (b *Backend) healthCheck() {
	ticker := time.NewTicker(b.refreshRate)
	defer ticker.Stop()

	for range ticker.C {
		if err := b.ping(); err != nil {
			b.mu.Lock()
			b.healthy = false
			b.mu.Unlock()

			b.logger.Error("Health check failed", zap.Error(err))
		} else {
			b.mu.Lock()
			wasUnhealthy := !b.healthy
			b.healthy = true
			b.mu.Unlock()

			if wasUnhealthy {
				b.logger.Info("Health check recovered")
			}
		}
	}
}

// Healthy returns the current health status
func (b *Backend) Healthy() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.healthy
}

// Close closes the backend connection
func (b *Backend) Close() error {
	return b.client.Close()
}

// qTypeToString converts DNS query type to string
func qTypeToString(qtype uint16) string {
	switch qtype {
	case dns.TypeA:
		return "A"
	case dns.TypeAAAA:
		return "AAAA"
	case dns.TypeCNAME:
		return "CNAME"
	case dns.TypeTXT:
		return "TXT"
	default:
		return dns.TypeToString[qtype]
	}
}

// stringToQType converts string to DNS query type
func stringToQType(s string) uint16 {
	switch strings.ToUpper(s) {
	case "A":
		return dns.TypeA
	case "AAAA":
		return dns.TypeAAAA
	case "CNAME":
		return dns.TypeCNAME
	case "TXT":
		return dns.TypeTXT
	default:
		return 0
	}
}
