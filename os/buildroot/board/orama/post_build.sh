#!/bin/bash
# OramaOS post-build script.
# Runs after Buildroot builds the rootfs but before image creation.
# $TARGET_DIR is the rootfs directory.
set -euo pipefail

TARGET_DIR="$1"

echo "=== OramaOS post_build.sh ==="

# --- Remove all shell access ---
# Operators must not have interactive access to OramaOS nodes.
# Busybox is kept for mount/umount/etc that systemd needs,
# but all shell entry points are removed.
rm -f "$TARGET_DIR/bin/bash"
rm -f "$TARGET_DIR/bin/ash"
rm -f "$TARGET_DIR/usr/bin/ssh"
rm -f "$TARGET_DIR/usr/sbin/sshd"

# Replace /bin/sh with /bin/false — any attempt to spawn a shell fails
ln -sf /bin/false "$TARGET_DIR/bin/sh"

# Remove getty / login (no console login)
rm -f "$TARGET_DIR/sbin/getty"
rm -f "$TARGET_DIR/bin/login"
rm -f "$TARGET_DIR/usr/bin/login"

# Disable all TTY gettys
rm -f "$TARGET_DIR/etc/systemd/system/getty.target.wants/"*
rm -f "$TARGET_DIR/etc/systemd/system/multi-user.target.wants/getty@"*

# --- Create service users ---
# Each service runs under a dedicated uid/gid (defined in sandbox.go).
for uid_name in "1001:rqlite" "1002:olric" "1003:ipfs" "1004:ipfscluster" "1005:gateway" "1006:coredns"; do
    uid="${uid_name%%:*}"
    name="${uid_name##*:}"
    echo "${name}:x:${uid}:${uid}:${name} service:/nonexistent:/bin/false" >> "$TARGET_DIR/etc/passwd"
    echo "${name}:x:${uid}:" >> "$TARGET_DIR/etc/group"
done

# --- Create required directories ---
mkdir -p "$TARGET_DIR/opt/orama/bin"
mkdir -p "$TARGET_DIR/opt/orama/.orama/configs"
mkdir -p "$TARGET_DIR/opt/orama/.orama/data"
mkdir -p "$TARGET_DIR/opt/orama/.orama/logs"
mkdir -p "$TARGET_DIR/etc/orama"
mkdir -p "$TARGET_DIR/etc/wireguard"
mkdir -p "$TARGET_DIR/boot/loader/entries"

# --- Copy pre-built binaries ---
# These are placed here by the outer build script (scripts/build.sh).
BINS_DIR="${BINARIES_DIR:-$TARGET_DIR/../images}"
if [ -d "$BINS_DIR/orama-bins" ]; then
    cp "$BINS_DIR/orama-bins/orama-agent" "$TARGET_DIR/usr/bin/orama-agent"
    chmod 755 "$TARGET_DIR/usr/bin/orama-agent"

    # Service binaries go to /opt/orama/bin/ or /usr/local/bin/
    for bin in rqlited olric-server ipfs ipfs-cluster-service coredns gateway; do
        if [ -f "$BINS_DIR/orama-bins/$bin" ]; then
            cp "$BINS_DIR/orama-bins/$bin" "$TARGET_DIR/usr/local/bin/$bin"
            chmod 755 "$TARGET_DIR/usr/local/bin/$bin"
        fi
    done
fi

# --- Write version file ---
if [ -n "${ORAMA_VERSION:-}" ]; then
    echo "$ORAMA_VERSION" > "$TARGET_DIR/etc/orama-version"
fi

# --- systemd-boot loader config ---
cat > "$TARGET_DIR/boot/loader/loader.conf" <<'LOADER'
default orama-*
timeout 0
console-mode max
LOADER

echo "=== OramaOS post_build.sh complete ==="
