package gateway

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
	webrtchandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/webrtc"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"go.uber.org/zap"
)

// WebRTC admission (bugboard #726): the gateway side. The handlers admit a join
// against the namespace's own database; the SFUs report membership back here,
// and the namespace's functions admit, kick and mute through host calls.

var errNoPubSub = errors.New("this gateway has no pubsub client, so it cannot publish WebRTC membership events")

// webrtcEventSink is the internal URL the SFUs are told to report membership
// to: this gateway, on the overlay address it listens on. A gateway that does
// not listen on the overlay (a development gateway) has none, and says so.
func webrtcEventSink(listenAddr string, logger *logging.ColoredLogger) string {
	sink := "http://" + strings.TrimSpace(listenAddr)
	if _, _, err := net.SplitHostPort(strings.TrimSpace(listenAddr)); err != nil || ctrlauth.ValidateSink(sink) != nil {
		logger.ComponentWarn(logging.ComponentGeneral,
			"WebRTC membership events are off on this gateway: it does not listen on a WireGuard overlay address, which is where the SFUs report to",
			zap.String("listen_addr", listenAddr))
		return ""
	}
	return sink
}

// wireWebRTCAdmission gives the WebRTC handlers the namespace's admission store
// and the event publisher, and the host functions the controller behind
// webrtc_admit, webrtc_kick and webrtc_mute.
func (g *Gateway) wireWebRTCAdmission(cfg *Config, deps *Dependencies) {
	h := g.webrtcHandlers
	if deps.ORMClient != nil {
		h.SetAdmissionStore(webrtchandlers.NewAdmissionStore(deps.ORMClient))
	}
	ns := ownNamespace(cfg)
	h.SetNamespace(ns)
	h.SetEventSink(webrtcEventSink(cfg.ListenAddr, g.logger), func(ctx context.Context, topic string, data []byte) error {
		return g.publishPlatformEvent(ctx, ns, topic, data)
	})
	if deps.HostFuncs != nil {
		deps.HostFuncs.SetWebRTCController(h)
	}
}

// publishPlatformEvent publishes a message the platform itself wrote on the
// namespace's pubsub, and fires the triggers of functions that listen on it.
func (g *Gateway) publishPlatformEvent(ctx context.Context, ns, topic string, data []byte) error {
	if g.client == nil {
		return errNoPubSub
	}
	ctx = pubsub.WithNamespace(client.WithInternalAuth(ctx), ns)
	if err := g.client.PubSub().Publish(ctx, topic, data); err != nil {
		return err
	}
	if g.pubsubDispatcher != nil {
		go g.pubsubDispatcher.Dispatch(context.Background(), ns, topic, data, 0)
	}
	return nil
}
