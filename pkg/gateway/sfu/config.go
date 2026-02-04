package sfu

import (
	"time"

	"github.com/pion/webrtc/v4"
)

// Config holds SFU configuration
type Config struct {
	// MaxParticipants is the maximum number of participants per room
	MaxParticipants int

	// MediaTimeout is the timeout for media operations
	MediaTimeout time.Duration

	// ICEServers are the ICE servers for WebRTC connections
	ICEServers []webrtc.ICEServer
}

// DefaultConfig returns a default SFU configuration
func DefaultConfig() *Config {
	return &Config{
		MaxParticipants: 10,
		MediaTimeout:    30 * time.Second,
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
}

// NewMediaEngine creates a MediaEngine with supported codecs for the SFU
func NewMediaEngine() (*webrtc.MediaEngine, error) {
	m := &webrtc.MediaEngine{}

	// Register Opus codec for audio
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Register VP8 codec for video
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeVP8,
			ClockRate: 90000,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register H264 codec for video (fallback)
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	return m, nil
}

// NewWebRTCAPI creates a new WebRTC API with the configured MediaEngine
func NewWebRTCAPI() (*webrtc.API, error) {
	mediaEngine, err := NewMediaEngine()
	if err != nil {
		return nil, err
	}

	// Create interceptor registry for RTCP feedback
	// This enables features like NACK, PLI, and REMB
	settingEngine := webrtc.SettingEngine{}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(settingEngine),
	), nil
}

// GetRTPCapabilities returns the RTP capabilities of the SFU
// This is used by clients to negotiate codecs
func GetRTPCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"codecs": []map[string]interface{}{
			{
				"kind":      "audio",
				"mimeType":  "audio/opus",
				"clockRate": 48000,
				"channels":  2,
			},
			{
				"kind":      "video",
				"mimeType":  "video/VP8",
				"clockRate": 90000,
			},
			{
				"kind":      "video",
				"mimeType":  "video/H264",
				"clockRate": 90000,
			},
		},
	}
}
