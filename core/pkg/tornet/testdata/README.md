# Synthetic Tor documents

These files are written from tor's dir-spec (the network-status consensus, vote and bandwidth
file formats). They are NOT captured from a running network: the E0 spike could not run tor
(plans/open-network/decisions/E0.md). Digests, ed25519 ids and signatures are placeholders.

Replace them with documents captured from the stagenet authorities (the vote archive that
`orama maint global tor archive` writes) once the first consensus exists. The parser tests must
keep passing on the real ones.

- consensus-microdesc.txt: a 3-relay microdesc consensus (r lines without a descriptor digest), valid-after 2026-10-08 12:00:00
- consensus-ns.txt: the same relays in the full consensus (r lines with the digest)
- votes.txt: two authorities' votes for that same period, concatenated as v3-status-votes holds them
- bandwidth-file.txt: an sbws v1.4 bandwidth file
