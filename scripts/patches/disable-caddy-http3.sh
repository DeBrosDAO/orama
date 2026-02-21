#!/usr/bin/env bash
# Patch: Disable HTTP/3 (QUIC) in Caddy to free UDP 443 for TURN server.
# Run on each VPS node. Safe to run multiple times (idempotent).
#
# Usage: sudo bash disable-caddy-http3.sh
set -euo pipefail

CADDYFILE="/etc/caddy/Caddyfile"

if [ ! -f "$CADDYFILE" ]; then
    echo "ERROR: $CADDYFILE not found"
    exit 1
fi

# Check if already patched
if grep -q 'protocols h1 h2' "$CADDYFILE"; then
    echo "Already patched — Caddyfile already has 'protocols h1 h2'"
else
    # The global block looks like:
    #   {
    #       email admin@...
    #   }
    #
    # Insert 'servers { protocols h1 h2 }' after the email line.
    sed -i '/^    email /a\
    servers {\
        protocols h1 h2\
    }' "$CADDYFILE"
    echo "Patched Caddyfile — added 'servers { protocols h1 h2 }'"
fi

# Validate the new config before reloading
if ! caddy validate --config "$CADDYFILE" --adapter caddyfile 2>/dev/null; then
    echo "ERROR: Caddyfile validation failed! Reverting..."
    sed -i '/^    servers {$/,/^    }$/d' "$CADDYFILE"
    exit 1
fi

# Reload Caddy (graceful, no downtime)
systemctl reload caddy
echo "Caddy reloaded successfully"

# Verify UDP 443 is no longer bound by Caddy
sleep 1
if ss -ulnp | grep -q ':443.*caddy'; then
    echo "WARNING: Caddy still binding UDP 443 — reload may need more time"
else
    echo "Confirmed: UDP 443 is free for TURN"
fi
