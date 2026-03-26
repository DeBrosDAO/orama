package sfu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Server is the SFU HTTP server providing WebSocket signaling and a health endpoint.
// It binds only to a WireGuard IP — never exposed publicly.
type Server struct {
	config      *Config
	roomManager *RoomManager
	logger      *zap.Logger
	httpServer  *http.Server
	upgrader    websocket.Upgrader
	draining    bool
	drainingMu  sync.RWMutex
}

// NewServer creates a new SFU server.
func NewServer(cfg *Config, logger *zap.Logger) (*Server, error) {
	if errs := cfg.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid SFU config: %v", errs[0])
	}

	s := &Server{
		config:      cfg,
		roomManager: NewRoomManager(cfg, logger),
		logger:      logger.With(zap.String("component", "sfu"), zap.String("namespace", cfg.Namespace)),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true }, // Gateway handles auth
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/signal", s.handleSignal)
	mux.HandleFunc("/health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

// ListenAndServe starts the HTTP server. Blocks until the server is stopped.
func (s *Server) ListenAndServe() error {
	s.logger.Info("SFU server starting",
		zap.String("addr", s.config.ListenAddr),
		zap.String("namespace", s.config.Namespace))
	return s.httpServer.ListenAndServe()
}

// Drain initiates graceful drain: notifies all peers, waits, then closes.
func (s *Server) Drain(timeout time.Duration) {
	s.drainingMu.Lock()
	s.draining = true
	s.drainingMu.Unlock()

	s.logger.Info("SFU draining started", zap.Duration("timeout", timeout))

	// Notify all peers
	s.roomManager.mu.RLock()
	for _, room := range s.roomManager.rooms {
		room.broadcastMessage("", NewServerMessage(MessageTypeServerDraining, &ServerDrainingData{
			Reason:    "server shutting down",
			TimeoutMs: int(timeout.Milliseconds()),
		}))
	}
	s.roomManager.mu.RUnlock()

	// Wait for timeout, then force close
	<-timeAfter(timeout)
}

// Close shuts down the SFU server.
func (s *Server) Close() error {
	s.logger.Info("SFU server shutting down")
	s.roomManager.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// handleHealth is a simple health check endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.drainingMu.RLock()
	draining := s.draining
	s.drainingMu.RUnlock()

	if draining {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"draining","rooms":%d}`, s.roomManager.RoomCount())
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","rooms":%d}`, s.roomManager.RoomCount())
}

// handleSignal upgrades to WebSocket and runs the signaling loop for one peer.
func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	s.drainingMu.RLock()
	if s.draining {
		s.drainingMu.RUnlock()
		http.Error(w, "server draining", http.StatusServiceUnavailable)
		return
	}
	s.drainingMu.RUnlock()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}

	s.logger.Debug("WebSocket connected", zap.String("remote", r.RemoteAddr))

	// Read the first message — must be a join
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		s.logger.Warn("Failed to read join message", zap.Error(err))
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // Clear deadline

	var msg ClientMessage
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		conn.WriteMessage(websocket.TextMessage, mustMarshal(NewErrorMessage("invalid_message", "malformed JSON")))
		conn.Close()
		return
	}
	if msg.Type != MessageTypeJoin {
		conn.WriteMessage(websocket.TextMessage, mustMarshal(NewErrorMessage("invalid_message", "first message must be join")))
		conn.Close()
		return
	}

	var joinData JoinData
	if err := json.Unmarshal(msg.Data, &joinData); err != nil || joinData.RoomID == "" || joinData.UserID == "" {
		conn.WriteMessage(websocket.TextMessage, mustMarshal(NewErrorMessage("invalid_join", "roomId and userId required")))
		conn.Close()
		return
	}

	room := s.roomManager.GetOrCreateRoom(joinData.RoomID)
	peer := NewPeer(joinData.UserID, conn, room, s.logger)

	if err := room.AddPeer(peer); err != nil {
		conn.WriteMessage(websocket.TextMessage, mustMarshal(NewErrorMessage("join_failed", err.Error())))
		conn.Close()
		return
	}

	// Send welcome with current participants
	peer.SendMessage(NewServerMessage(MessageTypeWelcome, &WelcomeData{
		PeerID:       peer.ID,
		RoomID:       room.ID,
		Participants: room.GetParticipants(),
	}))

	// Send TURN credentials
	if s.config.TURNSecret != "" && len(s.config.TURNServers) > 0 {
		s.sendTURNCredentials(peer)
	}

	// Send existing tracks from other peers
	room.SendExistingTracksTo(peer)

	// Start credential refresh goroutine
	if s.config.TURNCredentialTTL > 0 {
		go s.credentialRefreshLoop(peer)
	}

	// Signaling read loop
	s.signalingLoop(peer, room)
}

