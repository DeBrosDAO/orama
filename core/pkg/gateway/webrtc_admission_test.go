package gateway

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func TestWebRTCEventSink(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	cases := map[string]string{
		"10.0.0.5:10004":   "http://10.0.0.5:10004",
		" 10.0.0.5:10004 ": "http://10.0.0.5:10004",
		":10004":           "", // every interface: not an address the SFU can be told to dial
		"0.0.0.0:10004":    "",
		"127.0.0.1:6001":   "", // loopback is not the overlay
		"203.0.113.9:443":  "",
		"not an address":   "",
		"":                 "",
	}
	for listen, want := range cases {
		if got := webrtcEventSink(listen, logger); got != want {
			t.Errorf("webrtcEventSink(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestPublishPlatformEvent_withoutAPubSubClientIsAnError(t *testing.T) {
	g := &Gateway{}
	if err := g.publishPlatformEvent(context.Background(), "ns", "_orama/webrtc/r", []byte(`{}`)); err == nil {
		t.Fatal("an event was reported published with no pubsub client")
	}
}
