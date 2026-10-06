# Ubuntu fleet disk encryption (design only)

**Status: design. Not implemented on the Ubuntu fleet.** OramaOS has a
separate LUKS path under `os/agent`; that code is not a template to copy
here and is not production-ready (boot-order bugs, genesis unlock on
`:9998` with no credential, `isEnrolled()` looking at the mountpoint
before unlock). This page is the feat-272 write-up.

## Threat

A provider or NSA **disk snapshot** of one Ubuntu node is a total steal
today: `cluster-secret`, WireGuard private key, RQLite sqlite+raft,
IPFS repo, Caddy TLS keys, HMAC / TURN / function-secret keys. Network
wrap (RQLite `-auth`, bind off `0.0.0.0`, encrypt-before-Add, Caddy
TLS) does not move those files off the disk.

## Recommendation

**LUKS2 on a dedicated data volume, mounted at `/opt/orama/.orama`.
Key never stored on the VPS. Unlock from the operator laptop via
RootWallet over public SSH after boot.**

Reject for Ubuntu:

- Full-disk LUKS — breaks SSH and cloud-init on Hetzner/Vultr/OVH images.
- fscrypt — worse operational story, same “must unlock after boot” cost.
- age-encrypted files — does not cover RQLite/IPFS trees without a FUSE layer.
- Copying OramaOS Shamir-across-peers — Ubuntu has SSH; Shamir exists
  because OramaOS does not. The agent implementation is unsafe to reuse.

If the node already has a second volume, LUKS that volume. If it is a
single disk, add a partition. A full-disk snapshot of a LUKS **partition
without the key** is the actual win. A loop file on the same disk still
helps against naive `tar` of `.orama`, not against `dd` of the whole disk.

## Volume contents

**Inside LUKS (`/opt/orama/.orama`):**

- `secrets/` (entire tree)
- `data/` (index + namespace RQLite, IPFS repo and cluster, vault shares,
  `identity.key`, JWT keys)
- `configs/`, `logs/`, `tls-cache/`, `backups/`
- Bind-mount **`/var/lib/caddy`** into the volume (LE private keys live
  there today, outside `.orama`)

**Outside (boot + SSH):**

- Ubuntu rootfs, `/etc/ssh`, `authorized_keys`
- `/opt/orama/bin`, systemd units, `/etc/caddy/Caddyfile` (no secrets)

**WireGuard, first cut:** keep `/etc/wireguard/wg0.conf` **outside** so
the mesh is diagnosable before unlock. A snapshot still leaks the WG
private key (mesh membership, not cluster secret). Documented hole.

## Unlock UX

1. Node reboots. SSH on the public IP is up. `orama-node` stays down
   until `/opt/orama/.orama` is mounted (`RequiresMountsFor=`).
2. Operator laptop: RootWallet unlocked (same as `orama node setup`).
3. `orama node unlock` (Ubuntu path, distinct from OramaOS genesis HTTP)
   fetches the per-node volume key from RootWallet, SSHes with the
   existing vault key, `cryptsetup luksOpen` on stdin, mounts, starts
   the stack.
4. Reboot is a deliberate operator action. Do not auto-unlock from a
   key file on the VPS.

Do not send the key to an unauthenticated `:9998`. That is the OramaOS
genesis hole.

RootWallet **yes**: it is already the operator secret store and the SSH
key store. Store **per-node** volume keys so retiring a machine does not
rotate every volume.

## What this does not solve

- Live RAM / hypervisor snapshot while the mapper is open
- `tar` of the mounted tree as root
- Stolen operator laptop with RootWallet
- SSH on Ubuntu after unlock
- Existing nodes stay plaintext until migrated
- RQLite Raft log history of deleted rows

LUKS is a stolen-disk / offline-snapshot control, not a running-node
confidentiality control.

## Follow-up (not this change)

Implement ticket: Ubuntu data-volume LUKS, RootWallet-held key, SSH
unlock, Caddy bind-mount, systemd `RequiresMountsFor`, migration
playbook under Raft quorum (never lock a majority of voters at once),
tests that refuse to start services if the mount is missing so secrets
are never recreated on the unencrypted mountpoint.

## Chicken-and-egg

Ubuntu operator channel is public SSH, so the mesh does **not** have to
come up before unlock. That is why WireGuard can stay outside for the
first cut and why OramaOS Shamir is the wrong model here.
