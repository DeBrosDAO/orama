package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// Protocol ID for peer exchange
const PeerExchangeProtocol = "/orama/peer-exchange/1.0.0"

// libp2pPort is the standard port used for libp2p peer connections.
// Filtering on this port prevents cross-connecting with IPFS (4101) or IPFS Cluster (9096/9098).
const libp2pPort = 4001

// filterLibp2pAddrs returns only multiaddrs with TCP port 4001 (standard libp2p port).
func filterLibp2pAddrs(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
	filtered := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		port, err := addr.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum != libp2pPort {
			continue
		}
		filtered = append(filtered, addr)
	}
	return filtered
}

// hasLibp2pAddr returns true if any of the peer's addresses use the standard libp2p port.
func hasLibp2pAddr(addrs []multiaddr.Multiaddr) bool {
	for _, addr := range addrs {
		port, err := addr.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		portNum, err := strconv.Atoi(port)
		if err == nil && portNum == libp2pPort {
			return true
		}
	}
	return false
}

// PeerExchangeRequest represents a request for peer information
type PeerExchangeRequest struct {
	Limit int `json:"limit"`
}

// PeerExchangeResponse represents a list of peers to exchange
type PeerExchangeResponse struct {
	Peers          []PeerInfo          `json:"peers"`
	RQLiteMetadata *RQLiteNodeMetadata `json:"rqlite_metadata,omitempty"`
}

// PeerInfo contains peer identity and addresses
type PeerInfo struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

// Manager handles peer discovery operations without a DHT dependency.
// Note: The constructor intentionally accepts a second parameter of type
// interface{} to remain source-compatible with previous call sites that
// passed a DHT instance. The value is ignored.
type Manager struct {
	host   host.Host
	logger *zap.Logger
	cancel context.CancelFunc

	// failedMu protects failedPeerExchanges from concurrent access during
	// parallel peer exchange dials (H3 fix).
	failedMu            sync.Mutex
	failedPeerExchanges map[peer.ID]time.Time
}

// Config contains discovery configuration
type Config struct {
	DiscoveryInterval time.Duration
	MaxConnections    int
}

// NewManager creates a new discovery manager.
//
// The second parameter is intentionally typed as interface{} so callers that
// previously passed a DHT instance can continue to do so; the value is ignored.
func NewManager(h host.Host, _ interface{}, logger *zap.Logger) *Manager {
	return &Manager{
		host:                h,
		logger:              logger.With(zap.String("component", "peer-discovery")),
		cancel:              nil,
		failedPeerExchanges: make(map[peer.ID]time.Time),
	}
}

// NewManagerSimple creates a manager with a cleaner signature (host + logger).
func NewManagerSimple(h host.Host, logger *zap.Logger) *Manager {
	return NewManager(h, nil, logger)
}

// StartProtocolHandler registers the peer exchange protocol handler on the host
func (d *Manager) StartProtocolHandler() {
	d.host.SetStreamHandler(PeerExchangeProtocol, d.handlePeerExchangeStream)
	d.logger.Debug("Registered peer exchange protocol handler")
}

