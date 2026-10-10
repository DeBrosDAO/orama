# Relay rewards

> **At a glance.**
>
> - **What:** the 10 percent of emission reserved for Tor relays. Directory authorities measure relays, a reporter on each authority host submits the measurements, and `x/relay` takes the median, caps it and pays operators when the epoch settles. Nobody sends a message to be paid.
> - **Key numbers:** quorum of 3 reporters; a relay is paid only at median uptime of 0.9 or more; exit multiplier 2; caps of 100 ORAMA per relay, 200 per operator and 200 per /16 per epoch; reports are accepted for one epoch after it closes; payment splits 90/5/5.
> - **Code:** `chain/x/relay/`, `chain/reporter/`; the command is `orama-global reporter`.

## Why measurement lives off chain

The Tor network needs relays, and relays cost bandwidth. A transaction cannot measure bandwidth, so the measuring is done by the directory authorities that already vote on every relay. The chain's job is to turn several authorities' measurements into one payment without letting any one of them decide it. Four rules follow.

- **No single measurer is trusted.** A relay's pay is a median across independent reporters.
- **A relay must not pay itself.** A reporter cannot be a relay operator, and a relay's identity must be cross-signed by the key its node registered.
- **The mint is bounded by the epoch.** Only `x/emission` mints, and only up to the epoch's relay ceiling. A relay's weight is a claim; the ceiling is the budget.
- **An idle epoch costs nothing and halts nothing.** Without enough reports the epoch mints nothing, and an unfinished report is dropped when its window ends.

![Relay rewards: relay, authority votes, reporter, x/relay and the modules it pays through](../technical-reference/diagrams/ch46-overview.svg)

## Registering a relay

`MsgRegisterRelay` is signed by the node's operator. The key of a relay is its 20-byte RSA identity fingerprint. The chain checks that the node exists in `x/nodes`, that the signer operates it, and that the node's ed25519 identity signed a cross-certificate over the string `orama/relay/rsa-cross-cert/v1`, the node id and the fingerprint. That binds the Tor identity to the node and to the operator who owns it. The `/16` bucket comes from the node's literal-IP endpoints; a relay with no identified network, or an IPv6 one, lands in one shared bucket. The record keeps a snapshot of the ed25519 key and the bucket, and it does not follow later changes in `x/nodes`. Registration locks no bond. Both the fingerprint and the node may be registered once, and no message removes a relay.

## The reporter

`orama-global reporter` runs on an authority host with a funded hot key whose address is in the reporter set. At genesis the set holds the authorities' keys, and afterwards it changes only through a passed structural governance proposal. Every five minutes (`chain/reporter/runner.go:Step`) it checks whether an epoch has just ended. It remembers the start of the epoch in progress, so when the epoch counter advances by one, the span of the ended epoch is known. If the counter advanced by more than one, the missed spans are reported as lost and the reporter carries on from the present. Before it tries a report it asks the chain whether the epoch is already settled or its window is over, and if so it drops the report.

Measurements come from the archive of authority votes. The reporter requires that the votes cover at least four fifths of those the span expects, otherwise it reports nothing. For each relay it computes:

- **Weight:** the median of the authority's `Measured=` bandwidth over the votes that list the relay as running, times 1,000,000 norama. The bandwidth a relay advertises is never read, because the relay states it itself. A relay the authority did not measure weighs zero.
- **Uptime:** the share of selected votes that list the relay as running.
- **Exit flag:** set when at least half of the votes that list it carry Exit and not BadExit. Only this flag changes pay.

The reporter leaves out relays with no ed25519 id, relays not registered on chain, relays of its own operator, and relays whose id differs from the registered one, because the chain refuses a whole chunk that contains any of them. It writes the entries to `report-EPOCH.json` before sending the first chunk, so a crash or a registry change cannot make a half-sent report inconsistent.

## Taking a report

Reports travel in `MsgReportEpoch` chunks of up to 4,096 entries, at most 32 chunks, so a report holds up to 131,072 relays. The default chunk is 1,000 entries, about 150 KB. Every chunk carries the chunk count and an `inputs_root`, a SHA-256 over the canonical encoding of all entries, binding the chunks of one report to each other.

`reportEpoch` (`chain/x/relay/keeper/report.go`) accepts a chunk only when the signer is a reporter, the epoch is closed and no older than one epoch, a ceiling exists for it and it is not settled. Each entry must name a registered relay with the same ed25519 id, once per chunk, with weight in 0 to 2^62 and uptime in 0 to 1. A chunk already stored is accepted if identical and refused if different. When the last chunk arrives, the chain reassembles the report, rejects gaps and duplicate fingerprints, and recomputes the root. A mismatch deletes the partial report, so a corrected resubmission is not stuck behind a chunk that can no longer be replaced.

## Settlement

An epoch closes when the chain enters the next one. Reports for it are accepted during that next epoch, and the epoch settles in the first block of the one after. The end blocker finds the oldest epoch with stored reports and settles it, at most one epoch per block, before `x/emission` reconciles its mint.

![The scoring and capping pipeline of one epoch](../technical-reference/diagrams/ch46-settlement.svg)

`SettleEpoch` (`chain/x/relay/keeper/settle.go`) follows these steps:

1. A stored result is returned unchanged, so a repeat never mints twice.
2. Only complete reports from addresses still in the reporter set count. With fewer than the quorum of 3, the epoch stores a result with `quorum_met` false and mints nothing.
3. The first epoch to reach quorum activates rewards and mints nothing. This avoids paying an epoch that nobody could have been measured for.
4. For each relay listed by at least a quorum of reports, in fingerprint order, the chain skips it if it is unregistered or jailed, if its node has retired or is jailed, if any report carries a different ed25519 id (skipped, not an error, so a key change cannot abort the epoch), or if the median uptime is under 0.9. Otherwise it takes the median weight, caps it at 100 ORAMA, and doubles it if the median Exit bit is set.
5. Claims are scaled down pro rata to the per-operator cap, then each /16 bucket to its cap, then all claims to the epoch's relay ceiling. Remainders go to the largest fractional parts, so the sum equals the cap exactly.
6. The sum is minted once through `x/emission`, which refuses an amount over the epoch's remaining ceiling. Each operator is paid through the storage service split: 90 percent to its earnings account, 5 percent burned, 5 percent to the archive fund. Because the 5 percent share is the way relays fund archive deals, relay pay and stored history are linked.

For Exit, the median is the lower one, so a single dissenter among three does not set it.

## What the design assumes

With three reporters and a quorum of 3, one wrong reporter cannot change a payment, two can, and all three are needed for any payment at all. The reporter key is a hot key on an authority host with no other power in the module. The caps are what stop splitting one machine into many relays or many operators into one network from multiplying pay. All relays without an identified network share one /16 bucket and one cap.

Weights are bounded at 2^62 so that no sum or product overflows the 256-bit integer and halts the end blocker. The chain refuses an address that is both reporter and relay operator, but it cannot see that an authority operator also runs relays under another key. The reporter binary leaves out its own operator's relays, and the chain does not enforce it.

At 10x the first bottleneck is the end blocker. Each reporter's report is stored as one value, and settlement reads all of them in one block, groups by fingerprint and sorts. At 131,072 relays and three reporters that is about 400,000 observations in an end step with no gas meter. Payout rows are never pruned.

The most consequential limit: nothing slashes or jails a relay automatically. `Keeper.JailRelay` exists for a governance caller after bad-exit evidence, and no code path calls it.
