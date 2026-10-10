# Published networks

One directory per network, written when a chain is created: `orama setup
--create-network` writes it to its `--publish-dir`, and `orama maint network publish`
writes it from a genesis that already exists. Copy what setup wrote here, run
`make -C core sync-networks`, and commit both. Do not edit it by hand.

```
networks/<name>/
  manifest.json       chain id, genesis digest, seeds, channel, minimum version,
                      release repository and the digest of release-root.json
  genesis.json        the genesis the manifest's digest pins
  release-root.json   the TUF root this network's releases are verified against
```

The same files are served at `https://orama.network/networks/<name>/`: the website
build copies this directory into the site and fails if a copy differs. The `orama`
binary embeds each network's `manifest.json` and `release-root.json` from
`core/pkg/netregistry/embedded/`, a build copy that `make -C core sync-networks`
rewrites from here and that a test checks.

A network can be *announced* before the chain exists: `orama maint network announce` writes
`manifest.json` (with an empty `genesis_sha256`) and `release-root.json`, and no
`genesis.json`. The CLI lists such a network, refuses to join it ("has not been created yet"),
and `orama setup --create-network <name>` takes the chain id, release repository, channel,
seeds, faucet and release root from it; when the chain exists the created network's files
replace the announcement.

Every reset of a network gets a new chain id (`orama-stagenet-5` becomes
`orama-stagenet-6`); `publish` refuses to give an already published chain id a
different genesis.
