# Run your own cluster

A private Orama cluster is three or more Linux machines that you install with
`orama node setup`, plus a domain whose nameserver records you can publish.
The installer refuses a machine that fails the checks in
`core/pkg/install/checks.go`. This page is that sequence. It does not create
servers: `orama sandbox create` does, on Hetzner, and
[SANDBOX.md](SANDBOX.md) is that command.

## What you need

**Machines.** Three VPS, so the index RQLite group has three voters. Each one
must be one of debian 12, 13; ubuntu 22.04, 24.04, 26.04, with at least 2 CPU
cores (`runtime.NumCPU`), 2GB of RAM and 10GB free disk. Those floors are
`MinCPUCores`, `MinRAMBytes` and `MinFreeDiskBytes`. The RAM and disk checks
count 1024³ bytes and the error text calls that GB. The archive you install
must match the machine: amd64, arm64 or arm.

**A domain.** Genesis records the environment gateway as `https://<base-domain>`.
That name has to reach the cluster, which means NS records and glue for its
nameservers. For a name you registered (`example.com`), the glue is stored at
the registrar. For a name under a zone you already host (`stagenet.example.com`),
the glue is an ordinary A record in the parent zone. `--cloudflare-token-file`
writes that parent zone when it is hosted at Cloudflare, and it refuses a
registry TLD (a parent with no dot, such as `com`). The record format is
[NAMESERVER_SETUP.md](NAMESERVER_SETUP.md).

**On the machine you type from.** The RootWallet desktop app, open and
unlocked. `orama node setup` and `orama auth login` ask its agent for a key
and a signature. There is no SSH key on disk and no password flag: `rw vault
add <ip>` stores the VPS login, and `--password` reads it. Build the archive
you will install with `orama build` in a checkout of this repo; setup takes
that path as `--archive`. A test cluster passes `--acme-ca letsencrypt-staging`
so certificate issuance does not use the production Let's Encrypt quota.

**Ports the installer opens.** On every node: SSH `22/tcp`, WireGuard
`51820/udp`, `80/tcp` and `443/tcp`. On a nameserver it also opens `53/tcp` and
`53/udp`. A firewall in front of the VPS has to allow the same ports; ufw on
the machine cannot open it.

## Install

Store each VPS login, then install the genesis nameserver. `--genesis` creates
the cluster and records the environment. It does not make that environment the
active one.

```bash
rw vault add 203.0.113.10
orama build
orama node setup --ip 203.0.113.10 --password --env mycluster --archive /tmp/orama-linux-amd64.tar.gz \
  --base-domain cluster.example.com --role nameserver --genesis --acme-ca letsencrypt-staging
```

Publish the NS and glue records before joining anyone else. The invite a
joiner uses pins the genesis node's certificate, and that certificate is not
issued until the delegation resolves.

```bash
orama node dns delegation --env mycluster
orama node dns delegation --env mycluster --cloudflare-token-file /path/to/token
```

The command without the token prints the records. The command with the token
writes them when the parent zone is at Cloudflare, then checks DNS. Wait until
`dig NS cluster.example.com @8.8.8.8` lists the nameservers the command
printed and the genesis certificate has been issued.

Join the other two machines through the genesis node. `--join-via` mints the
invite over SSH, so the cluster name does not have to resolve on your machine
yet.

```bash
orama node setup --ip 203.0.113.11 --password --env mycluster --archive /tmp/orama-linux-amd64.tar.gz \
  --base-domain cluster.example.com --role nameserver --join-via root@203.0.113.10 --acme-ca letsencrypt-staging
orama node setup --ip 203.0.113.12 --password --env mycluster --archive /tmp/orama-linux-amd64.tar.gz \
  --base-domain cluster.example.com --role nameserver --join-via root@203.0.113.10 --acme-ca letsencrypt-staging
orama node dns delegation --env mycluster
```

Run the delegation command again after the new nameservers exist and update
the parent zone to match. A nameserver slot stays with its machine until
`orama node remove`.

## Use it

```bash
orama env use mycluster
orama auth login
orama namespace create myapp
orama auth login --namespace myapp
orama deploy static ./site --name www
```

`orama auth login` signs in to the active environment's gateway. Creating a
namespace does not sign you into it; the second login does. `orama deploy
static` publishes the directory as the deployment `www`.

Further nameserver detail, including installing by hand on the VPS with
`orama node install`, is [NAMESERVER_SETUP.md](NAMESERVER_SETUP.md).

## A sealed backup

A namespace backup is sealed to an X25519 keypair you keep. The cluster is
only ever given the public key, so it can write backups it cannot read.

```bash
# On the cluster the namespace lives on, signed in as its owner:
orama namespace backup --key <public key hex> --out myapp.orbk
```

The gateway puts three things in the file: the namespace's RQLite snapshot
(the same `/db/backup` that `orama namespace rqlite export` downloads), every
CID the namespace holds pinned (stored objects and each deployment's content
and build), and its stored secrets (function secrets, push tokens and
credentials, TURN secret, deployment environment). The secrets are decrypted
by the source cluster, because they are encrypted under its encryption root
and no other cluster can read them. If the source cluster dies before a backup
was taken, its secrets are gone.

To restore onto another cluster, create the namespace there, sign in to it as
its owner, and run:

```bash
orama namespace restore-key            # the destination's restore public key
orama namespace restore --in myapp.orbk --key-file ./backup.key \
  --namespace myapp --dest-key <restore public key hex>
```

`restore` opens the backup on your machine; the private key never leaves it.
It seals each secret to the destination gateway's restore key, which is derived
from that cluster's encryption root and changes when the root is rotated. The
gateway replaces the namespace's RQLite with the snapshot, writes the secrets
under its own encryption root, and pins the CIDs. A wrong key, a corrupt or
truncated file, or a backup of a different namespace is refused before
anything is written. A failure after the database was replaced says so;
running the same restore again is safe.

What it does not do: create the namespace, redeploy deployments or functions
onto the new cluster's nodes, or copy the pinned content. IPFS Cluster
accepts each pin; the content only arrives if it is still reachable on IPFS. The backup and the
restore request are held in memory whole (one nacl box; the restore request
is capped at 1 GiB). Backups are taken when you run the command; there is no
schedule and no storage deal.

`orama namespace backup-seal` and `backup-open` seal and open any file with
the same keys.
