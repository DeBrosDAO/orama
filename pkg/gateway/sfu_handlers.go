package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/sfu"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// SFUManager is a type alias for the SFU room manager
type SFUManager = sfu.RoomManager

// CreateRoomRequest is the request body for creating a room
type CreateRoomRequest struct {
	RoomID string `json:"roomId"`
}

// CreateRoomResponse is the response for creating/joining a room
type CreateRoomResponse struct {
	RoomID          string                 `json:"roomId"`
	Created         bool                   `json:"created"`
	RTPCapabilities map[string]interface{} `json:"rtpCapabilities"`
}

// JoinRoomRequest is the request body for joining a room
type JoinRoomRequest struct {
	DisplayName string `json:"displayName"`
}

// JoinRoomResponse is the response for joining a room
type JoinRoomResponse struct {
	ParticipantID string                `json:"participantId"`
	Participants  []sfu.ParticipantInfo `json:"participants"`
}

// sfuCreateRoomHandler handles POST /v1/sfu/room
func (g *Gateway) sfuCreateRoomHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Check if SFU is enabled
	if g.sfuManager == nil {
		writeError(w, http.StatusServiceUnavailable, "SFU service not enabled")
		return
	}

	// Get namespace from auth context
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	// Parse request
	var req CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RoomID == "" {
		writeError(w, http.StatusBadRequest, "roomId is required")
		return
	}

	// Create or get room
	room, created := g.sfuManager.GetOrCreateRoom(ns, req.RoomID)

	g.logger.ComponentInfo(logging.ComponentGeneral, "SFU room request",
		zap.String("room_id", req.RoomID),
		zap.String("namespace", ns),
		zap.Bool("created", created),
		zap.Int("participants", room.GetParticipantCount()),
	)

	writeJSON(w, http.StatusOK, &CreateRoomResponse{
		RoomID:          req.RoomID,
		Created:         created,
		RTPCapabilities: sfu.GetRTPCapabilities(),
	})
}

// sfuRoomHandler handles all /v1/sfu/room/:roomId/* endpoints
func (g *Gateway) sfuRoomHandler(w http.ResponseWriter, r *http.Request) {
	// Check if SFU is enabled
	if g.sfuManager == nil {
		writeError(w, http.StatusServiceUnavailable, "SFU service not enabled")
		return
	}

	// Get namespace from auth context
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	// Parse room ID and action from path
	// Path format: /v1/sfu/room/{roomId}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/v1/sfu/room/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "room ID required")
		return
	}

	roomID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Route to appropriate handler
	switch action {
	case "":
		// GET /v1/sfu/room/:roomId - Get room info
		g.sfuGetRoomHandler(w, r, ns, roomID)
	case "join":
		// POST /v1/sfu/room/:roomId/join - Join room
		g.sfuJoinRoomHandler(w, r, ns, roomID)
	case "leave":
		// POST /v1/sfu/room/:roomId/leave - Leave room
		g.sfuLeaveRoomHandler(w, r, ns, roomID)
	case "participants":
		// GET /v1/sfu/room/:roomId/participants - List participants
		g.sfuParticipantsHandler(w, r, ns, roomID)
	case "ws":
		// GET /v1/sfu/room/:roomId/ws - WebSocket signaling
		g.sfuWebSocketHandler(w, r, ns, roomID)
	default:
		writeError(w, http.StatusNotFound, "unknown action")
	}
}

