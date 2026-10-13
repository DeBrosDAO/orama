package serverless

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	"go.uber.org/zap"
)

type optinPubSub struct{ handler pubsub.MessageHandler }

func (p *optinPubSub) Subscribe(_ context.Context, _ string, h pubsub.MessageHandler) error {
	p.handler = h
	return nil
}
func (p *optinPubSub) Unsubscribe(context.Context, string) error { return nil }

type optinSender struct{ frames []string }

func (s *optinSender) Send(_ string, data []byte) error {
	s.frames = append(s.frames, string(data))
	return nil
}

func deliveredFrame(t *testing.T, target string) string {
	t.Helper()
	ps, ws := &optinPubSub{}, &optinSender{}
	h := &ServerlessHandlers{wsBridge: wsbridge.New(ps, ws, zap.NewNop())}

	h.registerBridgeClient(httptest.NewRequest("GET", target, nil), "c1", "ns")
	if err := h.wsBridge.Add(context.Background(), "ns", "c1", "chat"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_ = ps.handler("chat", []byte("hello"))

	if len(ws.frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(ws.frames))
	}
	return ws.frames[0]
}

func TestRegisterBridgeClient_stamped_query_opts_in(t *testing.T) {
	got := deliveredFrame(t, "/v1/functions/f/ws?pubsub_delivery=stamped")
	if !strings.Contains(got, `"_orama":"pubsub.message"`) || !strings.Contains(got, `"topic":"chat"`) {
		t.Errorf("frame %q is not a stamped delivery", got)
	}
}

func TestRegisterBridgeClient_no_query_keeps_raw_bytes(t *testing.T) {
	if got := deliveredFrame(t, "/v1/functions/f/ws"); got != "hello" {
		t.Errorf("frame %q, want the raw bytes", got)
	}
}

func TestRegisterBridgeClient_unknown_value_keeps_raw_bytes(t *testing.T) {
	if got := deliveredFrame(t, "/v1/functions/f/ws?pubsub_delivery=yes"); got != "hello" {
		t.Errorf("frame %q, want the raw bytes", got)
	}
}

func TestRegisterBridgeClient_without_a_bridge_is_a_no_op(t *testing.T) {
	h := &ServerlessHandlers{}
	h.registerBridgeClient(httptest.NewRequest("GET", "/ws?pubsub_delivery=stamped", nil), "c1", "ns")
}
