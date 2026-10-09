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

// refreshTimeout bounds one rebuild of the ancestor set.
const refreshTimeout = 30 * time.Second

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
	below       ancestorSet
}

// NewBackend creates a new RQLite backend.
// Optional username/password enable HTTP basic auth for RQLite connections.
func NewBackend(dsn string, refreshRate time.Duration, logger *zap.Logger, username, password string) (*Backend, error) {
	client, err := NewRQLiteClient(dsn, logger, username, password)
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
	if err := b.refreshAncestors(context.Background()); err != nil {
		return nil, err
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
		if record := b.recordFromRow(row); record != nil {
			records = append(records, record)
			b.below.learn(strings.ToLower(record.FQDN))
		}
	}

	return records, nil
}

// recordFromRow is the record a dns_records row (fqdn, record_type, value,
// ttl) holds, or nil for a row that is short or whose value does not parse.
func (b *Backend) recordFromRow(row []interface{}) *DNSRecord {
	if len(row) < 4 {
		return nil
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
		return nil
	}

	return &DNSRecord{
		FQDN:        fqdnVal,
		Type:        stringToQType(typeVal),
		Value:       valueVal,
		TTL:         int(ttlVal),
		ParsedValue: parsedValue,
	}
}

// Ownership is what a set of names owns in the zone, as Owners reads it.
type Ownership struct {
	// Records are the active records of the type asked that each name holds
	// (keyed by lower-cased name).
	Records map[string][]*DNSRecord
	// Owned are the names that hold an active record of any type.
	Owned map[string]bool
}

// Owners reads, in ONE indexed query, what each of fqdns owns. A name that owns
// a record of another type is NODATA for qtype, not NXDOMAIN, and is not
// answered from a wildcard (RFC 4592). One query for the whole list keeps the
// cost of a miss constant however many wildcard candidates the name has.
func (b *Backend) Owners(ctx context.Context, fqdns []string, qtype uint16) (Ownership, error) {
	own := Ownership{Records: map[string][]*DNSRecord{}, Owned: map[string]bool{}}
	if len(fqdns) == 0 {
		return own, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	args := make([]interface{}, len(fqdns))
	for i, fqdn := range fqdns {
		args[i] = dns.Fqdn(strings.ToLower(fqdn))
	}
	query := `SELECT fqdn, record_type, value, ttl FROM dns_records WHERE fqdn IN (?` +
		strings.Repeat(",?", len(fqdns)-1) + `) AND is_active = TRUE`
	rows, err := b.client.Query(ctx, query, args...)
	if err != nil {
		return Ownership{}, fmt.Errorf("query failed: %w", err)
	}

	wantType := qTypeToString(qtype)
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		fqdn, _ := row[0].(string)
		typeVal, _ := row[1].(string)
		fqdn = strings.ToLower(fqdn)
		own.Owned[fqdn] = true
		b.below.learn(fqdn)
		if typeVal == wantType {
			if record := b.recordFromRow(row); record != nil {
				own.Records[fqdn] = append(own.Records[fqdn], record)
			}
		}
	}
	return own, nil
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

	case "NS":
		return dns.Fqdn(value), nil

	case "SOA":
		// SOA format: "mname rname serial refresh retry expire minimum"
		// Example: "ns1.example.com. admin.example.com. 2026012401 3600 1800 604800 300"
		return b.parseSOA(value)

	default:
		return nil, fmt.Errorf("unsupported record type: %s", recordType)
	}
}

// parseSOA parses a SOA record value string
// Format: "mname rname serial refresh retry expire minimum"
func (b *Backend) parseSOA(value string) (*dns.SOA, error) {
	parts := strings.Fields(value)
	if len(parts) < 7 {
		return nil, fmt.Errorf("invalid SOA format, expected 7 fields: %s", value)
	}

	serial, err := parseUint32(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid SOA serial: %w", err)
	}
	refresh, err := parseUint32(parts[3])
	if err != nil {
		return nil, fmt.Errorf("invalid SOA refresh: %w", err)
	}
	retry, err := parseUint32(parts[4])
	if err != nil {
		return nil, fmt.Errorf("invalid SOA retry: %w", err)
	}
	expire, err := parseUint32(parts[5])
	if err != nil {
		return nil, fmt.Errorf("invalid SOA expire: %w", err)
	}
	minttl, err := parseUint32(parts[6])
	if err != nil {
		return nil, fmt.Errorf("invalid SOA minimum: %w", err)
	}

	return &dns.SOA{
		Ns:      dns.Fqdn(parts[0]),
		Mbox:    dns.Fqdn(parts[1]),
		Serial:  serial,
		Refresh: refresh,
		Retry:   retry,
		Expire:  expire,
		Minttl:  minttl,
	}, nil
}

// parseUint32 parses a string to uint32
func parseUint32(s string) (uint32, error) {
	var val uint32
	_, err := fmt.Sscanf(s, "%d", &val)
	return val, err
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
		err := b.ping()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
			err = b.refreshAncestors(ctx)
			cancel()
		}
		if err != nil {
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
	case dns.TypeNS:
		return "NS"
	case dns.TypeSOA:
		return "SOA"
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
	case "NS":
		return dns.TypeNS
	case "SOA":
		return dns.TypeSOA
	default:
		return 0
	}
}