// sfuGetRoomHandler handles GET /v1/sfu/room/:roomId
func (g *Gateway) sfuGetRoomHandler(w http.ResponseWriter, r *http.Request, ns, roomID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	info, err := g.sfuManager.GetRoomInfo(ns, roomID)
	if err != nil {
		if err == sfu.ErrRoomNotFound {
			writeError(w, http.StatusNotFound, "room not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// sfuJoinRoomHandler handles POST /v1/sfu/room/:roomId/join
func (g *Gateway) sfuJoinRoomHandler(w http.ResponseWriter, r *http.Request, ns, roomID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse request
	var req JoinRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default display name if not provided
		req.DisplayName = "Anonymous"
	}

	// Get user ID
	userID := g.extractUserID(r)
	if userID == "" {
		userID = "anonymous"
	}

	// Get or create room
	room, _ := g.sfuManager.GetOrCreateRoom(ns, roomID)

	// This endpoint just returns room info
	// The actual peer connection is established via WebSocket
	writeJSON(w, http.StatusOK, &JoinRoomResponse{
		ParticipantID: "", // Will be assigned when WebSocket connects
		Participants:  room.GetParticipants(),
	})
}

// sfuLeaveRoomHandler handles POST /v1/sfu/room/:roomId/leave
func (g *Gateway) sfuLeaveRoomHandler(w http.ResponseWriter, r *http.Request, ns, roomID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse participant ID from request body
	var req struct {
		ParticipantID string `json:"participantId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ParticipantID == "" {
		writeError(w, http.StatusBadRequest, "participantId required")
		return
	}

	room, err := g.sfuManager.GetRoom(ns, roomID)
	if err != nil {
		if err == sfu.ErrRoomNotFound {
			writeError(w, http.StatusNotFound, "room not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if err := room.RemovePeer(req.ParticipantID); err != nil {
		if err == sfu.ErrPeerNotFound {
			writeError(w, http.StatusNotFound, "participant not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// sfuParticipantsHandler handles GET /v1/sfu/room/:roomId/participants
func (g *Gateway) sfuParticipantsHandler(w http.ResponseWriter, r *http.Request, ns, roomID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	room, err := g.sfuManager.GetRoom(ns, roomID)
	if err != nil {
		if err == sfu.ErrRoomNotFound {
			writeError(w, http.StatusNotFound, "room not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"participants": room.GetParticipants(),
	})
}

// sfuWebSocketHandler handles WebSocket signaling for a room
func (g *Gateway) sfuWebSocketHandler(w http.ResponseWriter, r *http.Request, ns, roomID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get user ID and display name
	userID := g.extractUserID(r)
	if userID == "" {
		userID = "anonymous"
	}

	displayName := r.URL.Query().Get("displayName")
	if displayName == "" {
		displayName = "Anonymous"
	}

	// Upgrade to WebSocket
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "SFU WebSocket upgrade failed",
			zap.Error(err),
		)
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "SFU WebSocket connected",
		zap.String("room_id", roomID),
		zap.String("namespace", ns),
		zap.String("user_id", userID),
	)

	// Get or create room
	room, _ := g.sfuManager.GetOrCreateRoom(ns, roomID)

	// Create peer
	peer := sfu.NewPeer(userID, displayName, conn, room, g.logger.Logger)

	// Add peer to room
	if err := room.AddPeer(peer); err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "Failed to add peer to room",
			zap.String("room_id", roomID),
			zap.Error(err),
		)
		peer.SendMessage(sfu.NewErrorMessage("join_failed", err.Error()))
		conn.Close()
		return
	}

	// Send welcome message
	peer.SendMessage(sfu.NewServerMessage(sfu.MessageTypeWelcome, &sfu.WelcomeData{
		ParticipantID: peer.ID,
		RoomID:        roomID,
		Participants:  room.GetParticipants(),
	}))

	// Handle signaling messages
	g.handleSFUSignaling(conn, peer, room)
}

// handleSFUSignaling handles WebSocket signaling messages for a peer
func (g *Gateway) handleSFUSignaling(conn *websocket.Conn, peer *sfu.Peer, room *sfu.Room) {
	defer func() {
		room.RemovePeer(peer.ID)
		conn.Close()
		g.logger.ComponentInfo(logging.ComponentGeneral, "SFU WebSocket disconnected",
			zap.String("peer_id", peer.ID),
			zap.String("room_id", room.ID),
		)
	}()

	// Set up ping/pong for keepalive
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}()

	// Read messages
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				g.logger.ComponentWarn(logging.ComponentGeneral, "SFU WebSocket read error",
					zap.String("peer_id", peer.ID),
					zap.Error(err),
				)
			}
			return
		}

		// Reset read deadline
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		// Parse message
		var msg sfu.ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("invalid_message", "failed to parse message"))
			continue
		}

		// Handle message
		g.handleSFUMessage(peer, room, &msg)
	}
}

// handleSFUMessage handles a single signaling message
func (g *Gateway) handleSFUMessage(peer *sfu.Peer, room *sfu.Room, msg *sfu.ClientMessage) {
	switch msg.Type {
	case sfu.MessageTypeOffer:
		var data sfu.OfferData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("invalid_offer", err.Error()))
			return
		}
		if err := peer.HandleOffer(data.SDP); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("offer_failed", err.Error()))
		}

	case sfu.MessageTypeAnswer:
		var data sfu.AnswerData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("invalid_answer", err.Error()))
			return
		}
		if err := peer.HandleAnswer(data.SDP); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("answer_failed", err.Error()))
		}

	case sfu.MessageTypeICECandidate:
		var data sfu.ICECandidateData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("invalid_candidate", err.Error()))
			return
		}
		if err := peer.HandleICECandidate(&data); err != nil {
			peer.SendMessage(sfu.NewErrorMessage("candidate_failed", err.Error()))
		}

	case sfu.MessageTypeMute:
		peer.SetAudioMuted(true)
		g.logger.ComponentDebug(logging.ComponentGeneral, "Peer muted audio", zap.String("peer_id", peer.ID))

	case sfu.MessageTypeUnmute:
		peer.SetAudioMuted(false)
		g.logger.ComponentDebug(logging.ComponentGeneral, "Peer unmuted audio", zap.String("peer_id", peer.ID))

	case sfu.MessageTypeStartVideo:
		peer.SetVideoMuted(false)
		g.logger.ComponentDebug(logging.ComponentGeneral, "Peer started video", zap.String("peer_id", peer.ID))

	case sfu.MessageTypeStopVideo:
		peer.SetVideoMuted(true)
		g.logger.ComponentDebug(logging.ComponentGeneral, "Peer stopped video", zap.String("peer_id", peer.ID))

	case sfu.MessageTypeLeave:
		// Will be handled by deferred cleanup
		g.logger.ComponentInfo(logging.ComponentGeneral, "Peer leaving room", zap.String("peer_id", peer.ID))

	default:
		peer.SendMessage(sfu.NewErrorMessage("unknown_type", "unknown message type"))
	}
}

// initializeSFUManager initializes the SFU manager with the gateway's TURN config
func (g *Gateway) initializeSFUManager() error {
	if g.cfg.SFU == nil || !g.cfg.SFU.Enabled {
		g.logger.ComponentInfo(logging.ComponentGeneral, "SFU service disabled")
		return nil
	}

	// Build ICE servers from config
	iceServers := make([]webrtc.ICEServer, 0)

	// Add configured ICE servers
	if g.cfg.SFU.ICEServers != nil {
		for _, server := range g.cfg.SFU.ICEServers {
			iceServers = append(iceServers, webrtc.ICEServer{
				URLs:       server.URLs,
				Username:   server.Username,
				Credential: server.Credential,
			})
		}
	}

	// Add TURN servers if configured (credentials will be generated dynamically)
	if g.cfg.TURN != nil {
		if len(g.cfg.TURN.STUNURLs) > 0 {
			iceServers = append(iceServers, webrtc.ICEServer{
				URLs: g.cfg.TURN.STUNURLs,
			})
		}
		// Note: TURN credentials are time-limited, so clients should fetch them
		// via the /v1/turn/credentials endpoint before joining a room
	}

	// Create SFU config
	sfuConfig := &sfu.Config{
		MaxParticipants: g.cfg.SFU.MaxParticipants,
		MediaTimeout:    g.cfg.SFU.MediaTimeout,
		ICEServers:      iceServers,
	}

	if sfuConfig.MaxParticipants == 0 {
		sfuConfig.MaxParticipants = 10
	}
	if sfuConfig.MediaTimeout == 0 {
		sfuConfig.MediaTimeout = 30 * time.Second
	}

	manager, err := sfu.NewRoomManager(sfuConfig, g.logger.Logger)
	if err != nil {
		return err
	}

	g.sfuManager = manager

	g.logger.ComponentInfo(logging.ComponentGeneral, "SFU manager initialized",
		zap.Int("max_participants", sfuConfig.MaxParticipants),
		zap.Duration("media_timeout", sfuConfig.MediaTimeout),
		zap.Int("ice_servers", len(iceServers)),
	)

	return nil
}
