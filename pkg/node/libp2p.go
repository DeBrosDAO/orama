package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/libp2p/go-libp2p"
	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// startLibP2P initializes the LibP2P host
func (n *Node) startLibP2P() error {
	n.logger.ComponentInfo(logging.ComponentLibP2P, "Starting LibP2P host")

	// Load or create persistent identity
	identity, err := n.loadOrCreateIdentity()
	if err != nil {
		return fmt.Errorf("failed to load identity: %w", err)
	}

	// Create LibP2P host with explicit listen addresses
	var opts []libp2p.Option
	opts = append(opts,
		libp2p.Identity(identity),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultMuxers,
	)

	// Add explicit listen addresses from config
	if len(n.config.Node.ListenAddresses) > 0 {
		listenAddrs := make([]multiaddr.Multiaddr, 0, len(n.config.Node.ListenAddresses))
		for _, addr := range n.config.Node.ListenAddresses {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				return fmt.Errorf("invalid listen address %s: %w", addr, err)
			}
			listenAddrs = append(listenAddrs, ma)
		}
		opts = append(opts, libp2p.ListenAddrs(listenAddrs...))
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Configured listen addresses",
			zap.Strings("addrs", n.config.Node.ListenAddresses))
	}

	// For localhost/development, disable NAT services
	isLocalhost := len(n.config.Node.ListenAddresses) > 0 &&
		(strings.Contains(n.config.Node.ListenAddresses[0], "localhost") ||
			strings.Contains(n.config.Node.ListenAddresses[0], "127.0.0.1"))

	if isLocalhost {
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Localhost detected - disabling NAT services for local development")
	} else {
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Production mode - enabling NAT services")
		opts = append(opts,
			libp2p.EnableNATService(),
			libp2p.EnableAutoNATv2(),
			libp2p.EnableRelay(),
			libp2p.NATPortMap(),
			libp2p.EnableAutoRelayWithPeerSource(
				peerSource(n.config.Discovery.BootstrapPeers, n.logger.Logger),
			),
		)
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return err
	}

	n.host = h

	// Initialize pubsub
	ps, err := libp2ppubsub.NewGossipSub(context.Background(), h,
		libp2ppubsub.WithPeerExchange(true),
		libp2ppubsub.WithFloodPublish(true),
		libp2ppubsub.WithDirectPeers(nil),
	)
	if err != nil {
		return fmt.Errorf("failed to create pubsub: %w", err)
	}

	// Create pubsub adapter
	n.pubsub = pubsub.NewClientAdapter(ps, n.config.Discovery.NodeNamespace)
	n.logger.Info("Initialized pubsub adapter on namespace", zap.String("namespace", n.config.Discovery.NodeNamespace))

	// Connect to peers
	if err := n.connectToPeers(context.Background()); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect to peers", zap.Error(err))
	}

	// Start reconnection loop
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		peerCtx, cancel := context.WithCancel(context.Background())
		n.peerDiscoveryCancel = cancel

		go n.peerReconnectionLoop(peerCtx)
	}

	// Add peers to peerstore
	for _, peerAddr := range n.config.Discovery.BootstrapPeers {
		if ma, err := multiaddr.NewMultiaddr(peerAddr); err == nil {
			if peerInfo, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
				n.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour*24)
			}
		}
	}

	// Initialize discovery manager
	n.discoveryManager = discovery.NewManager(h, nil, n.logger.Logger)
	n.discoveryManager.StartProtocolHandler()

	n.logger.ComponentInfo(logging.ComponentNode, "LibP2P host started successfully")

	// Start peer discovery
	n.startPeerDiscovery()

	return nil
}

func (n *Node) peerReconnectionLoop(ctx context.Context) {
	interval := 5 * time.Second
	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !n.hasPeerConnections() {
			if err := n.connectToPeers(context.Background()); err != nil {
				consecutiveFailures++
				jitteredInterval := addJitter(interval)
				
				select {
				case <-ctx.Done():
					return
				case <-time.After(jitteredInterval):
				}

				interval = calculateNextBackoff(interval)
			} else {
				interval = 5 * time.Second
				consecutiveFailures = 0

				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
				}
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}
}

func (n *Node) connectToPeers(ctx context.Context) error {
	for _, peerAddr := range n.config.Discovery.BootstrapPeers {
		if err := n.connectToPeerAddr(ctx, peerAddr); err != nil {
			continue
		}
	}
	return nil
}

func (n *Node) connectToPeerAddr(ctx context.Context, addr string) error {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return err
	}
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return err
	}
	if n.host != nil && peerInfo.ID == n.host.ID() {
		return nil
	}
	return n.host.Connect(ctx, *peerInfo)
}

func (n *Node) hasPeerConnections() bool {
	if n.host == nil || len(n.config.Discovery.BootstrapPeers) == 0 {
		return false
	}
	connectedPeers := n.host.Network().Peers()
	if len(connectedPeers) == 0 {
		return false
	}

	bootstrapIDs := make(map[peer.ID]bool)
	for _, addr := range n.config.Discovery.BootstrapPeers {
		if ma, err := multiaddr.NewMultiaddr(addr); err == nil {
			if info, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
				bootstrapIDs[info.ID] = true
			}
		}
	}

	for _, p := range connectedPeers {
		if bootstrapIDs[p] {
			return true
		}
	}
	return false
}

func (n *Node) loadOrCreateIdentity() (crypto.PrivKey, error) {
	identityFile := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "identity.key")
	if strings.HasPrefix(identityFile, "~") {
		home, _ := os.UserHomeDir()
		identityFile = filepath.Join(home, identityFile[1:])
	}

	if _, err := os.Stat(identityFile); err == nil {
		info, err := encryption.LoadIdentity(identityFile)
		if err == nil {
			return info.PrivateKey, nil
		}
	}

	info, err := encryption.GenerateIdentity()
	if err != nil {
		return nil, err
	}
	if err := encryption.SaveIdentity(info, identityFile); err != nil {
		return nil, err
	}
	return info.PrivateKey, nil
}

func (n *Node) startPeerDiscovery() {
	if n.discoveryManager == nil {
		return
	}
	discoveryConfig := discovery.Config{
		DiscoveryInterval: n.config.Discovery.DiscoveryInterval,
		MaxConnections:    n.config.Node.MaxConnections,
	}
	n.discoveryManager.Start(discoveryConfig)
}

func (n *Node) stopPeerDiscovery() {
	if n.discoveryManager != nil {
		n.discoveryManager.Stop()
	}
}

func (n *Node) GetPeerID() string {
	if n.host == nil {
		return ""
	}
	return n.host.ID().String()
}

func peerSource(peerAddrs []string, logger *zap.Logger) func(context.Context, int) <-chan peer.AddrInfo {
	return func(ctx context.Context, num int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo, num)
		go func() {
			defer close(out)
			count := 0
			for _, s := range peerAddrs {
				if count >= num {
					return
				}
				ma, err := multiaddr.NewMultiaddr(s)
				if err != nil {
					continue
				}
				ai, err := peer.AddrInfoFromP2pAddr(ma)
				if err != nil {
					continue
				}
				select {
				case out <- *ai:
					count++
				case <-ctx.Done():
					return
				}
			}
		}()
		return out
	}
}