// signalingLoop reads signaling messages from the WebSocket until disconnect.
func (s *Server) signalingLoop(peer *Peer, room *Room) {
	defer room.RemovePeer(peer.ID)

	for {
		_, msgBytes, err := peer.conn.ReadMessage()
		if err != nil {
			s.logger.Debug("WebSocket read error", zap.String("peer_id", peer.ID), zap.Error(err))
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			peer.SendMessage(NewErrorMessage("invalid_message", "malformed JSON"))
			continue
		}

		switch msg.Type {
		case MessageTypeOffer:
			var data OfferData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				peer.SendMessage(NewErrorMessage("invalid_offer", err.Error()))
				continue
			}
			if err := peer.HandleOffer(data.SDP); err != nil {
				s.logger.Error("Failed to handle offer", zap.String("peer_id", peer.ID), zap.Error(err))
				peer.SendMessage(NewErrorMessage("offer_failed", err.Error()))
			}

		case MessageTypeAnswer:
			var data AnswerData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				peer.SendMessage(NewErrorMessage("invalid_answer", err.Error()))
				continue
			}
			if err := peer.HandleAnswer(data.SDP); err != nil {
				s.logger.Error("Failed to handle answer", zap.String("peer_id", peer.ID), zap.Error(err))
			}

		case MessageTypeICECandidate:
			var data ICECandidateData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				peer.SendMessage(NewErrorMessage("invalid_candidate", err.Error()))
				continue
			}
			if err := peer.HandleICECandidate(&data); err != nil {
				s.logger.Error("Failed to handle ICE candidate", zap.String("peer_id", peer.ID), zap.Error(err))
			}

		case MessageTypeLeave:
			s.logger.Info("Peer leaving", zap.String("peer_id", peer.ID))
			return

		default:
			peer.SendMessage(NewErrorMessage("unknown_message", fmt.Sprintf("unknown message type: %s", msg.Type)))
		}
	}
}

// sendTURNCredentials sends TURN server credentials to a peer.
func (s *Server) sendTURNCredentials(peer *Peer) {
	ttl := time.Duration(s.config.TURNCredentialTTL) * time.Second
	username, password := turn.GenerateCredentials(s.config.TURNSecret, s.config.Namespace, ttl)

	var uris []string
	for _, ts := range s.config.TURNServers {
		if ts.Secure {
			uris = append(uris, fmt.Sprintf("turns:%s:%d", ts.Host, ts.Port))
		} else {
			uris = append(uris, fmt.Sprintf("turn:%s:%d?transport=udp", ts.Host, ts.Port))
			uris = append(uris, fmt.Sprintf("turn:%s:%d?transport=tcp", ts.Host, ts.Port))
		}
	}

	peer.SendMessage(NewServerMessage(MessageTypeTURNCredentials, &TURNCredentialsData{
		Username: username,
		Password: password,
		TTL:      s.config.TURNCredentialTTL,
		URIs:     uris,
	}))
}

// credentialRefreshLoop sends fresh TURN credentials at 80% of TTL.
func (s *Server) credentialRefreshLoop(peer *Peer) {
	refreshInterval := time.Duration(float64(s.config.TURNCredentialTTL)*0.8) * time.Second

	for {
		<-timeAfter(refreshInterval)

		peer.closedMu.RLock()
		closed := peer.closed
		peer.closedMu.RUnlock()
		if closed {
			return
		}

		s.sendTURNCredentials(peer)
		s.logger.Debug("Refreshed TURN credentials", zap.String("peer_id", peer.ID))
	}
}

func mustMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