// handlePeerExchangeStream handles incoming peer exchange requests
func (d *Manager) handlePeerExchangeStream(s network.Stream) {
	defer s.Close()

	// Read request
	var req PeerExchangeRequest
	decoder := json.NewDecoder(s)
	if err := decoder.Decode(&req); err != nil {
		d.logger.Debug("Failed to decode peer exchange request", zap.Error(err))
		return
	}

	// Get local peer list
	peers := d.host.Peerstore().Peers()
	if req.Limit <= 0 {
		req.Limit = 10 // Default limit
	}
	if req.Limit > len(peers) {
		req.Limit = len(peers)
	}

	// Build response with peer information
	resp := PeerExchangeResponse{Peers: make([]PeerInfo, 0, req.Limit)}
	added := 0

	for _, pid := range peers {
		if added >= req.Limit {
			break
		}
		// Skip self
		if pid == d.host.ID() {
			continue
		}

		addrs := d.host.Peerstore().Addrs(pid)
		if len(addrs) == 0 {
			continue
		}

		filteredAddrs := filterLibp2pAddrs(addrs)
		if len(filteredAddrs) == 0 {
			continue
		}

		addrStrs := make([]string, len(filteredAddrs))
		for i, addr := range filteredAddrs {
			addrStrs[i] = addr.String()
		}

		resp.Peers = append(resp.Peers, PeerInfo{
			ID:    pid.String(),
			Addrs: addrStrs,
		})
		added++
	}

	// Add RQLite metadata if available
	if val, err := d.host.Peerstore().Get(d.host.ID(), "rqlite_metadata"); err == nil {
		if jsonData, ok := val.([]byte); ok {
			var metadata RQLiteNodeMetadata
			if err := json.Unmarshal(jsonData, &metadata); err == nil {
				resp.RQLiteMetadata = &metadata
			}
		}
	}

	// Send response
	encoder := json.NewEncoder(s)
	if err := encoder.Encode(&resp); err != nil {
		d.logger.Debug("Failed to encode peer exchange response", zap.Error(err))
		return
	}

	// Response sent - routine operation, no need to log
}

// Start begins periodic peer discovery
func (d *Manager) Start(config Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	go func() {
		// Do initial discovery immediately
		d.discoverPeers(ctx, config)

		// Continue with periodic discovery
		ticker := time.NewTicker(config.DiscoveryInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.discoverPeers(ctx, config)
			}
		}
	}()

	return nil
}

// Stop stops peer discovery
func (d *Manager) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// discoverPeers discovers and connects to new peers using non-DHT strategies:
//   - Peerstore entries (peers added to peerstore by the caller)
//   - Peer exchange: query currently connected peers' peerstore entries
func (d *Manager) discoverPeers(ctx context.Context, config Config) {
	connectedPeers := d.host.Network().Peers()
	initialCount := len(connectedPeers)

	newConnections := 0

	// Strategy 1: Try to connect to peers learned from the host's peerstore
	newConnections += d.discoverViaPeerstore(ctx, config.MaxConnections-newConnections)

	// Strategy 2: Ask connected peers about their connections (peer exchange)
	if newConnections < config.MaxConnections {
		newConnections += d.discoverViaPeerExchange(ctx, config.MaxConnections-newConnections)
	}

	finalPeerCount := len(d.host.Network().Peers())

	// Summary log: only log if there were changes or new connections
	if newConnections > 0 || finalPeerCount != initialCount {
		d.logger.Debug("Discovery summary",
			zap.Int("connected", finalPeerCount),
			zap.Int("new", newConnections),
			zap.Int("was", initialCount))
	}
}

// discoverViaPeerstore attempts to connect to peers found in the host's peerstore.
// This is useful for peers that have been pre-populated into the peerstore.
func (d *Manager) discoverViaPeerstore(ctx context.Context, maxConnections int) int {
	if maxConnections <= 0 {
		return 0
	}

	connected := 0

	// Iterate over peerstore known peers
	peers := d.host.Peerstore().Peers()

	for _, pid := range peers {
		if connected >= maxConnections {
			break
		}
		if pid == d.host.ID() {
			continue
		}
		if d.host.Network().Connectedness(pid) != network.NotConnected {
			continue
		}

		// Only connect to peers with addresses on the standard libp2p port
		peerInfo := d.host.Peerstore().PeerInfo(pid)
		if !hasLibp2pAddr(peerInfo.Addrs) {
			continue
		}

		// Try to connect
		if err := d.connectToPeer(ctx, pid); err == nil {
			connected++
		}
	}

	return connected
}

