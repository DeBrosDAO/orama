package report

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// collectWireGuard gathers WireGuard interface status, peer information,
// and configuration details using local commands and sysfs.
func collectWireGuard() *WireGuardReport {
	r := &WireGuardReport{}

	// 1. ServiceActive: check if wg-quick@wg0 systemd service is active
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "wg-quick@wg0"); err == nil {
			r.ServiceActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. InterfaceUp: check if /sys/class/net/wg0 exists
	if _, err := os.Stat("/sys/class/net/wg0"); err == nil {
		r.InterfaceUp = true
	}

	// If interface is not up, return partial data early.
	if !r.InterfaceUp {
		// Still check config existence even if interface is down.
		if _, err := os.Stat("/etc/wireguard/wg0.conf"); err == nil {
			r.ConfigExists = true

			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if out, err := runCmd(ctx, "stat", "-c", "%a", "/etc/wireguard/wg0.conf"); err == nil {
				r.ConfigPerms = strings.TrimSpace(out)
			}
		}
		return r
	}

	// 3. WgIP: extract IP from `ip -4 addr show wg0`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "ip", "-4", "addr", "show", "wg0"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "inet ") {
					// Line format: "inet X.X.X.X/Y scope ..."
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						// Extract just the IP, strip the /prefix
						ip := fields[1]
						if idx := strings.Index(ip, "/"); idx != -1 {
							ip = ip[:idx]
						}
						r.WgIP = ip
					}
					break
				}
			}
		}
	}

	// 4. MTU: read /sys/class/net/wg0/mtu
	if data, err := os.ReadFile("/sys/class/net/wg0/mtu"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			r.MTU = n
		}
	}

	// 5. ListenPort: parse from `wg show wg0 listen-port`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "wg", "show", "wg0", "listen-port"); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.ListenPort = n
			}
		}
	}

	// 6. ConfigExists: check if /etc/wireguard/wg0.conf exists
	if _, err := os.Stat("/etc/wireguard/wg0.conf"); err == nil {
		r.ConfigExists = true
	}

	// 7. ConfigPerms: run `stat -c '%a' /etc/wireguard/wg0.conf`
	if r.ConfigExists {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "stat", "-c", "%a", "/etc/wireguard/wg0.conf"); err == nil {
			r.ConfigPerms = strings.TrimSpace(out)
		}
	}

	// 8. Peers: run `wg show wg0 dump` and parse peer lines
	//    Line 1: interface (private_key, public_key, listen_port, fwmark)
	//    Line 2+: peers (public_key, preshared_key, endpoint, allowed_ips,
	//             latest_handshake, transfer_rx, transfer_tx, persistent_keepalive)
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "wg", "show", "wg0", "dump"); err == nil {
			lines := strings.Split(out, "\n")
			now := time.Now().Unix()
			for i, line := range lines {
				if i == 0 {
					// Skip interface line
					continue
				}
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				fields := strings.Split(line, "\t")
				if len(fields) < 8 {
					continue
				}

				peer := WGPeerInfo{
					PublicKey:  fields[0],
					Endpoint:   fields[2],
					AllowedIPs: fields[3],
				}

				// LatestHandshake: unix timestamp (0 = never)
				if ts, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
					peer.LatestHandshake = ts
					if ts > 0 {
						peer.HandshakeAgeSec = now - ts
					}
				}

				// TransferRx
				if n, err := strconv.ParseInt(fields[5], 10, 64); err == nil {
					peer.TransferRx = n
				}

				// TransferTx
				if n, err := strconv.ParseInt(fields[6], 10, 64); err == nil {
					peer.TransferTx = n
				}

				// PersistentKeepalive
				if fields[7] != "off" {
					if n, err := strconv.Atoi(fields[7]); err == nil {
						peer.Keepalive = n
					}
				}

				r.Peers = append(r.Peers, peer)
			}
			r.PeerCount = len(r.Peers)
		}
	}

	return r
}
