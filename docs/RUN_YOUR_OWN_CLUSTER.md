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
must match the machine: `orama build --arch` builds amd64 (the default) or arm64, and
nothing else.

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
add <ip>` stores the VPS login, and `--password` reads it. You need no source
checkout, Go or zig: setup installs a published release. It takes three things
you choose, none of which it fetches for you:

- `--release <version>`: the release to install, on the `stable` channel unless
  you pass `--channel`.
- `--release-repo <url>`: the https address of the release repository, which
  serves the signed metadata and the archives.
- `--release-root <root.json>`: the TUF root of the release signers you decided
  to trust. It is the one thing that has to reach you by another road than the
  repository (the project publishes its SHA-256; check the file against it).

Setup fetches the release's metadata on your machine, verifies it against that
root (every role at its threshold, a timestamp that has not expired, a snapshot
no older than the newest this machine has accepted, the channel's own keys for
its own files), downloads the archive and checks its length and hashes. Only then
does your RootWallet sign it, as the build your cluster runs, with the root
inside: your cluster trusts your wallet for what it installs, and every node
adopts the root, so it can later update itself (below). A test cluster passes
`--acme-ca letsencrypt-staging` so certificate issuance does not use the
production Let's Encrypt quota.

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
orama node setup --ip 203.0.113.10 --password --env mycluster --release 0.3.1 \
  --release-repo https://releases.example.org/tuf --release-root ./root.json \
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
orama node setup --ip 203.0.113.11 --password --env mycluster --release 0.3.1 \
  --release-repo https://releases.example.org/tuf --release-root ./root.json \
  --base-domain cluster.example.com --role nameserver --join-via root@203.0.113.10 --acme-ca letsencrypt-staging
orama node setup --ip 203.0.113.12 --password --env mycluster --release 0.3.1 \
  --release-repo https://releases.example.org/tuf --release-root ./root.json \
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
static` publishes a directory (any directory with an `index.html`; `./site` here)
as the deployment `www`.

Further nameserver detail, including installing by hand on the VPS with
`orama node install`, is [NAMESERVER_SETUP.md](NAMESERVER_SETUP.md).

## Check it

```bash
orama status --env mycluster
orama app list
```

`orama status` reads every node's health from the cluster's operator telemetry
API, so it needs the sign-in above, with the RootWallet account you ran
`orama node setup` with: that account is the cluster's operator. A node is healthy when its gateway answers
and its RQLite is Leader or Follower; all three of yours should say so.
`orama app list` shows `www`.

