package sfu

import (
	"encoding/json"

	"github.com/pion/webrtc/v4"
)

// MessageType represents the type of signaling message
type MessageType string

const (
	// Client → Server
	MessageTypeJoin         MessageType = "join"
	MessageTypeLeave        MessageType = "leave"
	MessageTypeOffer        MessageType = "offer"
	MessageTypeAnswer       MessageType = "answer"
	MessageTypeICECandidate MessageType = "ice-candidate"

	// Server → Client
	MessageTypeWelcome            MessageType = "welcome"
	MessageTypeParticipantJoined  MessageType = "participant-joined"
	MessageTypeParticipantLeft    MessageType = "participant-left"
	MessageTypeTrackAdded         MessageType = "track-added"
	MessageTypeTrackRemoved       MessageType = "track-removed"
	MessageTypeTURNCredentials    MessageType = "turn-credentials"
	MessageTypeRefreshCredentials MessageType = "refresh-credentials"
	MessageTypeServerDraining     MessageType = "server-draining"
	MessageTypeError              MessageType = "error"
)

// ClientMessage is a message from client to server
type ClientMessage struct {
	Type MessageType     `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ServerMessage is a message from server to client
type ServerMessage struct {
	Type MessageType `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// JoinData is the payload for join messages
type JoinData struct {
	RoomID string `json:"roomId"`
	UserID string `json:"userId"`
}

// OfferData is the payload for SDP offer messages
type OfferData struct {
	SDP string `json:"sdp"`
}

// AnswerData is the payload for SDP answer messages
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

// ToWebRTCCandidate converts to pion ICECandidateInit
func (c *ICECandidateData) ToWebRTCCandidate() webrtc.ICECandidateInit {
	return webrtc.ICECandidateInit{
		Candidate:        c.Candidate,
		SDPMid:           &c.SDPMid,
		SDPMLineIndex:    &c.SDPMLineIndex,
		UsernameFragment: &c.UsernameFragment,
	}
}

// WelcomeData is sent when a peer successfully joins a room
type WelcomeData struct {
	PeerID       string            `json:"peerId"`
	RoomID       string            `json:"roomId"`
	Participants []ParticipantInfo `json:"participants"`
}

// ParticipantInfo is public info about a room participant
type ParticipantInfo struct {
	PeerID string `json:"peerId"`
	UserID string `json:"userId"`
}

// ParticipantJoinedData is sent when a new participant joins
type ParticipantJoinedData struct {
	Participant ParticipantInfo `json:"participant"`
}

// ParticipantLeftData is sent when a participant leaves
type ParticipantLeftData struct {
	PeerID string `json:"peerId"`
}

// TrackAddedData is sent when a new track is available
type TrackAddedData struct {
	PeerID   string `json:"peerId"`
	TrackID  string `json:"trackId"`
	StreamID string `json:"streamId"`
	Kind     string `json:"kind"` // "audio" or "video"
}

// TrackRemovedData is sent when a track is removed
type TrackRemovedData struct {
	PeerID   string `json:"peerId"`
	TrackID  string `json:"trackId"`
	Kind     string `json:"kind"`
}

// TURNCredentialsData provides TURN server credentials
type TURNCredentialsData struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	TTL      int      `json:"ttl"`
	URIs     []string `json:"uris"`
}

// ServerDrainingData warns clients the server is shutting down
type ServerDrainingData struct {
	Reason    string `json:"reason"`
	TimeoutMs int    `json:"timeoutMs"`
}

// ErrorData is sent when an error occurs
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewServerMessage creates a new server message
func NewServerMessage(msgType MessageType, data interface{}) *ServerMessage {
	return &ServerMessage{Type: msgType, Data: data}
}

// NewErrorMessage creates a new error message
func NewErrorMessage(code, message string) *ServerMessage {
	return NewServerMessage(MessageTypeError, &ErrorData{Code: code, Message: message})
}
