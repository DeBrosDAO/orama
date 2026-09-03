package sfu

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testConfig() *Config {
	return &Config{
		ListenAddr:        "10.0.0.1:8443",
		Namespace:         "test-ns",
		MediaPortStart:    20000,
		MediaPortEnd:      20500,
		TURNServers:       []TURNServerConfig{{Host: "1.2.3.4", Port: 3478}},
		TURNSecret:        "test-secret-key-32bytes-long!!!!",
		TURNCredentialTTL: 600,
		RQLiteDSN:         "http://10.0.0.1:4001",
	}
}

func testLogger() *zap.Logger {
	return zap.NewNop()
}

// --- RoomManager tests ---

func TestNewRoomManager(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	if rm == nil {
		t.Fatal("NewRoomManager returned nil")
	}
	if rm.RoomCount() != 0 {
		t.Errorf("RoomCount = %d, want 0", rm.RoomCount())
	}
}

func TestRoomManagerGetOrCreateRoom(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())

	room1 := rm.GetOrCreateRoom("room-1")
	if room1 == nil {
		t.Fatal("GetOrCreateRoom returned nil")
	}
	if room1.ID != "room-1" {
		t.Errorf("Room.ID = %q, want %q", room1.ID, "room-1")
	}
	if room1.Namespace != "test-ns" {
		t.Errorf("Room.Namespace = %q, want %q", room1.Namespace, "test-ns")
	}
	if rm.RoomCount() != 1 {
		t.Errorf("RoomCount = %d, want 1", rm.RoomCount())
	}

	// Getting same room returns same instance
	room1Again := rm.GetOrCreateRoom("room-1")
	if room1 != room1Again {
		t.Error("expected same room instance")
	}
	if rm.RoomCount() != 1 {
		t.Errorf("RoomCount = %d, want 1 (same room)", rm.RoomCount())
	}

	// Different room creates new instance
	room2 := rm.GetOrCreateRoom("room-2")
	if room2 == nil {
		t.Fatal("second room is nil")
	}
	if room2.ID != "room-2" {
		t.Errorf("Room.ID = %q, want %q", room2.ID, "room-2")
	}
	if rm.RoomCount() != 2 {
		t.Errorf("RoomCount = %d, want 2", rm.RoomCount())
	}
}

func TestRoomManagerGetRoom(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())

	// Non-existent room returns nil
	room := rm.GetRoom("nonexistent")
	if room != nil {
		t.Error("expected nil for non-existent room")
	}

	// Create a room and retrieve it
	rm.GetOrCreateRoom("room-1")
	room = rm.GetRoom("room-1")
	if room == nil {
		t.Fatal("expected non-nil for existing room")
	}
	if room.ID != "room-1" {
		t.Errorf("Room.ID = %q, want %q", room.ID, "room-1")
	}
}

func TestRoomManagerCloseAll(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())

	rm.GetOrCreateRoom("room-1")
	rm.GetOrCreateRoom("room-2")
	rm.GetOrCreateRoom("room-3")
	if rm.RoomCount() != 3 {
		t.Fatalf("RoomCount = %d, want 3", rm.RoomCount())
	}

	rm.CloseAll()
	if rm.RoomCount() != 0 {
		t.Errorf("RoomCount after CloseAll = %d, want 0", rm.RoomCount())
	}
}

func TestRoomManagerGetOrCreateRoomReplacesClosedRoom(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())

	room1 := rm.GetOrCreateRoom("room-1")
	room1.Close()

	// Getting the same room ID after close should create a new room
	room1New := rm.GetOrCreateRoom("room-1")
	if room1New == room1 {
		t.Error("expected new room instance after close")
	}
	if room1New.IsClosed() {
		t.Error("new room should not be closed")
	}
}

// --- Room tests ---

func TestRoomIsClosed(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	room := rm.GetOrCreateRoom("room-1")

	if room.IsClosed() {
		t.Error("new room should not be closed")
	}

	room.Close()
	if !room.IsClosed() {
		t.Error("room should be closed after Close()")
	}
}

