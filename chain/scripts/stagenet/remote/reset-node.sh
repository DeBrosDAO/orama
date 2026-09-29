#!/usr/bin/env bash
# Runs on a stagenet node, as root (deploy.sh reset stages and runs it with sudo). Removes the
# global services and the co-located layout `orama global install --colocated` wrote, and any
# legacy direct-unit chain install, following the removal steps in docs/RUN_A_GLOBAL_NODE.md
# ("Sharing a machine with a cluster node"). Every step tolerates the thing it removes already
# being absent, so a half-finished install or a second run is fine.
#
# It touches only orama-global-* units, the orama-global directories, the ufw rules tagged
# orama-global and the two global lines of the cluster's preferences.yaml. It never stops,
# restarts or reconfigures a cluster service.
set -euo pipefail

BIN_DIR=/usr/lib/orama-global/bin
UNIT_DIR=/etc/systemd/system
PREFS=/opt/orama/.orama/preferences.yaml
NETNS=orama-global
HOST_IFACE=ogl-host
UFW_TAG=orama-global
MAX_UFW_RULES=64

log() { printf '  %s\n' "$*"; }

# Stop in the order the CLI knows (chain last), when it is installed.
if [ -x "$BIN_DIR/orama" ] && compgen -G "$UNIT_DIR/orama-global-*.service" >/dev/null; then
	if ! "$BIN_DIR/orama" global stop; then
		log "orama global stop reported an error; the units are disabled below anyway"
	fi
fi

# Every orama-global unit: timers first (they trigger the GC oneshot), the namespace unit last
# (the other units BindsTo it, and its stop takes the namespace, veth and rulesets down).
shopt -s nullglob
units=()
for f in "$UNIT_DIR"/orama-global-*.timer "$UNIT_DIR"/orama-global-*.service; do
	name=$(basename "$f")
	[ "$name" = orama-global-netns.service ] || units+=("$name")
done
[ -e "$UNIT_DIR/orama-global-netns.service" ] && units+=(orama-global-netns.service)
for u in "${units[@]}"; do
	log "disable $u"
	systemctl disable --now "$u" >/dev/null 2>&1 || true
done
rm -f "$UNIT_DIR"/orama-global-*.service "$UNIT_DIR"/orama-global-*.timer
systemctl daemon-reload
systemctl reset-failed 'orama-global-*' >/dev/null 2>&1 || true

# What the netns unit's stop should already have removed.
ip netns delete "$NETNS" >/dev/null 2>&1 || true
ip link delete "$HOST_IFACE" >/dev/null 2>&1 || true
nft delete table ip orama_global >/dev/null 2>&1 || true

# ufw rules tagged orama-global (input and route rules), highest number first is not needed: the
# first match is re-read after each delete.
if command -v ufw >/dev/null 2>&1; then
	for _ in $(seq "$MAX_UFW_RULES"); do
		num=$({ ufw status numbered | grep -E "# $UFW_TAG\$" || true; } | head -n1 | sed -n 's/^\[ *\([0-9][0-9]*\)\].*/\1/p')
		[ -n "$num" ] || break
		ufw --force delete "$num" >/dev/null
	done
fi

# The cluster's role goes back to what it was: no role line (cluster) and no namespace line.
if [ -f "$PREFS" ] && grep -Eq '^(role: both|global_netns:)' "$PREFS"; then
	log "restore the cluster role in $PREFS"
	sed -i -e '/^role: both$/d' -e '/^global_netns:/d' "$PREFS"
fi

rm -rf /etc/orama-global /etc/sysctl.d/60-orama-global-netns.conf
rm -rf /var/lib/orama-global /usr/lib/orama-global
rm -rf /root/orama-global-release /usr/local/lib/orama-stagenet /run/orama-stagenet /run/orama-stagenet-fwd
log "global install removed"
