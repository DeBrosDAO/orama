package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// Protocol ID for peer exchange
const PeerExchangeProtocol = "/debros/peer-exchange/1.0.0"

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
	host                host.Host
	logger              *zap.Logger
	cancel              context.CancelFunc
	failedPeerExchanges map[peer.ID]time.Time // Track failed peer exchange attempts to suppress repeated warnings
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

		// Filter addresses to only include port 4001 (standard libp2p port)
		// This prevents including non-libp2p service ports (like RQLite ports) in peer exchange
		const libp2pPort = 4001
		filteredAddrs := make([]multiaddr.Multiaddr, 0)
		filteredCount := 0
		for _, addr := range addrs {
			// Extract TCP port from multiaddr
			port, err := addr.ValueForProtocol(multiaddr.P_TCP)
			if err == nil {
				portNum, err := strconv.Atoi(port)
				if err == nil {
					// Only include addresses with port 4001
					if portNum == libp2pPort {
						filteredAddrs = append(filteredAddrs, addr)
					} else {
						filteredCount++
					}
				}
				// Skip addresses with unparseable ports
			} else {
				// Skip non-TCP addresses (libp2p uses TCP)
				filteredCount++
			}
		}

		// Log if addresses were filtered out
		if filteredCount > 0 {
			d.logger.Debug("Filtered out non-libp2p addresses",
				zap.String("peer_id", pid.String()[:8]+"..."),
				zap.Int("filtered_count", filteredCount),
				zap.Int("valid_count", len(filteredAddrs)))
		}

		// If no addresses remain after filtering, skip this peer
		if len(filteredAddrs) == 0 {
			d.logger.Debug("No valid addresses after filtering",
				zap.String("peer_id", pid.String()[:8]+"..."),
				zap.Int("original_count", len(addrs)))
			continue
		}

		// Convert addresses to strings
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

	d.logger.Debug("Sent peer exchange response",
		zap.Int("peer_count", len(resp.Peers)),
		zap.Bool("has_rqlite_metadata", resp.RQLiteMetadata != nil))
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
//   - Peerstore entries (bootstrap peers added to peerstore by the caller)
//   - Peer exchange: query currently connected peers' peerstore entries
func (d *Manager) discoverPeers(ctx context.Context, config Config) {
	connectedPeers := d.host.Network().Peers()
	initialCount := len(connectedPeers)

	d.logger.Debug("Starting peer discovery",
		zap.Int("current_peers", initialCount))

	newConnections := 0

	// Strategy 1: Try to connect to peers learned from the host's peerstore
	newConnections += d.discoverViaPeerstore(ctx, config.MaxConnections-newConnections)

	// Strategy 2: Ask connected peers about their connections (peer exchange)
	if newConnections < config.MaxConnections {
		newConnections += d.discoverViaPeerExchange(ctx, config.MaxConnections-newConnections)
	}

	finalPeerCount := len(d.host.Network().Peers())

	if newConnections > 0 || finalPeerCount != initialCount {
		d.logger.Debug("Peer discovery completed",
			zap.Int("new_connections", newConnections),
			zap.Int("initial_peers", initialCount),
			zap.Int("final_peers", finalPeerCount))
	}
}