func TestRoomCloseIdempotent(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	room := rm.GetOrCreateRoom("room-1")

	// Should not panic or error when called multiple times
	if err := room.Close(); err != nil {
		t.Errorf("first Close() returned error: %v", err)
	}
	if err := room.Close(); err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestRoomGetParticipantsEmpty(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	room := rm.GetOrCreateRoom("room-1")

	participants := room.GetParticipants()
	if len(participants) != 0 {
		t.Errorf("Participants count = %d, want 0", len(participants))
	}
	if room.GetParticipantCount() != 0 {
		t.Errorf("ParticipantCount = %d, want 0", room.GetParticipantCount())
	}
}

func TestRoomBuildICEServers(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	room := rm.GetOrCreateRoom("room-1")

	servers := room.buildICEServers()
	if len(servers) != 1 {
		t.Fatalf("ICE servers count = %d, want 1", len(servers))
	}
	if len(servers[0].URLs) != 2 {
		t.Fatalf("URLs count = %d, want 2", len(servers[0].URLs))
	}
	if servers[0].URLs[0] != "turn:1.2.3.4:3478?transport=udp" {
		t.Errorf("URL[0] = %q, want %q", servers[0].URLs[0], "turn:1.2.3.4:3478?transport=udp")
	}
	if servers[0].URLs[1] != "turn:1.2.3.4:3478?transport=tcp" {
		t.Errorf("URL[1] = %q, want %q", servers[0].URLs[1], "turn:1.2.3.4:3478?transport=tcp")
	}
	if servers[0].Username == "" {
		t.Error("Username should not be empty")
	}
	if servers[0].Credential == "" {
		t.Error("Credential should not be empty")
	}
}

func TestRoomBuildICEServersNoTURN(t *testing.T) {
	cfg := testConfig()
	cfg.TURNServers = nil

	rm := NewRoomManager(cfg, testLogger())
	room := rm.GetOrCreateRoom("room-1")

	servers := room.buildICEServers()
	if servers != nil {
		t.Errorf("expected nil ICE servers when no TURN configured, got %v", servers)
	}
}

func TestRoomBuildICEServersNoSecret(t *testing.T) {
	cfg := testConfig()
	cfg.TURNSecret = ""

	rm := NewRoomManager(cfg, testLogger())
	room := rm.GetOrCreateRoom("room-1")

	servers := room.buildICEServers()
	if servers != nil {
		t.Errorf("expected nil ICE servers when no secret, got %v", servers)
	}
}

func TestRoomBuildICEServersMultipleTURN(t *testing.T) {
	cfg := testConfig()
	cfg.TURNServers = []TURNServerConfig{
		{Host: "1.2.3.4", Port: 3478},               // non-secure → UDP + TCP = 2 URIs
		{Host: "5.6.7.8", Port: 5349, Secure: true}, // secure → 1 URI
	}

	rm := NewRoomManager(cfg, testLogger())
	room := rm.GetOrCreateRoom("room-1")

	servers := room.buildICEServers()
	if len(servers) != 1 {
		t.Fatalf("ICE servers count = %d, want 1", len(servers))
	}
	// 1 non-secure (UDP+TCP) + 1 secure (TURNS) = 3 URIs
	if len(servers[0].URLs) != 3 {
		t.Fatalf("URLs count = %d, want 3", len(servers[0].URLs))
	}
}

// --- Empty room cleanup test ---

func TestEmptyRoomCleanup(t *testing.T) {
	// Override timeAfter for instant timer
	origTimeAfter := timeAfter
	timeAfter = func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	defer func() { timeAfter = origTimeAfter }()

	rm := NewRoomManager(testConfig(), testLogger())
	room := rm.GetOrCreateRoom("room-1")

	// Trigger the onEmpty callback (which starts cleanup timer)
	room.onEmpty(room)

	// Give the goroutine time to execute
	time.Sleep(50 * time.Millisecond)

	if rm.RoomCount() != 0 {
		t.Errorf("RoomCount = %d, want 0 (should have been cleaned up)", rm.RoomCount())
	}
}

// --- Server health tests ---

func TestHealthEndpointOK(t *testing.T) {
	cfg := testConfig()
	server, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	server.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if body != `{"status":"ok","rooms":0}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok","rooms":0}`)
	}
}

func TestHealthEndpointDraining(t *testing.T) {
	cfg := testConfig()
	server, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Set draining
	server.drainingMu.Lock()
	server.draining = true
	server.drainingMu.Unlock()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	server.handleHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	body := w.Body.String()
	if body != `{"status":"draining","rooms":0}` {
		t.Errorf("body = %q, want %q", body, `{"status":"draining","rooms":0}`)
	}
}

func TestServerDrainSetsFlag(t *testing.T) {
	// Override timeAfter for instant timer
	origTimeAfter := timeAfter
	timeAfter = func(d time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	defer func() { timeAfter = origTimeAfter }()

	cfg := testConfig()
	server, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	server.Drain(0)

	server.drainingMu.RLock()
	draining := server.draining
	server.drainingMu.RUnlock()

	if !draining {
		t.Error("expected draining to be true after Drain()")
	}
}

func TestServerNewServerValidation(t *testing.T) {
	// Invalid config should return error
	cfg := &Config{} // Empty = invalid
	_, err := NewServer(cfg, testLogger())
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestServerSignalEndpointRejectsDraining(t *testing.T) {
	cfg := testConfig()
	server, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	server.drainingMu.Lock()
	server.draining = true
	server.drainingMu.Unlock()

	req := httptest.NewRequest("GET", "/ws/signal", nil)
	w := httptest.NewRecorder()
	server.handleSignal(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
