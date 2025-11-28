package client

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// connectToPeer connects to a peer address
func (c *Client) connectToPeer(ctx context.Context, addr string) error {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Try to extract peer info if it's a full multiaddr with peer ID
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		// If there's no peer ID, we can't connect
		c.logger.Warn("Peer address missing peer ID, skipping",
			zap.String("addr", addr))
		return nil
	}

	// Avoid dialing ourselves: if the peer address resolves to our own peer ID, skip.
	if c.host != nil && peerInfo.ID == c.host.ID() {
		c.logger.Debug("Skipping peer address because it resolves to self",
			zap.String("addr", addr),
			zap.String("peer_id", peerInfo.ID.String()))
		return nil
	}

	// Attempt connection
	if err := c.host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	c.logger.Debug("Connected to peer",
		zap.String("peer_id", peerInfo.ID.String()),
		zap.String("addr", addr))

	return nil
}
