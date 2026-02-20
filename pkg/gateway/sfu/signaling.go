package sfu

import (
	"encoding/json"

	"github.com/pion/webrtc/v4"
)

// MessageType represents the type of signaling message
type MessageType string

const (
	// Client -> Server message types
	MessageTypeJoin         MessageType = "join"
	MessageTypeLeave        MessageType = "leave"
	MessageTypeOffer        MessageType = "offer"
	MessageTypeAnswer       MessageType = "answer"
	MessageTypeICECandidate MessageType = "ice-candidate"
	MessageTypeMute         MessageType = "mute"
	MessageTypeUnmute       MessageType = "unmute"
	MessageTypeStartVideo   MessageType = "start-video"
	MessageTypeStopVideo    MessageType = "stop-video"

	// Server -> Client message types
	MessageTypeWelcome           MessageType = "welcome"
	MessageTypeParticipantJoined MessageType = "participant-joined"
	MessageTypeParticipantLeft   MessageType = "participant-left"
	MessageTypeTrackAdded        MessageType = "track-added"
	MessageTypeTrackRemoved      MessageType = "track-removed"
	MessageTypeError             MessageType = "error"
)

// ClientMessage represents a message from client to server
type ClientMessage struct {
	Type MessageType     `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ServerMessage represents a message from server to client
type ServerMessage struct {
	Type MessageType `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// JoinData is the payload for join messages
type JoinData struct {
	DisplayName string `json:"displayName"`
	AudioOnly   bool   `json:"audioOnly,omitempty"`
}

// OfferData is the payload for offer messages
type OfferData struct {
	SDP string `json:"sdp"`
}

// AnswerData is the payload for answer messages
type AnswerData struct {
	SDP string `json:"sdp"`
}

// ICECandidateData is the payload for ICE candidate messages
type ICECandidateData struct {
	Candidate        string `json:"candidate"`
	SDPMid           string `json:"sdpMid,omitempty"`
	SDPMLineIndex    uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment string `json:"usernameFragment,omitempty"`
}

// ToWebRTCCandidate converts ICECandidateData to webrtc.ICECandidateInit
func (c *ICECandidateData) ToWebRTCCandidate() webrtc.ICECandidateInit {
	return webrtc.ICECandidateInit{
		Candidate:        c.Candidate,
		SDPMid:           &c.SDPMid,
		SDPMLineIndex:    &c.SDPMLineIndex,
		UsernameFragment: &c.UsernameFragment,
	}
}

// WelcomeData is sent to a client when they successfully join
type WelcomeData struct {
	ParticipantID string            `json:"participantId"`
	RoomID        string            `json:"roomId"`
	Participants  []ParticipantInfo `json:"participants"`
}

// ParticipantInfo contains public information about a participant
type ParticipantInfo struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	HasAudio    bool   `json:"hasAudio"`
	HasVideo    bool   `json:"hasVideo"`
	AudioMuted  bool   `json:"audioMuted"`
	VideoMuted  bool   `json:"videoMuted"`
}

// ParticipantJoinedData is sent when a new participant joins
type ParticipantJoinedData struct {
	Participant ParticipantInfo `json:"participant"`
}

// ParticipantLeftData is sent when a participant leaves
type ParticipantLeftData struct {
	ParticipantID string `json:"participantId"`
}

// TrackAddedData is sent when a new track is available
type TrackAddedData struct {
	ParticipantID string `json:"participantId"`           // Internal SFU peer ID
	UserID        string `json:"userId"`                  // The user's actual ID for easier mapping
	TrackID       string `json:"trackId"`                 // Format: "{kind}-{participantId}"
	StreamID      string `json:"streamId"`                // Same as userId (for WebRTC stream matching)
	Kind          string `json:"kind"`                    // "audio" or "video"
}

// TrackRemovedData is sent when a track is removed
type TrackRemovedData struct {
	ParticipantID string `json:"participantId"`
	UserID        string `json:"userId"` // The user's actual ID for easier mapping
	TrackID       string `json:"trackId"`
	StreamID      string `json:"streamId"`
	Kind          string `json:"kind"`
}

// ErrorData is sent when an error occurs
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewServerMessage creates a new server message
func NewServerMessage(msgType MessageType, data interface{}) *ServerMessage {
	return &ServerMessage{
		Type: msgType,
		Data: data,
	}
}

// NewErrorMessage creates a new error message
func NewErrorMessage(code, message string) *ServerMessage {
	return NewServerMessage(MessageTypeError, &ErrorData{
		Code:    code,
		Message: message,
	})
}
