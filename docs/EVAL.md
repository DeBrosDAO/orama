# One-VPS eval

A single VPS can run Orama end to end: the index plane on that machine, then a
tenant namespace on the same machine, then an app with a database. That path is
**eval**, not production HA.

## What the code does

Index membership is every node (`MembersAll`). One machine *is* the whole
index. Tenant provision looks at how many nodes have a free namespace slot:

| Eligible nodes | Tenant size | Notes |
|---|---|---|
| 1 | `BlueprintTenantN(1)` | Same units (`orama-namespace-{rqlite,olric,gateway}@<name>`), replica count 1. RQLite is leader, no `-join`. |
| 3 or more | `BlueprintTenant()` — N=3 | Production default. A 10-node fleet still provisions tenants at 3, not 10. |
| 0 | refused | Nobody to run on. |
| 2 | refused | Neither eval nor HA. Even-sized Raft is a split-brain. Add a third node. |

There is no second “dev mode” that skips systemd or blueprints, and no
`--replicas` flag. The create API is still `POST /v1/namespaces` with `{name}`.

Vault is an index service (one guardian per node). On one VPS there is one
guardian. Shamir cannot split (`K` is floored at 2). The gateway stores the
envelope as a **local key** on that disk (`K=1`, `W=1`) and logs it. That is
not information-theoretic secret sharing: lose the disk, lose the secret.
Production (`N≥3`) is unchanged: `K = max(2, floor(N/3))`.

WebRTC stays unavailable (`need at least 3` for SFU/TURN). Do not enable it on
eval.

Bounce of the only node is local restore. There is no spare machine to fail
over onto. Disk loss is namespace loss.

## Operator path

Genesis on the VPS (nameserver so the node has DNS):

```bash
sudo orama node install --vps-ip <ip> --domain <domain> --base-domain <domain> --nameserver
```

Or from your machine, with RootWallet unlocked:

```bash
orama build   # prints the archive path
orama node setup --ip <ip> --password --env <env> --archive <archive path> \
  --base-domain <domain> --role nameserver --genesis
# --password reads the VPS login from your RootWallet vault (rw vault add <ip>)
```

Then:

```bash
orama auth login
orama namespace create myapp
orama auth login --namespace myapp
```

`namespace create` on a one-node fleet no longer fails with
`insufficient nodes available for cluster`. The cluster row stores replica
counts 1/1/1 so repair does not try to grow it to 3.

This is not a substitute for [DEVNET_INSTALL.md](DEVNET_INSTALL.md) (three
nameservers) or for rolling-upgrade rules. Those remain the cluster path.
