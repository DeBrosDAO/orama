package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/rqlite/gorqlite"
)

// DatabaseClientImpl implements DatabaseClient
type DatabaseClientImpl struct {
	client     *Client
	connection *gorqlite.Connection
	mu         sync.RWMutex
}

// checkConnection verifies the client is connected
func (d *DatabaseClientImpl) checkConnection() error {
	if !d.client.isConnected() {
		return fmt.Errorf("client not connected")
	}
	return nil
}

// withRetry executes an operation with retry logic
func (d *DatabaseClientImpl) withRetry(operation func(*gorqlite.Connection) error) error {
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		conn, err := d.getRQLiteConnection()
		if err != nil {
			lastErr = err
			d.clearConnection()
			continue
		}

		if err := operation(conn); err != nil {
			lastErr = err
			d.clearConnection()
			continue
		}

		return nil
	}

	return fmt.Errorf("operation failed after %d attempts. Last error: %w", maxRetries, lastErr)
}

// Query executes a SQL query
func (d *DatabaseClientImpl) Query(ctx context.Context, sql string, args ...interface{}) (*QueryResult, error) {
	if err := d.checkConnection(); err != nil {
		return nil, err
	}

	if err := d.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	// Determine if this is a read or write operation
	isWriteOperation := d.isWriteOperation(sql)

	// Retry logic for resilient querying
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Get RQLite connection (tries multiple nodes)
		conn, err := d.getRQLiteConnection()
		if err != nil {
			lastErr = err

			// Clear any cached connection and try again
			d.clearConnection()
			continue
		}

		if isWriteOperation {
			// Execute write operation with parameters
			_, err := conn.WriteOneParameterized(gorqlite.ParameterizedStatement{
				Query:     sql,
				Arguments: args,
			})
			if err != nil {
				lastErr = err
				d.clearConnection()
				continue
			}

			// For write operations, return empty result set
			return &QueryResult{
				Columns: []string{"affected"},
				Rows:    [][]interface{}{{"success"}},
				Count:   1,
			}, nil
		} else {
			// Execute read operation with parameters
			result, err := conn.QueryOneParameterized(gorqlite.ParameterizedStatement{
				Query:     sql,
				Arguments: args,
			})
			if err != nil {
				lastErr = err
				d.clearConnection()
				continue
			}

			// Convert gorqlite.QueryResult to our QueryResult
			columns := result.Columns()
			numRows := int(result.NumRows())

			queryResult := &QueryResult{
				Columns: columns,
				Rows:    make([][]interface{}, 0, numRows),
				Count:   result.NumRows(),
			}

			// Iterate through rows
			for result.Next() {
				row, err := result.Slice()
				if err != nil {
					continue
				}
				queryResult.Rows = append(queryResult.Rows, row)
			}

			return queryResult, nil
		}
	}

	return nil, fmt.Errorf("query failed after %d attempts. Last error: %w", maxRetries, lastErr)
}

// isWriteOperation determines if a SQL statement is a write operation
func (d *DatabaseClientImpl) isWriteOperation(sql string) bool {
	// Convert to uppercase for comparison
	sqlUpper := strings.ToUpper(strings.TrimSpace(sql))

	// List of write operation keywords
	writeKeywords := []string{
		"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER",
		"TRUNCATE", "REPLACE", "MERGE", "PRAGMA",
	}

	for _, keyword := range writeKeywords {
		if strings.HasPrefix(sqlUpper, keyword) {
			return true
		}
	}

	return false
}

// clearConnection clears the cached connection to force reconnection
func (d *DatabaseClientImpl) clearConnection() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.connection != nil {
		d.connection.Close()
		d.connection = nil
	}
}

// getRQLiteConnection returns a connection to RQLite, creating one if needed
func (d *DatabaseClientImpl) getRQLiteConnection() (*gorqlite.Connection, error) {
	d.mu.RLock()
	conn := d.connection
	d.mu.RUnlock()

	if conn != nil {
		return conn, nil
	}

	newConn, err := d.connectToAvailableNode()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.connection = newConn
	d.mu.Unlock()
	return newConn, nil
}

// getRQLiteNodes returns a list of RQLite node URLs with precedence:
// 1) client config DatabaseEndpoints
// 2) RQLITE_NODES env (comma/space separated)
// 3) library defaults via DefaultDatabaseEndpoints()
func (d *DatabaseClientImpl) getRQLiteNodes() []string {
	// 1) Prefer explicit configuration on the client
	if d.client != nil && d.client.config != nil && len(d.client.config.DatabaseEndpoints) > 0 {
		return dedupeStrings(normalizeEndpoints(d.client.config.DatabaseEndpoints))
	}

	// 3) Fallback to library defaults derived from bootstrap peers
	return DefaultDatabaseEndpoints()
}

