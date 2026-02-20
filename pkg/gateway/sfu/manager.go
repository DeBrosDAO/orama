package sfu

import (
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

var (
	ErrRoomNotFound = errors.New("room not found")
)

// RoomManager manages all rooms in the SFU
type RoomManager struct {
	rooms   map[string]*Room // key: namespace:roomId
	roomsMu sync.RWMutex

	api    *webrtc.API
	config *Config
	logger *zap.Logger
}

// NewRoomManager creates a new room manager
func NewRoomManager(config *Config, logger *zap.Logger) (*RoomManager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	api, err := NewWebRTCAPI()
	if err != nil {
		return nil, err
	}

	return &RoomManager{
		rooms:  make(map[string]*Room),
		api:    api,
		config: config,
		logger: logger.With(zap.String("component", "sfu_manager")),
	}, nil
}

// roomKey creates a unique key for namespace:roomId
func roomKey(namespace, roomID string) string {
	return namespace + ":" + roomID
}

// GetOrCreateRoom gets an existing room or creates a new one
func (m *RoomManager) GetOrCreateRoom(namespace, roomID string) (*Room, bool) {
	key := roomKey(namespace, roomID)

	m.roomsMu.Lock()
	defer m.roomsMu.Unlock()

	// Check if room already exists
	if room, ok := m.rooms[key]; ok {
		if !room.IsClosed() {
			return room, false
		}
		// Room is closed, remove it and create a new one
		delete(m.rooms, key)
	}

	// Create new room
	room := NewRoom(roomID, namespace, m.api, m.config, m.logger)

	// Set up empty room handler
	room.OnEmpty(func(r *Room) {
		m.logger.Info("Room is empty, will be garbage collected",
			zap.String("room_id", r.ID),
			zap.String("namespace", r.Namespace),
		)
		// Optionally close the room immediately
		// m.CloseRoom(r.Namespace, r.ID)
	})

	m.rooms[key] = room

	m.logger.Info("Room created",
		zap.String("room_id", roomID),
		zap.String("namespace", namespace),
	)

	return room, true
}

// GetRoom gets an existing room
func (m *RoomManager) GetRoom(namespace, roomID string) (*Room, error) {
	key := roomKey(namespace, roomID)

	m.roomsMu.RLock()
	defer m.roomsMu.RUnlock()

	room, ok := m.rooms[key]
	if !ok {
		return nil, ErrRoomNotFound
	}

	if room.IsClosed() {
		return nil, ErrRoomClosed
	}

	return room, nil
}

// CloseRoom closes and removes a room
func (m *RoomManager) CloseRoom(namespace, roomID string) error {
	key := roomKey(namespace, roomID)

	m.roomsMu.Lock()
	room, ok := m.rooms[key]
	if !ok {
		m.roomsMu.Unlock()
		return ErrRoomNotFound
	}
	delete(m.rooms, key)
	m.roomsMu.Unlock()

	if err := room.Close(); err != nil {
		m.logger.Warn("Error closing room",
			zap.String("room_id", roomID),
			zap.Error(err),
		)
		return err
	}

	m.logger.Info("Room closed",
		zap.String("room_id", roomID),
		zap.String("namespace", namespace),
	)

	return nil
}

// ListRooms returns all rooms for a namespace
func (m *RoomManager) ListRooms(namespace string) []*Room {
	m.roomsMu.RLock()
	defer m.roomsMu.RUnlock()

	prefix := namespace + ":"
	rooms := make([]*Room, 0)

	for key, room := range m.rooms {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix && !room.IsClosed() {
			rooms = append(rooms, room)
		}
	}

	return rooms
}

// RoomInfo contains public information about a room
type RoomInfo struct {
	ID               string            `json:"id"`
	Namespace        string            `json:"namespace"`
	ParticipantCount int               `json:"participantCount"`
	Participants     []ParticipantInfo `json:"participants"`
}

// GetRoomInfo returns public information about a room
func (m *RoomManager) GetRoomInfo(namespace, roomID string) (*RoomInfo, error) {
	room, err := m.GetRoom(namespace, roomID)
	if err != nil {
		return nil, err
	}

	return &RoomInfo{
		ID:               room.ID,
		Namespace:        room.Namespace,
		ParticipantCount: room.GetParticipantCount(),
		Participants:     room.GetParticipants(),
	}, nil
}

// GetStats returns statistics about the room manager
func (m *RoomManager) GetStats() map[string]interface{} {
	m.roomsMu.RLock()
	defer m.roomsMu.RUnlock()

	totalRooms := 0
	totalParticipants := 0

	for _, room := range m.rooms {
		if !room.IsClosed() {
			totalRooms++
			totalParticipants += room.GetParticipantCount()
		}
	}

	return map[string]interface{}{
		"totalRooms":        totalRooms,
		"totalParticipants": totalParticipants,
	}
}

// Close closes all rooms and cleans up resources
func (m *RoomManager) Close() error {
	m.roomsMu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, room := range m.rooms {
		rooms = append(rooms, room)
	}
	m.rooms = make(map[string]*Room)
	m.roomsMu.Unlock()

	for _, room := range rooms {
		if err := room.Close(); err != nil {
			m.logger.Warn("Error closing room during shutdown",
				zap.String("room_id", room.ID),
				zap.Error(err),
			)
		}
	}

	m.logger.Info("Room manager closed", zap.Int("rooms_closed", len(rooms)))
	return nil
}

// GetConfig returns the SFU configuration
func (m *RoomManager) GetConfig() *Config {
	return m.config
}

// UpdateICEServers updates the ICE servers configuration
func (m *RoomManager) UpdateICEServers(servers []webrtc.ICEServer) {
	m.config.ICEServers = servers
}