Every command in Install, Use it and Check it is executed, in this order, by
`make e2e-cluster` against machines the repo's owner provides
(`core/e2e/clusterguide`, see [DEV_DEPLOY.md](DEV_DEPLOY.md), "Cluster guide
e2e"). Change a command here and that test fails until its plan matches.

## Keep it updated

A cluster does not update itself until you tell it where releases are. These
are settings of the cluster, changed by an operator and written to the audit
trail:

```bash
orama cluster settings set release-repo https://releases.example.org/tuf
orama cluster settings set update-channel stable
orama cluster settings set auto-update notify
orama cluster settings show
```

With `notify`, the default, every node looks every 15 minutes, verifies what it
finds against the release root it adopted at install, and `orama monitor` shows
a newer release as information (a release that does not verify, or that a node
rolled back, as a warning). Nothing is installed. With `orama cluster settings
set auto-update auto` the nodes install it themselves, one at a time, followers
first and the leader last, only while the cluster is healthy and the hour is
inside `update-window`, and a release that fails on one node is not tried on the
others. How it decides, and what it does when an install fails, is
[DEV_DEPLOY.md](DEV_DEPLOY.md), "Auto-update".

## Building from source

A release is the way to run a cluster without a checkout. To run a build of your
own instead, build it in a checkout of this repository, which needs Go, zig and
the RootWallet agent, and pass the path to setup in place of the three release
flags (`--archive` and `--release` are alternatives):

```bash
orama build
```

`orama build` prints the archive's path. The build is reproducible, so a second
build of the same commit with `SOURCE_DATE_EPOCH` set to the commit's time gives
the same archive, byte for byte (DEV_DEPLOY.md, "Reproducible builds").

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
It seals each secret to the destination gateway's restore key for that
namespace, which is derived from the cluster's encryption root and the
namespace name, so it changes when the root is rotated. Each sealed secret
also carries its namespace and row, and the gateway checks them, so secrets
cannot be replayed into another namespace or row.

Before writing anything the gateway refuses: a wrong key, a corrupt or
truncated file, a backup of a different namespace, a gateway whose RQLite
client cannot run atomic batches, and a restore that would put the namespace
over its storage quota on this cluster (`max_storage_bytes` times the
replication factor, as `/v1/storage/pin` counts it). Then it replaces the
namespace's RQLite with the snapshot, puts back this cluster's own storage
quota (a backup never brings its quota with it), writes the secrets under its
own encryption root, checks the quota again against the restored storage
table, and pins the CIDs, eight at a time, within ten minutes. A failure after
the database was replaced says so; running the same restore again is safe.

The live keys, grants and sessions are not restored: they are in the cluster's
registry, not in the namespace's database. A key revoked since the backup stays
revoked and one minted since still works; mint the keys the new cluster needs
with `orama namespace keys create`. The backup's database still carries stale
copies of those rows, which nothing reads.

A database image (a restore's, or an `orama namespace rqlite import`'s) is
checked before RQLite loads it, because once loaded every gateway of the
namespace serves writes against it. The gateway writes it to a temporary file
and opens it read-only in SQLite. It refuses the image with 400, writing
nothing, unless it is an intact SQLite database with no trigger (tenant SQL
cannot create one), no view over a platform table, and no stored-object record
naming content that the registry records only against other namespaces (such a
record would let you read another tenant's content). Table names are matched as SQLite does, ignoring case, and an
ownership table missing its `cid` or `namespace` column is refused (the
platform's schema has had both since the first release of the table). Older backups may still
carry plaintext `api_keys` rows and stored-object records of other namespaces;
nothing reads them, so they are accepted. After the load the gateway removes
them, checks again for anything above as a backstop, and only then puts the
destination's storage quota back; both run detached from your connection, so hanging up
after the upload does not skip them. It cannot check the `size_bytes` of the
records it keeps: no gateway call reports a pinned object's size, so the storage
quota counts them as the image says. Functions, their triggers and the other
rows the namespace's gateway reads from its own database come back as the image
has them.

Each gateway runs one whole-database transfer at a time (`backup`, `restore`,
`rqlite export`, `rqlite import`) and answers 429 while one is running. A backup
and its restore request are held in memory whole (one nacl box), so they are
capped: a namespace database over 256 MiB, more than 50,000 pinned CIDs, or
more than 8 MiB of pins and secrets together is refused with 413, and so is an
`rqlite import` over 256 MiB. A request announcing more than that (a restore:
256 MiB plus its headers) is refused 413 at the cluster gateway before any of
it is sent on; one that does not announce its length is refused 413 once it has
sent more than that. (The cluster gateway's own `/v1/rqlite/import`, an
operator's replacement of the registry, is streamed with no cap and no check
beyond the SQLite header.) While it seals a backup the gateway holds about three copies of
the database.

These four routes run on the namespace's own gateway, so `orama namespace
backup`, `restore`, `restore-key` and `rqlite export` and `import` go to the
namespace host you signed in to (`ns-<name>.<domain>`), not to the
environment's gateway. With `ORAMA_TOKEN`, set `ORAMA_API_URL` to that host. A
gateway's HTTP server cuts every other request off 60 seconds after its headers
(reading) and 120 seconds after its handler starts (writing). On these four
routes, both the cluster gateway's proxy and the namespace gateway move both
deadlines to five minutes from the start of the request, and the proxy waits at
most five minutes: a transfer slower than that fails, and a database of 256 MiB
needs about 1 MiB/s.

RQLite 8 forwards `/db/backup` and `/db/load` from a follower to the leader
itself. The gateway never follows a redirect from RQLite (a redirected POST
would be re-sent without its body) and reports one as an error instead.

What it does not do: create the namespace, redeploy deployments or functions
onto the new cluster's nodes, or copy the pinned content. IPFS Cluster
accepts each pin; the content only arrives if it is still reachable on IPFS.
Backups are taken when you run the command; the cluster does not take, store
or schedule them, and holds no key that could pay for a storage deal.

### A backup in a storage deal

A backup survives its cluster only if a copy lives elsewhere. A private
storage deal on the Orama chain keeps it with providers, under your own keys.
The backup stays sealed to your X25519 key; the deal adds its own slot layer
under your `orama-storage-v1` key and repair seed (both read from `0600` files,
never from the command line).

```
orama namespace backup --key <public key hex> --deal-dir ./slots \
    --deal-nonce <32 bytes hex> --deal-replicas 3 \
    --storage-key-file ./storage.key --repair-seed-file ./repair.seed
```

This writes `slot-0` ... `slot-2`, one distinct ciphertext per replica, and
prints each slot's piece root and the `orama storage create --class private`
command that opens the deal for exactly those pieces (the same nonce and
replica count). Run that command, then `orama storage put --deal-id <id> --dir
./slots --rpc <oramad RPC>`. `--out` can be given as well, to keep the sealed
file.

On the new cluster, restore from the deal:

```
orama namespace restore --from-deal <id> --rpc <oramad RPC> \
    --storage-key-file ./storage.key --repair-seed-file ./repair.seed \
    --key-file ./backup.key --namespace myapp --dest-key <restore key>
```

The first slot a provider serves with the on-chain root is fetched and opened,
then restored as above. A wrong storage key or repair seed, a truncated slot,
or the wrong backup key stops before anything is sent to the gateway. The deal
runs for the epochs you bought; extend it with `orama storage extend`. Each
backup is a new deal, and the deal's price comes from your account. Keeping
several (daily, weekly) is your choice of how often you run the command and
which deals you extend.

`orama namespace backup-seal` and `backup-open` seal and open any file with
the same keys.
