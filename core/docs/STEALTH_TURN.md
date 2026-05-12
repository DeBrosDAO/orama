# Stealth TURN Deployment Guide

## What this is

A TLS-level SNI router that lets Orama serve TURN-over-TLS on `:443`,
sharing the port with Caddy HTTPS. From a network observer's
perspective, TURN traffic is indistinguishable from ordinary HTTPS —
useful for users in regions that block standard VoIP ports (UAE, Saudi
Arabia, China, Iran).

## Architecture

```
                    Internet
                        │
                        ▼
                  TCP :443
                        │
              ┌─────────┴─────────┐
              │  orama-sni-router │   peeks SNI, forwards bytes
              └─────────┬─────────┘
                        │
        ┌───────────────┼────────────────┐
        ▼                                ▼
  cdn.<base>                  *.<base>, <base>
  turn.<base>                  (everything else)
        │                                │
        ▼                                ▼
  Pion TURN-TLS                       Caddy
  127.0.0.1:5349                  127.0.0.1:8443
   (existing)                      (moved from :443)
```

The router does **not** terminate TLS. It reads the unencrypted TLS
ClientHello (first ~5 KB), inspects the SNI extension, and dials the
matching backend. Encrypted bytes pass through verbatim.

## Components

- **Library:** `pkg/sniproxy/` — ClientHello parser, route table, TCP server
- **Binary:** `cmd/sni-router/` (built as `bin/orama-sni-router`)
- **Systemd unit:** `systemd/orama-sni-router.service`
- **Config:** `~/.orama/sni-router.yaml`

## Deployment cutover

⚠️ **This change touches production `:443`. Stage on one node first, watch for 24h, then roll out.**

### 1. Reconfigure Caddy to listen on `:8443`

Update wherever the Caddy config is generated (`pkg/environments/production/installers/caddy.go`)
so Caddy binds `:8443` (HTTPS) and `:8080` (HTTP) instead of `:443` and `:80`.

Drop `CAP_NET_BIND_SERVICE` from Caddy's systemd unit — it no longer needs privileged ports.

### 2. Provision the cert SAN for `cdn.<base-domain>`

Caddy's automatic Let's Encrypt flow needs to issue a cert covering
`cdn.<base-domain>` and `cdn.ns-*.<base-domain>` so Pion TURN can read it
on startup. Add these names to Caddy's TLS config block.

### 3. Drop `sni-router.yaml` config

Example for a single-namespace node:

```yaml
listen: ":443"
client_hello_timeout: 5s
backend_dial_timeout: 5s
max_concurrent_conns: 10000
fallback:
  name: caddy
  addr: "127.0.0.1:8443"
routes:
  - match: "cdn.example.com"
    backend:
      name: turn-tls
      addr: "127.0.0.1:5349"
  - match: "turn.example.com"
    backend:
      name: turn-tls
      addr: "127.0.0.1:5349"
```

For multi-namespace, add per-namespace TURN backends (each namespace's
TURN-TLS port is allocated by `pkg/namespace`):

```yaml
  - match: "cdn.ns-myapp.example.com"
    backend: { name: "turn-myapp", addr: "127.0.0.1:5349" }
  - match: "cdn.ns-other.example.com"
    backend: { name: "turn-other", addr: "127.0.0.1:5350" }
```

### 4. Deploy + start in order

```bash
# Install binary
sudo cp bin-linux/orama-sni-router /opt/orama/bin/

# Install service
sudo cp systemd/orama-sni-router.service /etc/systemd/system/
sudo systemctl daemon-reload

# Stop Caddy briefly (it's about to lose :443)
sudo systemctl stop caddy

# Start the SNI router (it takes :443)
sudo systemctl enable --now orama-sni-router

# Restart Caddy on its new port
sudo systemctl start caddy

# Verify
curl -v https://cdn.<base>:443  # should hit TURN backend (TLS handshake will fail; that's fine)
curl -v https://<base>:443      # should hit Caddy (normal HTTPS response)
```

### 5. Enable stealth in the gateway

Once the SNI router is live, tell the gateway to advertise the stealth URI:

```go
// in gateway dependencies / startup
webrtcHandlers.SetStealthCDNDomain("cdn.<base-domain>")
```

The credentials handler will start including `turns:cdn.<base-domain>:443`
in `POST /v1/webrtc/turn/credentials` responses automatically.

### 6. Monitor

```bash
journalctl -u orama-sni-router.service -f
journalctl -u caddy.service -f
```

Watch for:
- `Connection limit reached` warnings (bump `max_concurrent_conns`)
- `backend dial failed` warnings (Caddy isn't listening on `:8443`, or TURN isn't on `:5349`)
- `ClientHello peek failed` debugs (curious clients sending non-TLS to `:443` — usually port scanners)

## Rollback

If anything is wrong:

```bash
sudo systemctl stop orama-sni-router
# Reconfigure Caddy back to :443 and restart
sudo systemctl restart caddy
```

Caddy reclaiming `:443` from the disabled router is the fastest way back to
the previous topology.

## Known gaps

- **Dynamic route source:** today's router reads YAML once at startup. To
  pick up new namespaces without restart, implement a `RouteSource` that
  polls `pkg/namespace` for active TURN deployments. The library is
  already designed for `Router.Replace` to be called concurrently.
- **TLS cert hot-reload:** Pion TURN reads the cert once at startup. When
  Caddy renews `cdn.<base-domain>`, Pion needs to be restarted to pick up
  the new cert. A small file-watcher service (or a periodic restart in
  off-peak hours) handles this for now.

## What clients see

Once enabled, the credentials response gains one entry:

```json
{
  "username": "...",
  "password": "...",
  "ttl": 600,
  "uris": [
    "turn:turn.example.com:3478?transport=udp",
    "turn:turn.example.com:3478?transport=tcp",
    "turns:turn.example.com:5349",
    "turns:cdn.example.com:443"
  ]
}
```

Browsers iterate ICE candidates; users in restricted regions will silently
succeed via the `:443` URI when others fail. No client-side change is
required.
