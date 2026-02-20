package sfu

import (
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/interceptor/pkg/nack"
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

	// RTCP feedback for video codecs - enables NACK, PLI, FIR
	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},           // Bandwidth estimation
		{Type: "ccm", Parameter: "fir"},              // Full Intra Request
		{Type: "nack", Parameter: ""},                // Generic NACK
		{Type: "nack", Parameter: "pli"},             // Picture Loss Indication
	}

	// Register Opus codec for audio with NACK support
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

	// Register VP8 codec for video with full RTCP feedback
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register RTX for VP8 (retransmission)
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    "video/rtx",
			ClockRate:   90000,
			SDPFmtpLine: "apt=96",
		},
		PayloadType: 97,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register H264 codec for video with full RTCP feedback (fallback)
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register RTX for H264 (retransmission)
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    "video/rtx",
			ClockRate:   90000,
			SDPFmtpLine: "apt=102",
		},
		PayloadType: 103,
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
	i := &interceptor.Registry{}

	// Register NACK responder - handles retransmission requests from receivers
	// This is critical for video quality: when a receiver loses a packet,
	// it sends NACK and the sender retransmits the lost packet
	nackResponderFactory, err := nack.NewResponderInterceptor()
	if err != nil {
		return nil, err
	}
	i.Add(nackResponderFactory)

	// Register NACK generator - sends NACK when we detect packet loss as receiver
	nackGeneratorFactory, err := nack.NewGeneratorInterceptor()
	if err != nil {
		return nil, err
	}
	i.Add(nackGeneratorFactory)

	// Register interval PLI - automatically sends PLI periodically for video tracks
	// This helps new receivers get keyframes faster
	intervalPLIFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		return nil, err
	}
	i.Add(intervalPLIFactory)

	// Configure settings for better media performance
	settingEngine := webrtc.SettingEngine{}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(i),
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