// discoverViaPeerExchange asks currently connected peers for addresses of other peers
// by using an active peer exchange protocol.
func (d *Manager) discoverViaPeerExchange(ctx context.Context, maxConnections int) int {
	if maxConnections <= 0 {
		return 0
	}

	connected := 0
	connectedPeers := d.host.Network().Peers()
	if len(connectedPeers) == 0 {
		return 0
	}

	exchangedPeers := 0
	metadataCollected := 0

	for _, peerID := range connectedPeers {
		if connected >= maxConnections {
			break
		}

		// Request peer list from this peer
		peers := d.requestPeersFromPeer(ctx, peerID, maxConnections-connected)
		if len(peers) == 0 {
			continue
		}

		exchangedPeers++
		// Check if we got RQLite metadata
		if val, err := d.host.Peerstore().Get(peerID, "rqlite_metadata"); err == nil {
			if _, ok := val.([]byte); ok {
				metadataCollected++
			}
		}

		// Try to connect to discovered peers
		for _, peerInfo := range peers {
			if connected >= maxConnections {
				break
			}

			// Parse peer ID and addresses
			parsedID, err := peer.Decode(peerInfo.ID)
			if err != nil {
				d.logger.Debug("Failed to parse peer ID", zap.Error(err))
				continue
			}

			// Skip self
			if parsedID == d.host.ID() {
				continue
			}

			// Skip if already connected
			if d.host.Network().Connectedness(parsedID) != network.NotConnected {
				continue
			}

			// Parse and filter addresses to only include port 4001 (standard libp2p port)
			parsedAddrs := make([]multiaddr.Multiaddr, 0, len(peerInfo.Addrs))
			for _, addrStr := range peerInfo.Addrs {
				ma, err := multiaddr.NewMultiaddr(addrStr)
				if err != nil {
					d.logger.Debug("Failed to parse multiaddr", zap.Error(err))
					continue
				}
				parsedAddrs = append(parsedAddrs, ma)
			}
			addrs := filterLibp2pAddrs(parsedAddrs)
			if len(addrs) == 0 {
				continue
			}

			// Add to peerstore (only valid addresses with port 4001)
			d.host.Peerstore().AddAddrs(parsedID, addrs, time.Hour*24)

			// Try to connect (5s timeout — WireGuard peers respond fast)
			connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			peerAddrInfo := peer.AddrInfo{ID: parsedID, Addrs: addrs}

			if err := d.host.Connect(connectCtx, peerAddrInfo); err != nil {
				cancel()
				// Only log connection failures for debugging - errors are still useful
				d.logger.Debug("Connect failed",
					zap.String("peer", parsedID.String()[:8]+"..."),
					zap.Error(err))
				continue
			}
			cancel()

			d.logger.Info("Connected",
				zap.String("peer", parsedID.String()[:8]+"..."),
				zap.String("from", peerID.String()[:8]+"..."))
			connected++
		}
	}

	// Summary log for peer exchange
	if exchangedPeers > 0 {
		d.logger.Debug("Exchange summary",
			zap.Int("exchanged_with", exchangedPeers),
			zap.Int("metadata_collected", metadataCollected),
			zap.Int("new_connections", connected))
	}

	return connected
}

