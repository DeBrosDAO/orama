package client

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// peerRedialTimeout bounds one dial of a bootstrap peer by the maintenance
// loop, so a peer that swallows packets does not hold up the next one.
const peerRedialTimeout = 10 * time.Second

// forceDialReason is the reason the swarm's dial backoff is bypassed for.
const forceDialReason = "bootstrap peer maintenance"

// peerRedialInterval is how often the maintenance loop looks for a bootstrap
// peer that is not connected. A variable so a test need not wait for it.
var peerRedialInterval = 5 * time.Second

// startPeerMaintenance keeps the host connected to the configured bootstrap
// peers for as long as the client is connected. Connect dials them once; a
// peer that was not listening yet (a gateway starts in the same second as the
// node it bootstraps from, and a node restart drops every connection to it)
// would otherwise leave the client with no peer until the process restarted.
// Disconnect stops the loop. The caller holds c.mu.
func (c *Client) startPeerMaintenance(h host.Host) {
	targets := bootstrapTargets(c.config.BootstrapPeers, h.ID(), c.logger)
	if len(targets) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.stopPeers, c.peersDone = cancel, done
	go func() {
		defer close(done)
		c.maintainPeers(ctx, h, targets)
	}()
}

// stopPeerMaintenance ends the loop and waits for it. The loop never takes
// c.mu, so the caller may hold it.
func (c *Client) stopPeerMaintenance() {
	if c.stopPeers == nil {
		return
	}
	c.stopPeers()
	<-c.peersDone
	c.stopPeers, c.peersDone = nil, nil
}

// bootstrapTargets parses the bootstrap addresses that name a peer other than
// self. An address that does not parse or names no peer is skipped: Connect
// has already reported it.
func bootstrapTargets(addrs []string, self peer.ID, logger *zap.Logger) []peer.AddrInfo {
	var targets []peer.AddrInfo
	for _, addr := range addrs {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			logger.Debug("Bootstrap address is not a multiaddr, not maintained", zap.String("addr", addr), zap.Error(err))
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil || info.ID == self {
			continue
		}
		targets = append(targets, *info)
	}
	return targets
}

// maintainPeers redials every target that is not connected, every
// peerRedialInterval, until ctx ends. It logs a peer when it is lost and when
// it is back, not on every failed attempt.
func (c *Client) maintainPeers(ctx context.Context, h host.Host, targets []peer.AddrInfo) {
	down := make(map[peer.ID]bool, len(targets))
	ticker := time.NewTicker(peerRedialInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, t := range targets {
			c.redial(ctx, h, t, down)
		}
	}
}

// redial connects h to t if it is not connected and records the transition in
// down.
func (c *Client) redial(ctx context.Context, h host.Host, t peer.AddrInfo, down map[peer.ID]bool) {
	if h.Network().Connectedness(t.ID) != network.Connected {
		dialCtx, cancel := context.WithTimeout(ctx, peerRedialTimeout)
		// The swarm backs a failed peer off for 5s plus n^2 seconds, up to five
		// minutes, and refuses dials meanwhile; this loop is the pacing of its
		// own retries, so the backoff would only delay the peer's return.
		err := h.Connect(network.WithForceDirectDial(dialCtx, forceDialReason), t)
		cancel()
		if err != nil {
			if ctx.Err() == nil && !down[t.ID] {
				down[t.ID] = true
				c.logger.Warn("Bootstrap peer is not connected, redialing until it answers",
					zap.String("peer", t.ID.String()), zap.Error(err))
			}
			return
		}
	}
	if down[t.ID] {
		delete(down, t.ID)
		c.logger.Info("Bootstrap peer connected again", zap.String("peer", t.ID.String()))
	}
}
