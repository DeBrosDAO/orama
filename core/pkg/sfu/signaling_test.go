package sfu

import (
	"encoding/json"
	"testing"
)

func TestClientMessageDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType MessageType
		wantData bool
	}{
		{
			name:     "join message",
			input:    `{"type":"join","data":{"roomId":"room-1","userId":"user-1"}}`,
			wantType: MessageTypeJoin,
			wantData: true,
		},
		{
			name:     "leave message",
			input:    `{"type":"leave"}`,
			wantType: MessageTypeLeave,
			wantData: false,
		},
		{
			name:     "offer message",
			input:    `{"type":"offer","data":{"sdp":"v=0..."}}`,
			wantType: MessageTypeOffer,
			wantData: true,
		},
		{
			name:     "answer message",
			input:    `{"type":"answer","data":{"sdp":"v=0..."}}`,
			wantType: MessageTypeAnswer,
			wantData: true,
		},
		{
			name:     "ice-candidate message",
			input:    `{"type":"ice-candidate","data":{"candidate":"candidate:1234"}}`,
			wantType: MessageTypeICECandidate,
			wantData: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg ClientMessage
			if err := json.Unmarshal([]byte(tt.input), &msg); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if msg.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", msg.Type, tt.wantType)
			}
			if tt.wantData && msg.Data == nil {
				t.Error("expected Data to be non-nil")
			}
			if !tt.wantData && msg.Data != nil {
				t.Error("expected Data to be nil")
			}
		})
	}
}

func TestJoinDataDeserialization(t *testing.T) {
	input := `{"roomId":"room-abc","userId":"user-xyz"}`
	var data JoinData
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if data.RoomID != "room-abc" {
		t.Errorf("RoomID = %q, want %q", data.RoomID, "room-abc")
	}
	if data.UserID != "user-xyz" {
		t.Errorf("UserID = %q, want %q", data.UserID, "user-xyz")
	}
}

func TestServerMessageSerialization(t *testing.T) {
	tests := []struct {
		name    string
		msg     *ServerMessage
		wantKey string
	}{
		{
			name:    "welcome message",
			msg:     NewServerMessage(MessageTypeWelcome, &WelcomeData{PeerID: "p1", RoomID: "r1"}),
			wantKey: "welcome",
		},
		{
			name:    "participant joined",
			msg:     NewServerMessage(MessageTypeParticipantJoined, &ParticipantJoinedData{Participant: ParticipantInfo{PeerID: "p2", UserID: "u2"}}),
			wantKey: "participant-joined",
		},
		{
			name:    "participant left",
			msg:     NewServerMessage(MessageTypeParticipantLeft, &ParticipantLeftData{PeerID: "p2"}),
			wantKey: "participant-left",
		},
		{
			name:    "track added",
			msg:     NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{PeerID: "p1", TrackID: "t1", StreamID: "s1", Kind: "video"}),
			wantKey: "track-added",
		},
		{
			name:    "track removed",
			msg:     NewServerMessage(MessageTypeTrackRemoved, &TrackRemovedData{PeerID: "p1", TrackID: "t1", Kind: "video"}),
			wantKey: "track-removed",
		},
		{
			name:    "TURN credentials",
			msg:     NewServerMessage(MessageTypeTURNCredentials, &TURNCredentialsData{Username: "u", Password: "p", TTL: 600, URIs: []string{"turn:1.2.3.4:3478"}}),
			wantKey: "turn-credentials",
		},
		{
			name:    "server draining",
			msg:     NewServerMessage(MessageTypeServerDraining, &ServerDrainingData{Reason: "shutdown", TimeoutMs: 30000}),
			wantKey: "server-draining",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			// Verify it roundtrips correctly
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("failed to unmarshal to raw: %v", err)
			}

			var msgType string
			if err := json.Unmarshal(raw["type"], &msgType); err != nil {
				t.Fatalf("failed to unmarshal type: %v", err)
			}
			if msgType != tt.wantKey {
				t.Errorf("type = %q, want %q", msgType, tt.wantKey)
			}
			if _, ok := raw["data"]; !ok {
				t.Error("expected data field in output")
			}
		})
	}
}

func TestNewErrorMessage(t *testing.T) {
	msg := NewErrorMessage("invalid_offer", "bad SDP")
	if msg.Type != MessageTypeError {
		t.Errorf("Type = %q, want %q", msg.Type, MessageTypeError)
	}

	errData, ok := msg.Data.(*ErrorData)
	if !ok {
		t.Fatal("Data is not *ErrorData")
	}
	if errData.Code != "invalid_offer" {
		t.Errorf("Code = %q, want %q", errData.Code, "invalid_offer")
	}
	if errData.Message != "bad SDP" {
		t.Errorf("Message = %q, want %q", errData.Message, "bad SDP")
	}

	// Verify serialization
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	result := string(data)
	if result == "" {
		t.Error("expected non-empty serialized output")
	}
}

func TestICECandidateDataToWebRTCCandidate(t *testing.T) {
	data := &ICECandidateData{
		Candidate:        "candidate:842163049 1 udp 1677729535 203.0.113.1 3478 typ srflx",
		SDPMid:           "0",
		SDPMLineIndex:    0,
		UsernameFragment: "abc123",
	}

	candidate := data.ToWebRTCCandidate()
	if candidate.Candidate != data.Candidate {
		t.Errorf("Candidate = %q, want %q", candidate.Candidate, data.Candidate)
	}
	if candidate.SDPMid == nil || *candidate.SDPMid != "0" {
		t.Error("SDPMid should be pointer to '0'")
	}
	if candidate.SDPMLineIndex == nil || *candidate.SDPMLineIndex != 0 {
		t.Error("SDPMLineIndex should be pointer to 0")
	}
	if candidate.UsernameFragment == nil || *candidate.UsernameFragment != "abc123" {
		t.Error("UsernameFragment should be pointer to 'abc123'")
	}
}

func TestWelcomeDataSerialization(t *testing.T) {
	welcome := &WelcomeData{
		PeerID: "peer-123",
		RoomID: "room-456",
		Participants: []ParticipantInfo{
			{PeerID: "peer-001", UserID: "user-001"},
			{PeerID: "peer-002", UserID: "user-002"},
		},
	}

	data, err := json.Marshal(welcome)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result WelcomeData
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.PeerID != "peer-123" {
		t.Errorf("PeerID = %q, want %q", result.PeerID, "peer-123")
	}
	if result.RoomID != "room-456" {
		t.Errorf("RoomID = %q, want %q", result.RoomID, "room-456")
	}
	if len(result.Participants) != 2 {
		t.Errorf("Participants count = %d, want 2", len(result.Participants))
	}
}

func TestTURNCredentialsDataSerialization(t *testing.T) {
	creds := &TURNCredentialsData{
		Username: "1234567890:test-ns",
		Password: "base64password==",
		TTL:      600,
		URIs:     []string{"turn:1.2.3.4:3478?transport=udp", "turns:5.6.7.8:5349"},
	}

	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result TURNCredentialsData
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.Username != creds.Username {
		t.Errorf("Username = %q, want %q", result.Username, creds.Username)
	}
	if result.TTL != 600 {
		t.Errorf("TTL = %d, want 600", result.TTL)
	}
	if len(result.URIs) != 2 {
		t.Errorf("URIs count = %d, want 2", len(result.URIs))
	}
}