// requestPeersFromPeer asks a specific peer for its peer list
func (d *Manager) requestPeersFromPeer(ctx context.Context, peerID peer.ID, limit int) []PeerInfo {
	// Open a stream to the peer
	stream, err := d.host.NewStream(ctx, peerID, PeerExchangeProtocol)
	if err != nil {
		d.failedMu.Lock()
		if strings.Contains(err.Error(), "protocols not supported") {
			// Lightweight client (gateway, etc.) — expected, track to suppress retries
			d.failedPeerExchanges[peerID] = time.Now()
			d.failedMu.Unlock()
			return nil
		}

		// Actual connection error — log but suppress repeated warnings
		lastFailure, seen := d.failedPeerExchanges[peerID]
		if !seen || time.Since(lastFailure) > time.Minute {
			d.logger.Debug("Failed to open peer exchange stream with node",
				zap.String("peer_id", peerID.String()[:8]+"..."),
				zap.String("reason", "peer does not support peer exchange protocol or connection failed"),
				zap.Error(err))
			d.failedPeerExchanges[peerID] = time.Now()
		}
		d.failedMu.Unlock()
		return nil
	}
	defer stream.Close()

	// Clear failure tracking on success
	d.failedMu.Lock()
	delete(d.failedPeerExchanges, peerID)
	d.failedMu.Unlock()

	// Send request
	req := PeerExchangeRequest{Limit: limit}
	encoder := json.NewEncoder(stream)
	if err := encoder.Encode(&req); err != nil {
		d.logger.Debug("Failed to send peer exchange request", zap.Error(err))
		return nil
	}

	// Set read deadline (5s — small JSON payload)
	if err := stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		d.logger.Debug("Failed to set read deadline", zap.Error(err))
		return nil
	}

	// Read response
	var resp PeerExchangeResponse
	decoder := json.NewDecoder(stream)
	if err := decoder.Decode(&resp); err != nil {
		if err != io.EOF {
			d.logger.Debug("Failed to read peer exchange response", zap.Error(err))
		}
		return nil
	}

	// Store remote peer's RQLite metadata if available
	if resp.RQLiteMetadata != nil {
		// Verify sender identity — prevent metadata spoofing (H2 fix).
		// If the metadata contains a PeerID, it must match the stream sender.
		if resp.RQLiteMetadata.PeerID != "" && resp.RQLiteMetadata.PeerID != peerID.String() {
			d.logger.Warn("Rejected metadata: PeerID mismatch",
				zap.String("claimed", resp.RQLiteMetadata.PeerID[:8]+"..."),
				zap.String("actual", peerID.String()[:8]+"..."))
			return resp.Peers
		}
		// Stamp verified PeerID so downstream consumers can trust it
		resp.RQLiteMetadata.PeerID = peerID.String()

		metadataJSON, err := json.Marshal(resp.RQLiteMetadata)
		if err == nil {
			_ = d.host.Peerstore().Put(peerID, "rqlite_metadata", metadataJSON)
			d.logger.Debug("Metadata stored",
				zap.String("peer", peerID.String()[:8]+"..."),
				zap.String("node", resp.RQLiteMetadata.NodeID))
		}
	}

	return resp.Peers
}

// TriggerPeerExchange manually triggers peer exchange with all connected peers
// This is useful for pre-startup cluster discovery to populate the peerstore with RQLite metadata
func (d *Manager) TriggerPeerExchange(ctx context.Context) int {
	connectedPeers := d.host.Network().Peers()
	if len(connectedPeers) == 0 {
		d.logger.Debug("No connected peers for peer exchange")
		return 0
	}

	metadataCollected := 0
	for _, peerID := range connectedPeers {
		// Request peer list from this peer (which includes their RQLite metadata)
		_ = d.requestPeersFromPeer(ctx, peerID, 50) // Request up to 50 peers

		// Check if we got RQLite metadata from this peer
		if val, err := d.host.Peerstore().Get(peerID, "rqlite_metadata"); err == nil {
			if _, ok := val.([]byte); ok {
				metadataCollected++
			}
		}
	}

	d.logger.Info("Exchange completed",
		zap.Int("peers", len(connectedPeers)),
		zap.Int("with_metadata", metadataCollected))

	return metadataCollected
}

// connectToPeer attempts to connect to a specific peer using its peerstore info.
func (d *Manager) connectToPeer(ctx context.Context, peerID peer.ID) error {
	peerInfo := d.host.Peerstore().PeerInfo(peerID)
	if len(peerInfo.Addrs) == 0 {
		return errors.New("no addresses for peer")
	}

	// Attempt connection
	if err := d.host.Connect(ctx, peerInfo); err != nil {
		d.logger.Debug("Failed to connect to peer",
			zap.String("peer_id", peerID.String()[:8]+"..."),
			zap.Error(err))
		return err
	}

	// Connection success logged at higher level - no need for duplicate DEBUG log

	return nil
}