// normalizeEndpoints is now imported from defaults.go

func hasPort(hostport string) bool {
	// cheap check for :port suffix (IPv6 with brackets handled by url.Parse earlier)
	if i := strings.LastIndex(hostport, ":"); i > -1 && i < len(hostport)-1 {
		// ensure the segment after ':' is numeric-ish
		for _, c := range hostport[i+1:] {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// connectToAvailableNode tries to connect to any available RQLite node
func (d *DatabaseClientImpl) connectToAvailableNode() (*gorqlite.Connection, error) {
	// Get RQLite nodes from environment or use defaults
	rqliteNodes := d.getRQLiteNodes()

	var lastErr error

	for _, rqliteURL := range rqliteNodes {
		var conn *gorqlite.Connection
		var err error

		conn, err = gorqlite.Open(rqliteURL)
		if err != nil {
			lastErr = err
			continue
		}

		// Test the connection with a simple query to ensure it's working
		// and the node has leadership or can serve reads
		if err := d.testConnection(conn); err != nil {
			lastErr = err
			continue
		}

		return conn, nil
	}

	return nil, fmt.Errorf("failed to connect to any RQLite instance. Last error: %w", lastErr)
}

// testConnection performs a health check on the RQLite connection
func (d *DatabaseClientImpl) testConnection(conn *gorqlite.Connection) error {
	// Try a simple read query first (works even without leadership)
	result, err := conn.QueryOne("SELECT 1")
	if err != nil {
		return fmt.Errorf("read test failed: %w", err)
	}

	// Check if we got a valid result
	if result.NumRows() == 0 {
		return fmt.Errorf("read test returned no rows")
	}

	return nil
}

// Transaction executes multiple queries in a transaction
func (d *DatabaseClientImpl) Transaction(ctx context.Context, queries []string) error {
	if !d.client.isConnected() {
		return fmt.Errorf("client not connected")
	}

	if err := d.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Get RQLite connection
		conn, err := d.getRQLiteConnection()
		if err != nil {
			lastErr = err
			d.clearConnection()
			continue
		}

		// Execute all queries in the transaction
		success := true
		for _, query := range queries {
			_, err := conn.WriteOne(query)
			if err != nil {
				lastErr = err
				success = false
				d.clearConnection()
				break
			}
		}

		if success {
			return nil
		}
	}

	return fmt.Errorf("transaction failed after %d attempts. Last error: %w", maxRetries, lastErr)
}

// CreateTable creates a new table
func (d *DatabaseClientImpl) CreateTable(ctx context.Context, schema string) error {
	if err := d.checkConnection(); err != nil {
		return err
	}

	if err := d.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	return d.withRetry(func(conn *gorqlite.Connection) error {
		_, err := conn.WriteOne(schema)
		return err
	})
}

// DropTable drops a table
func (d *DatabaseClientImpl) DropTable(ctx context.Context, tableName string) error {
	if err := d.checkConnection(); err != nil {
		return err
	}

	return d.withRetry(func(conn *gorqlite.Connection) error {
		dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
		_, err := conn.WriteOne(dropSQL)
		return err
	})
}

// GetSchema returns schema information
func (d *DatabaseClientImpl) GetSchema(ctx context.Context) (*SchemaInfo, error) {
	if !d.client.isConnected() {
		return nil, fmt.Errorf("client not connected")
	}

	if err := d.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	// Get RQLite connection
	conn, err := d.getRQLiteConnection()
	if err != nil {
		return nil, fmt.Errorf("failed to get RQLite connection: %w", err)
	}

	// Query for all tables
	result, err := conn.QueryOne("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to query table list: %w", err)
	}

	schema := &SchemaInfo{
		Tables: make([]TableInfo, 0),
	}

	// Iterate through tables
	for result.Next() {
		row, err := result.Slice()
		if err != nil {
			return nil, fmt.Errorf("failed to get table row: %w", err)
		}

		if len(row) > 0 {
			tableName := fmt.Sprintf("%v", row[0])

			// Get column information for this table
			columnResult, err := conn.QueryOne(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
			if err != nil {
				continue // Skip this table if we can't get column info
			}

			tableInfo := TableInfo{
				Name:    tableName,
				Columns: make([]ColumnInfo, 0),
			}

			// Parse column information
			for columnResult.Next() {
				colRow, err := columnResult.Slice()
				if err != nil {
					continue
				}

				if len(colRow) >= 6 {
					columnInfo := ColumnInfo{
						Name:     fmt.Sprintf("%v", colRow[1]),        // name
						Type:     fmt.Sprintf("%v", colRow[2]),        // type
						Nullable: fmt.Sprintf("%v", colRow[3]) == "0", // notnull (0 = nullable, 1 = not null)
					}
					tableInfo.Columns = append(tableInfo.Columns, columnInfo)
				}
			}

			schema.Tables = append(schema.Tables, tableInfo)
		}
	}

	return schema, nil
}

// NetworkInfoImpl implements NetworkInfo
type NetworkInfoImpl struct {
	client *Client
}

// GetPeers returns information about connected peers
func (n *NetworkInfoImpl) GetPeers(ctx context.Context) ([]PeerInfo, error) {
	if !n.client.isConnected() {
		return nil, fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	// Get peers from LibP2P host
	host := n.client.host
	if host == nil {
		return nil, fmt.Errorf("no host available")
	}

	// Get connected peers
	connectedPeers := host.Network().Peers()
	peers := make([]PeerInfo, 0, len(connectedPeers)+1) // +1 for self

	// Add connected peers
	for _, peerID := range connectedPeers {
		// Get peer addresses
		peerInfo := host.Peerstore().PeerInfo(peerID)

		// Convert multiaddrs to strings
		addrs := make([]string, len(peerInfo.Addrs))
		for i, addr := range peerInfo.Addrs {
			addrs[i] = addr.String()
		}

		peers = append(peers, PeerInfo{
			ID:        peerID.String(),
			Addresses: addrs,
			Connected: true,
			LastSeen:  time.Now(), // LibP2P doesn't track last seen, so use current time
		})
	}

	// Add self node
	selfPeerInfo := host.Peerstore().PeerInfo(host.ID())
	selfAddrs := make([]string, len(selfPeerInfo.Addrs))
	for i, addr := range selfPeerInfo.Addrs {
		selfAddrs[i] = addr.String()
	}

	// Insert self node at the beginning of the list
	selfPeer := PeerInfo{
		ID:        host.ID().String(),
		Addresses: selfAddrs,
		Connected: true,
		LastSeen:  time.Now(),
	}

	// Prepend self to the list
	peers = append([]PeerInfo{selfPeer}, peers...)

	return peers, nil
}

// GetStatus returns network status
func (n *NetworkInfoImpl) GetStatus(ctx context.Context) (*NetworkStatus, error) {
	if !n.client.isConnected() {
		return nil, fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return nil, fmt.Errorf("no host available")
	}

	// Get actual network status
	connectedPeers := host.Network().Peers()

	// Try to get database size from RQLite (optional - don't fail if unavailable)
	var dbSize int64 = 0
	dbClient := n.client.database
	if conn, err := dbClient.getRQLiteConnection(); err == nil {
		// Query database size (rough estimate)
		if result, err := conn.QueryOne("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()"); err == nil {
			for result.Next() {
				if row, err := result.Slice(); err == nil && len(row) > 0 {
					if size, ok := row[0].(int64); ok {
						dbSize = size
					}
				}
			}
		}
	}

	return &NetworkStatus{
		NodeID:       host.ID().String(),
		Connected:    true,
		PeerCount:    len(connectedPeers),
		DatabaseSize: dbSize,
		Uptime:       time.Since(n.client.startTime),
	}, nil
}

// ConnectToPeer connects to a specific peer
func (n *NetworkInfoImpl) ConnectToPeer(ctx context.Context, peerAddr string) error {
	if !n.client.isConnected() {
		return fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return fmt.Errorf("no host available")
	}

	// Parse the multiaddr
	ma, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return fmt.Errorf("failed to extract peer info: %w", err)
	}

	// Connect to the peer
	if err := host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	return nil
}

// DisconnectFromPeer disconnects from a specific peer
func (n *NetworkInfoImpl) DisconnectFromPeer(ctx context.Context, peerID string) error {
	if !n.client.isConnected() {
		return fmt.Errorf("client not connected")
	}

	if err := n.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}

	host := n.client.host
	if host == nil {
		return fmt.Errorf("no host available")
	}

	// Parse the peer ID
	pid, err := peer.Decode(peerID)
	if err != nil {
		return fmt.Errorf("invalid peer ID: %w", err)
	}

	// Close the connection to the peer
	if err := host.Network().ClosePeer(pid); err != nil {
		return fmt.Errorf("failed to disconnect from peer: %w", err)
	}

	return nil
}
