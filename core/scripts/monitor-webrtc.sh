#!/usr/bin/env bash
# Monitor WebRTC endpoints every 10 seconds
# Usage: ./scripts/monitor-webrtc.sh

API_KEY="ak_SiODBDJFHrfE3HxjOtSe4CYm:anchat-test"
BASE="https://ns-anchat-test.orama-devnet.network"

while true; do
  TS=$(date '+%H:%M:%S')

  # 1. Health check
  HEALTH=$(curl -sk -o /dev/null -w "%{http_code}" "${BASE}/v1/health")

  # 2. TURN credentials
  CREDS=$(curl -sk -o /dev/null -w "%{http_code}" -X POST -H "X-API-Key: ${API_KEY}" "${BASE}/v1/webrtc/turn/credentials")

  # 3. WebSocket signal (connect, send join, read response, disconnect)
  WS_OUT=$(echo '{"type":"join","data":{"roomId":"monitor-room","userId":"monitor"}}' \
    | timeout 5 websocat -k --no-close -t "wss://ns-anchat-test.orama-devnet.network/v1/webrtc/signal?token=${API_KEY}" 2>&1 \
    | head -1)

  if echo "$WS_OUT" | grep -q '"welcome"'; then
    SIGNAL="OK"
  else
    SIGNAL="FAIL"
  fi

  # Print status
  if [ "$HEALTH" = "200" ] && [ "$CREDS" = "200" ] && [ "$SIGNAL" = "OK" ]; then
    echo "$TS  health=$HEALTH  creds=$CREDS  signal=$SIGNAL  ✓"
  else
    echo "$TS  health=$HEALTH  creds=$CREDS  signal=$SIGNAL  ✗ PROBLEM"
  fi

  sleep 10
done
