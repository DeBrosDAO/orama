package sfu

import (
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// RoomManager manages the lifecycle of rooms.
type RoomManager struct {
	rooms  map[string]*Room // key: roomID
	mu     sync.RWMutex
	config *Config
	logger *zap.Logger

	// reporter is handed to every room; set by NewServer, nil in a bare manager.
	reporter *reporter
}

// NewRoomManager creates a new room manager.
func NewRoomManager(cfg *Config, logger *zap.Logger) *RoomManager {
	return &RoomManager{
		rooms:  make(map[string]*Room),
		config: cfg,
		logger: logger.With(zap.String("component", "room-manager")),
	}
}

// GetOrCreateRoom returns an existing room or creates a new one.
func (rm *RoomManager) GetOrCreateRoom(roomID string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, ok := rm.rooms[roomID]; ok && !room.IsClosed() {
		return room
	}

	api := newWebRTCAPI(rm.config)
	room := &Room{
		ID:              roomID,
		Namespace:       rm.config.Namespace,
		peers:           make(map[string]*Peer),
		publishedTracks: make(map[string]*publishedTrack),
		api:             api,
		config:          rm.config,
		logger:          rm.logger.With(zap.String("room_id", roomID)),
		reporter:        rm.reporter,
	}

	room.onEmpty = func(r *Room) {
		// Start empty room cleanup timer
		go func() {
			<-timeAfter(emptyRoomTTL)
			if r.GetParticipantCount() == 0 {
				rm.mu.Lock()
				delete(rm.rooms, r.ID)
				rm.mu.Unlock()
				r.Close()
				rm.logger.Info("Empty room cleaned up", zap.String("room_id", r.ID))
			}
		}()
	}

	rm.rooms[roomID] = room
	rm.logger.Info("Room created", zap.String("room_id", roomID))
	return room
}

// GetRoom returns a room by ID, or nil if not found.
func (rm *RoomManager) GetRoom(roomID string) *Room {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.rooms[roomID]
}

// HasParticipants reports whether roomID is open here with at least one peer.
// An empty room awaiting cleanup does not count: it hosts no call to join.
func (rm *RoomManager) HasParticipants(roomID string) bool {
	if roomID == "" {
		return false
	}
	room := rm.GetRoom(roomID)
	return room != nil && !room.IsClosed() && room.GetParticipantCount() > 0
}

// CloseAll closes all rooms (for graceful shutdown).
func (rm *RoomManager) CloseAll() {
	rm.mu.Lock()
	rooms := make([]*Room, 0, len(rm.rooms))
	for _, r := range rm.rooms {
		rooms = append(rooms, r)
	}
	rm.rooms = make(map[string]*Room)
	rm.mu.Unlock()

	for _, r := range rooms {
		r.Close()
	}
}

// RoomCount returns the number of active rooms.
func (rm *RoomManager) RoomCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.rooms)
}

// newWebRTCAPI creates a Pion WebRTC API with codecs and interceptors.
func newWebRTCAPI(cfg *Config) *webrtc.API {
	m := &webrtc.MediaEngine{}

	// Audio: Opus
	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack", Parameter: ""},
		{Type: "nack", Parameter: "pli"},
	}

	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio)

	// Video: VP8
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo)

	// Video: H264
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 125,
	}, webrtc.RTPCodecTypeVideo)

	// Interceptors: NACK + PLI
	i := &interceptor.Registry{}
	if f, err := nack.NewResponderInterceptor(); err == nil {
		i.Add(f)
	}
	if f, err := nack.NewGeneratorInterceptor(); err == nil {
		i.Add(f)
	}
	if f, err := intervalpli.NewReceiverInterceptor(); err == nil {
		i.Add(f)
	}

	// SettingEngine: restrict media ports
	se := webrtc.SettingEngine{}
	if cfg.MediaPortStart > 0 && cfg.MediaPortEnd > 0 {
		se.SetEphemeralUDPPortRange(uint16(cfg.MediaPortStart), uint16(cfg.MediaPortEnd))
	}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(i),
		webrtc.WithSettingEngine(se),
	)
}
