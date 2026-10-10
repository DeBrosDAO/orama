# Embedded network registry

A build copy of the repository's `networks/` directory: each `<name>/` holds that
network's `manifest.json` and `release-root.json`. Do not edit it by hand.

`make -C core sync-networks` rewrites it from `networks/`, and
`TestEmbedded_matchesPublishedNetworks` fails when the two differ.
