package ntfy

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/push"
)

// A transport failure must not carry the URL the provider posted to: for a
// UnifiedPush endpoint that URL holds the device's topic, and the error text
// is reported back to the function that sent the push (FEAT-265).
func TestSend_transportErrorOmitsTheTopic(t *testing.T) {
	// A listener that is closed at once: every post to it is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	const topic = "upSecretTopic0123456789"
	p := New(Config{BaseURL: base}, nil)
	for name, token := range map[string]string{
		"bare topic":           topic,
		"UnifiedPush endpoint": base + "/" + topic + "?up=1",
	} {
		t.Run(name, func(t *testing.T) {
			sendErr := p.Send(context.Background(), push.PushMessage{DeviceToken: token, Body: "x"})
			if sendErr == nil {
				t.Fatal("post to a closed port succeeded")
			}
			if strings.Contains(sendErr.Error(), topic) {
				t.Errorf("error carries the topic: %v", sendErr)
			}
			var opErr *net.OpError
			if !errors.As(sendErr, &opErr) {
				t.Errorf("the transport cause was lost: %v", sendErr)
			}
		})
	}
}
