package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
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

	listen := os.Getenv("PUBSUB_LISTEN")
	if listen == "" {
		listen = pubsub.DefaultListenAddr
	}
	identityPath := os.Getenv("IDENTITY_PATH")
	bootstrap := splitCSV(os.Getenv("BOOTSTRAP_PEERS"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
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

	gs, err := libp2ppubsub.NewGossipSub(ctx, h,
		libp2ppubsub.WithPeerExchange(true),
		libp2ppubsub.WithFloodPublish(true),
	)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "gossipsub", zap.Error(err))
		os.Exit(1)
	}

	for _, addr := range bootstrap {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			continue
		}
		_ = h.Connect(ctx, *info)
	}

	mgr := pubsub.NewManager(gs, "", logger.Logger)
	defer mgr.Close()

	srv := &http.Server{
		Addr:              listen,
		Handler:           pubsub.Handler(mgr, logger.Logger),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.ComponentInfo(logging.ComponentGeneral, "pubsub HTTP API listening",
			zap.String("addr", listen),
			zap.String("peer_id", h.ID().String()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
