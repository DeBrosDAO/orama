package main

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/wireguard"
	"github.com/libp2p/go-libp2p"
	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

func main() {
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, true)
	if err != nil {
		panic(err)
	}

	identityPath := os.Getenv("IDENTITY_PATH")
	bootstrap := splitCSV(os.Getenv("BOOTSTRAP_PEERS"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listenAddr, err := overlayListenAddr(wireguard.GetIP)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "pubsub libp2p listener", zap.Error(err))
		os.Exit(1)
	}
	bootstrapPeers := parseBootstrap(bootstrap, logger.Logger)
	gater := pubsub.NewOverlayGater(constants.WireGuardOverlay())
	for _, info := range bootstrapPeers {
		gater.Allow(info.ID)
	}
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenAddr),
		libp2p.ConnectionGater(gater),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultMuxers,
	}
	if identityPath != "" {
		id, err := encryption.LoadIdentity(identityPath)
		if err != nil {
			id, err = encryption.GenerateIdentity()
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "generate pubsub identity", zap.Error(err))
				os.Exit(1)
			}
			if err := encryption.SaveIdentity(id, identityPath); err != nil {
				logger.ComponentError(logging.ComponentGeneral, "save pubsub identity", zap.Error(err))
				os.Exit(1)
			}
		}
		opts = append(opts, libp2p.Identity(id.PrivateKey))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "libp2p host", zap.Error(err))
		os.Exit(1)
	}
	defer h.Close()

	gs, err := libp2ppubsub.NewGossipSub(ctx, h, gossipSubOptions()...)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "gossipsub", zap.Error(err))
		os.Exit(1)
	}

	for _, info := range bootstrapPeers {
		if err := h.Connect(ctx, info); err != nil {
			logger.ComponentWarn(logging.ComponentGeneral, "pubsub bootstrap peer unreachable",
				zap.String("peer_id", info.ID.String()), zap.Error(err))
		}
	}

	mgr := pubsub.NewManager(gs, "", logger.Logger)
	defer mgr.Close()

	ln, err := pubsub.ListenSocket(pubsub.DefaultSocketPath, logger.Logger)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "pubsub API socket", zap.Error(err))
		os.Exit(1)
	}
	srv := &http.Server{
		Handler:           pubsub.Handler(mgr, logger.Logger, pubsub.WithMesh(pubsub.NewMesh(h, constants.WireGuardOverlay(), gater))),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.ComponentInfo(logging.ComponentGeneral, "pubsub HTTP API listening",
			zap.String("socket", pubsub.DefaultSocketPath),
			zap.Strings("libp2p", multiaddrStrings(h.Addrs())),
			zap.String("peer_id", h.ID().String()))
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.ComponentError(logging.ComponentGeneral, "pubsub http", zap.Error(err))
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	_ = srv.Shutdown(shCtx)
}

// gossipSubOptions configures GossipSub. Peer exchange stays off: it lets a
// pruning peer hand this host addresses to dial, and the only discovery the
// service accepts is the registry mesh (pkg/pubsub Mesh), which the gateway
// feeds and the connection gate bounds.
func gossipSubOptions() []libp2ppubsub.Option {
	return []libp2ppubsub.Option{
		libp2ppubsub.WithPeerExchange(false),
		libp2ppubsub.WithFloodPublish(true),
	}
}

// parseBootstrap is the node libp2p hosts the service bootstraps from. An
// address that does not name a peer is logged and skipped; the rest are still
// used.
func parseBootstrap(addrs []string, logger *zap.Logger) []peer.AddrInfo {
	out := make([]peer.AddrInfo, 0, len(addrs))
	for _, addr := range addrs {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			logger.Warn("pubsub bootstrap address ignored", zap.String("addr", addr), zap.Error(err))
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			logger.Warn("pubsub bootstrap address names no peer", zap.String("addr", addr), zap.Error(err))
			continue
		}
		out = append(out, *info)
	}
	return out
}

// overlayListenAddr is where the pubsub libp2p host listens: this node's
// WireGuard address, on a port the OS picks. It listened on 0.0.0.0, which
// put it on the public interface with only the firewall in front of it. The
// peers that dial it are other nodes' pubsub hosts, over the mesh. With no
// overlay address this is an error — listening on every interface instead is
// the exposure this closes.
func overlayListenAddr(overlayIP func() (string, error)) (string, error) {
	ip, err := overlayIP()
	if err != nil {
		return "", fmt.Errorf("pubsub listens on this node's WireGuard address, and it has none: %w", err)
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !constants.WireGuardOverlay().Contains(addr) {
		return "", fmt.Errorf("pubsub listens on this node's WireGuard address, and %q is not one inside %s", ip, constants.WireGuardSubnet)
	}
	return fmt.Sprintf("/ip4/%s/tcp/0", addr), nil
}

func multiaddrStrings(addrs []multiaddr.Multiaddr) []string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	return out
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