// discoverViaPeerstore attempts to connect to peers found in the host's peerstore.
// This is useful for bootstrap peers that have been pre-populated into the peerstore.
func (d *Manager) discoverViaPeerstore(ctx context.Context, maxConnections int) int {
	if maxConnections <= 0 {
		return 0
	}

	connected := 0

	// Iterate over peerstore known peers
	peers := d.host.Peerstore().Peers()
	d.logger.Debug("Peerstore contains peers", zap.Int("count", len(peers)))

	for _, pid := range peers {
		if connected >= maxConnections {
			break
		}
		// Skip self
		if pid == d.host.ID() {
			continue
		}
		// Skip already connected peers
		if d.host.Network().Connectedness(pid) != network.NotConnected {
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

	d.logger.Debug("Starting peer exchange with connected peers",
		zap.Int("num_peers", len(connectedPeers)))

	for _, peerID := range connectedPeers {
		if connected >= maxConnections {
			break
		}

		// Request peer list from this peer
		peers := d.requestPeersFromPeer(ctx, peerID, maxConnections-connected)
		if len(peers) == 0 {
			continue
		}

		d.logger.Debug("Received peer list from peer",
			zap.String("from_peer", peerID.String()[:8]+"..."),
			zap.Int("peer_count", len(peers)))

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
			const libp2pPort = 4001
			addrs := make([]multiaddr.Multiaddr, 0, len(peerInfo.Addrs))
			for _, addrStr := range peerInfo.Addrs {
				ma, err := multiaddr.NewMultiaddr(addrStr)
				if err != nil {
					d.logger.Debug("Failed to parse multiaddr", zap.Error(err))
					continue
				}
				// Only include addresses with port 4001
				port, err := ma.ValueForProtocol(multiaddr.P_TCP)
				if err == nil {
					portNum, err := strconv.Atoi(port)
					if err == nil && portNum == libp2pPort {
						addrs = append(addrs, ma)
					}
					// Skip addresses with wrong ports
				}
				// Skip non-TCP addresses
			}

			if len(addrs) == 0 {
				d.logger.Debug("No valid libp2p addresses (port 4001) for peer",
					zap.String("peer_id", parsedID.String()[:8]+"..."),
					zap.Int("total_addresses", len(peerInfo.Addrs)))
				continue
			}

			// Add to peerstore (only valid addresses with port 4001)
			d.host.Peerstore().AddAddrs(parsedID, addrs, time.Hour*24)

			// Try to connect
			connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			peerAddrInfo := peer.AddrInfo{ID: parsedID, Addrs: addrs}

			if err := d.host.Connect(connectCtx, peerAddrInfo); err != nil {
				cancel()
				d.logger.Debug("Failed to connect to discovered peer",
					zap.String("peer_id", parsedID.String()[:8]+"..."),
					zap.Error(err))
				continue
			}
			cancel()

			d.logger.Info("Successfully connected to discovered peer",
				zap.String("peer_id", parsedID.String()[:8]+"..."),
				zap.String("discovered_from", peerID.String()[:8]+"..."))
			connected++
		}
	}

	return connected
}

// requestPeersFromPeer asks a specific peer for its peer list
func (d *Manager) requestPeersFromPeer(ctx context.Context, peerID peer.ID, limit int) []PeerInfo {
	// Open a stream to the peer
	stream, err := d.host.NewStream(ctx, peerID, PeerExchangeProtocol)
	if err != nil {
		// Suppress repeated warnings for the same peer (log once per minute max)
		lastFailure, seen := d.failedPeerExchanges[peerID]
		if !seen || time.Since(lastFailure) > time.Minute {
			d.logger.Debug("Failed to open peer exchange stream",
				zap.String("peer_id", peerID.String()[:8]+"..."),
				zap.Error(err))
			d.failedPeerExchanges[peerID] = time.Now()
		}
		return nil
	}
	defer stream.Close()

	// Clear failure tracking on success
	delete(d.failedPeerExchanges, peerID)

	// Send request
	req := PeerExchangeRequest{Limit: limit}
	encoder := json.NewEncoder(stream)
	if err := encoder.Encode(&req); err != nil {
		d.logger.Debug("Failed to send peer exchange request", zap.Error(err))
		return nil
	}

	// Set read deadline
	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
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
		metadataJSON, err := json.Marshal(resp.RQLiteMetadata)
		if err == nil {
			_ = d.host.Peerstore().Put(peerID, "rqlite_metadata", metadataJSON)
			d.logger.Debug("Stored RQLite metadata from peer",
				zap.String("peer_id", peerID.String()[:8]+"..."),
				zap.String("node_id", resp.RQLiteMetadata.NodeID))
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

	d.logger.Info("Manually triggering peer exchange",
		zap.Int("connected_peers", len(connectedPeers)))

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

	d.logger.Info("Peer exchange completed",
		zap.Int("peers_with_metadata", metadataCollected),
		zap.Int("total_peers", len(connectedPeers)))

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

	d.logger.Debug("Successfully connected to peer",
		zap.String("peer_id", peerID.String()[:8]+"..."))

	return nil
}
