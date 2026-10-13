package sfu

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"github.com/pion/webrtc/v4"
)

const (
	// sfuTURNCredentialTTL (the 24h turn.DefaultCredentialTTL every one-shot
	// credential gets) is the lifetime of the TURN credential the SFU's own
	// PeerConnection authenticates with. The credential never leaves the SFU
	// (clients get their own, shorter one over the signaling socket), so a long
	// lifetime exposes nothing, and it keeps the SFU's relay allocation
	// refreshable for a whole call: pion's TURN client refreshes an allocation
	// with the credential it was created with, and the TURN server rejects an
	// expired one. See turn_refresh.go for what happens before it runs out.
	sfuTURNCredentialTTL = turn.DefaultCredentialTTL

	// sfuTURNRefreshInterval is when a session swaps the credential, well
	// before sfuTURNCredentialTTL ends.
	sfuTURNRefreshInterval = sfuTURNCredentialTTL / 5 * 4
)

// turnURIs lists the TURN URIs of servers: one turns: URI for a secure
// server, a UDP and a TCP turn: URI for the others.
func turnURIs(servers []TURNServerConfig) []string {
	var urls []string
	for _, ts := range servers {
		if ts.Secure {
			urls = append(urls, fmt.Sprintf("turns:%s:%d", ts.Host, ts.Port))
		} else {
			urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=udp", ts.Host, ts.Port))
			urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=tcp", ts.Host, ts.Port))
		}
	}
	return urls
}

// buildICEServers constructs the ICE server config of the SFU's own
// PeerConnections from TURN settings, with a credential valid for
// sfuTURNCredentialTTL from now.
func (r *Room) buildICEServers() []webrtc.ICEServer {
	if len(r.config.TURNServers) == 0 || r.config.TURNSecret == "" {
		return nil
	}

	username, password := turn.GenerateCredentials(r.config.TURNSecret, r.config.Namespace, sfuTURNCredentialTTL)

	return []webrtc.ICEServer{
		{
			URLs:       turnURIs(r.config.TURNServers),
			Username:   username,
			Credential: password,
		},
	}
}
