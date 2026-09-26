package node

import (
	"fmt"
	mathrand "math/rand"
	"net"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/multiformats/go-multiaddr"
)

func extractIPFromMultiaddr(multiaddrStr string) string {
	ma, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return ""
	}

	var ip string
	var dnsName string
	multiaddr.ForEach(ma, func(c multiaddr.Component) bool {
		switch c.Protocol().Code {
		case multiaddr.P_IP4, multiaddr.P_IP6:
			ip = c.Value()
			return false
		case multiaddr.P_DNS4, multiaddr.P_DNS6, multiaddr.P_DNSADDR:
			dnsName = c.Value()
		}
		return true
	})

	if ip != "" {
		return ip
	}

	if dnsName != "" {
		if resolvedIPs, err := net.LookupIP(dnsName); err == nil && len(resolvedIPs) > 0 {
			for _, resolvedIP := range resolvedIPs {
				if resolvedIP.To4() != nil {
					return resolvedIP.String()
				}
			}
			return resolvedIPs[0].String()
		}
	}

	return ""
}

func calculateNextBackoff(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * 1.5)
	maxInterval := 10 * time.Minute
	if next > maxInterval {
		next = maxInterval
	}
	return next
}

func addJitter(interval time.Duration) time.Duration {
	jitterPercent := 0.2
	jitterRange := float64(interval) * jitterPercent
	jitter := (mathrand.Float64() - 0.5) * 2 * jitterRange
	result := time.Duration(float64(interval) + jitter)
	if result < time.Second {
		result = time.Second
	}
	return result
}

// readNodePeerID returns the libp2p peer id of the identity key install wrote
// to <dataDir>/identity.key.
func readNodePeerID(dataDir string) (string, error) {
	expanded, err := config.ExpandPath(dataDir)
	if err != nil {
		return "", fmt.Errorf("expand data dir %q: %w", dataDir, err)
	}
	identityFile := filepath.Join(expanded, "identity.key")
	info, err := encryption.LoadIdentity(identityFile)
	if err != nil {
		return "", fmt.Errorf("read this node's identity %s (written by `orama node install`): %w", identityFile, err)
	}
	return info.PeerID.String(), nil
}
