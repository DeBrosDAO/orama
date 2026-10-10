# C0 spike results

**Date:** 2026-09-28. **Branch:** `0.300.0`. **No production code.** `chain/app/app.go` and `core/` were not changed.

The C0 section of `plans/open-network/track-c-chain.md` asks for eight records, `C0-1.md` through `C0-8.md`. This file is all eight. The owner has not signed off. Do not mark C0 done from this file alone.

Pinned modules, from `chain/go.mod`: Cosmos SDK **v0.54.4**, CometBFT **v0.39.4**, `github.com/cosmos/cosmos-sdk/store/v2` **v2.0.0**, `github.com/cosmos/iavl` **v1.2.8** (indirect, required by store v2). Tests ran on an Apple M3 (`darwin/arm64`) with `go1.25.4` (`GOTOOLCHAIN=auto` did not switch toolchains). `cd chain && go test ./spikes/...` passes. Nothing under `chain/spikes/` is imported by `chain/app`.

Numbers below come from that test run or from reading those module sources. Timings that were not run are listed at the end and are not guessed.

---

## C0-1 — Sign mode and accounts

**Unblocks:** C1 ante (which modes the chain accepts) and F2 (RootWallet path and address format).

### What was read and measured

SDK v0.54.4 `types/tx/signing/signing.pb.go` registers `SIGN_MODE_DIRECT` (1), `SIGN_MODE_TEXTUAL` (2), `SIGN_MODE_DIRECT_AUX` (3), `SIGN_MODE_LEGACY_AMINO_JSON` (127) and `SIGN_MODE_EIP_191` (191). There is no `SIGN_MODE_EIP712` enum value. `x/auth/tx/config.go` says EIP-191 has no default handler; a chain has to pass a custom `TxConfig` to enable it. `DefaultSignModes` is DIRECT, DIRECT_AUX, LEGACY_AMINO_JSON. TEXTUAL is off unless the app adds it.

`x/auth/tx/direct.go` `DirectSignBytes` is the protobuf `cosmos.tx.v1beta1.SignDoc`: body bytes, auth-info bytes, chain id, account number. It does not contain the sequence; the sequence is inside the auth info. `crypto/keys/secp256k1` `Sign` / `VerifySignature` hash that blob with SHA-256 once, then ECDSA, 64-byte R||S, low-S. Address bytes are RIPEMD160(SHA256(compressed 33-byte pubkey)), 20 bytes (`secp256k1.go` `Address`).

This repo already has the prefix and the coin type in `chain/app/params/params.go`: bech32 `orama`, base denom `norama`, 9 decimals, BIP-44 coin type **118**. `chain/app/app.go` currently enables `DefaultSignModes` plus TEXTUAL. `core/pkg/rwagent/client.go` `SignOramaTx` posts the raw SignDoc to `POST /v1/orama/tx/sign` and checks the signature with go-ethereum `VerifySignature` over SHA-256(SignDoc), which does not hash again. That matches the SDK's single hash. The generic `POST /v1/wallet/sign` is a different call.

`chain/spikes/signmode_test.go` derived the BIP-39 test mnemonic (`abandon` × 11, `about`) and signed a SignDoc. Output:

```
path=m/44'/118'/1'/0/0 address=orama1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2
pubkey=02b74657437c7b173c2f4e442f1c863b24857fa97283385c5ec172ff63ff18b7be sig_ok=true eip712_enum=absent
coin60_pubkey=0237b0bb7a8288d38ed49a524b5dc98cff3eb5ca824c9f9dc0dfdb3d9cd600f299
coin118_account0_pubkey=024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62
```

The test asserts coin-type 60 account 0, coin-type 118 account 0, and coin-type 118 account 1 are three different compressed pubkeys. It also asserts a signature over SHA-256(SignDoc) made by the SDK signer (a second hash) does **not** verify against the SignDoc.

### Decision

- Account key: compressed secp256k1. Address: bech32 human prefix `orama` over the 20-byte SDK address. Validator operator prefix `oramavaloper`, as `params.go` already defines. Not an Ethereum address and not `ethsecp256k1`.
- Wallet derivation for F2: `m/44'/118'/1'/0/0`. Coin type 118 already differs from Ethereum's 60 (measured). Account index **1**, not 0, is the separate Orama account, so a default Cosmos account (`m/44'/118'/0'/0/0`) on the same seed is also a different key. The chain does not enforce the path. The wallet must.
- The only sign mode the genesis binary should accept is `SIGN_MODE_DIRECT`. C1 narrows `enabledSignModes` to that one value. Leave amino, direct-aux, textual and EIP-191 off. Amino sign bytes are a different digest, and there is no legacy traffic to keep.
- EIP-712 is refused by not existing. Do not add an Ethermint ante or a custom sign mode. RootWallet's generic `/v1/wallet/sign` must not be the Orama tx path. `SignOramaTx` already is not that path. F2, in the RootWallet agent (not this repo), refuses `chain == "orama"` and any EIP-712 domain that names Orama on the generic route, and signs only the protobuf SignDoc. A signer that hashes the SignDoc again will fail the chain's `VerifySignature`.

---

## C0-2 — Performance, capped power, p2p

**Unblocks:** P7 and C4's active-set start, once a real cluster is run. It does **not** unblock a number today. C4's code can keep the 5%/3% cap step at 60 validators (`chain/x/power/types/params.go` `DefaultCapStepDownValidatorCount`); that step is not the active-set size.

### What was read

No 30/60/100/150 validator run was done. This machine is one laptop. `chain/scripts/localnet/localnet.sh` only has ports for **10** localhost processes (`31000`–`31099`) and is not a latency test. `x/power/types/params.go` `ProductionMinCommitteeSize` is 30 on a production chain-id, so a 30+ run cannot use the localnet chain-id escape.

CometBFT v0.39.4 defaults (`config/config.go` `DefaultConsensusConfig`): `timeout_propose` 3s, `timeout_prevote` 1s, `timeout_precommit` 1s, `timeout_commit` 1s, `create_empty_blocks` true, `skip_timeout_commit` false. Those are the block-time knobs. They were not changed and not timed.

P2P (`config/config.go` `DefaultP2PConfig`): listen `tcp://0.0.0.0:26656`, `p2p.libp2p.enabled` **false**. The stock reactor dials TCP (`p2p/transport.go` `net.Dial("tcp", ...)`). The experimental libp2p host (`lp2p/host.go`, `lp2p/address.go`) supports **only QUIC**, which is UDP (`tcp://1.1.1.1:5678` becomes `/ip4/1.1.1.1/udp/5678/quic-v1`). UDP is not required while libp2p stays off.

Vote-extension byte budgets at the C13 32KiB cap are in C0-3. They fit the default 21MiB block even at 150 validators if the lists are disjoint and not duplicated. That is a size check, not a latency check. Shielded verify and `x/storage` settlement were not loaded; neither exists in this binary.

### Decision

- **P7 is not chosen.** Do not write 60, 100 or 150 into genesis from this spike. The plan's "60–100 from the C0 benchmark" is still waiting on the benchmark.
- Keep stock CometBFT timeouts and the default 21,020,096-byte block until that run. Do not tune them from a laptop.
- **Do not enable libp2p.** Genesis p2p is the default TCP reactor. Firewalls open TCP 26656, not UDP. A later benchmark can reverse this. It has not been run.
- C4's capped-power function is unchanged by this spike.

### Not run (parent command)

Do not point this at devnet or testnet. Use new machines in three or more regions.

```
# Not run. chain/scripts/localnet/localnet.sh cannot do this:
# it only has ports for 10 local processes.
#
# On the throwaway machines, build oramad from this commit:
#   cd chain && CGO_ENABLED=0 go build -o oramad ./cmd/oramad
# Genesis: chain-id WITHOUT -localnet-/-devnet-/-stagenet- if N >= 30,
# so x/power's ProductionMinCommitteeSize (30) applies.
# N in {30, 60, 100, 150}. Same binary, p2p.libp2p.enabled = false.
# Repeat one N with p2p.libp2p.enabled = true only if the TCP run misses
# the block-time target; that mode needs UDP/QUIC, not TCP, on 26656.
# Leave vote_extensions_enable_height at 0 for the first pass.
# Second pass, only on a genesis that already contains the C13 handlers:
#   consensus.params.abci.vote_extensions_enable_height = 1
#   and 32KiB extensions (see C0-3). There is no such handler in this binary.
# Record, from Prometheus or the consensus WAL, per N:
#   block interval p50/p99, rounds per height, peers, mempool size.
# Do not treat a single-region localhost run as this benchmark.
```

---

## C0-3 — Inclusion lists

**Unblocks:** C13, as feasible on CometBFT v0.39.4, with the limits below. Not a license to start C13 before C1.

### What was read and measured

`types/vote.go`: `MaxVoteExtensionSize = 1024 * 1024` (1MiB). It is a constant, not a consensus parameter. `types/params.go`: `VoteExtensionsEnableHeight == 0` means extensions are **off**. Default block `MaxBytes` is 22,020,096. Absolute cap `MaxBlockSizeBytes` is 100MiB.

`consensus/state.go` `defaultDoPrevote`: `ProcessProposal` reject makes that validator prevote nil. The comment in that function says misuse of rejection can compromise liveness. A rejected proposal does not halt the process by itself. It burns the round. The next proposer can propose a different block. Liveness holds only when every honest validator rejects the same blocks and some later proposal is one they all accept.

`state/execution.go` passes the previous height's extensions into `PrepareProposal` as `LocalLastCommit` (`buildExtendedCommitInfoFromStore`). Extensions are available to the app at H+1. They are not in the block unless the app puts them there. C13's "embed the extended commit" is app work.

`chain/app/app.go` sets every consensus-param authority to `UnreachableAuthority`. A later `MsgUpdateParams` cannot turn extensions on. The height is genesis or a hard fork.

`chain/spikes/inclusion_test.go` (32KiB = 32768):

```
validators=30  disjoint_extension_bytes=983040   share_of_default_block_max=0.0446  comet_1MiB_cap_bytes=31457280  exceeds_absolute_block_max=false
validators=60  disjoint_extension_bytes=1966080  share_of_default_block_max=0.0893  comet_1MiB_cap_bytes=62914560  exceeds_absolute_block_max=false
validators=100 disjoint_extension_bytes=3276800  share_of_default_block_max=0.1488  comet_1MiB_cap_bytes=104857600 exceeds_absolute_block_max=false
validators=150 disjoint_extension_bytes=4915200  share_of_default_block_max=0.2232  comet_1MiB_cap_bytes=157286400 exceeds_absolute_block_max=true
```

So a 32KiB app cap fits in the default block at 150 validators even when every list is disjoint (4,915,200 bytes, 22.3% of 21MiB, before signatures and the non-listed txs). The CometBFT 1MiB cap does not: 150 × 1MiB is 150MiB, past the 100MiB absolute block max. 100 × 1MiB equals that absolute max and still does not fit the default 21MiB block.

### Decision

C13 is feasible. Build it as specified, with these rules fixed before any genesis that turns extensions on:

1. **App cap, not the Comet cap.** `VerifyVoteExtension` rejects an extension larger than `list_max_bytes` (32KiB until a real shielded bundle is measured). Comet will otherwise accept 1MiB.
2. **Genesis height.** `vote_extensions_enable_height` stays 0 in every binary that does not implement `ExtendVote` / `VerifyVoteExtension`. The genesis that contains C13 sets it. It cannot be switched on later through `x/consensus` while authority is `UnreachableAuthority`.
3. **One copy of the tx bytes.** Signature verification needs each validator's exact extension bytes. Dedup stores each tx once, rebuilds an extension to check the signature, and uses those same bytes as the block txs. Storing the commit raw and the txs again doubles the 4.9MiB.
4. **ProcessProposal may reject** a block that drops a listed tx which is still valid and fits. Rejection must be deterministic across honest nodes. A tx that cannot fit, or that fails against state already updated by an earlier listed tx (same nullifier or sequence), is skipped, not a reason to reject the block forever. A listed tx bigger than the block is rejected in `VerifyVoteExtension`, not left for `ProcessProposal` to reject on every round.
5. **The flat transfer floor does not price 32KiB.** P2 is about $0.001 per transfer, and SDK gas is whatever the tx declares. `VerifyVoteExtension` must require a burned fee that scales with listed tx bytes (or gas that the ante actually charges from size) and must reject the vote otherwise. A validator who fails that check loses the vote. That is not a halt unless at least one third of power does it every round. That case is a validator-set attack, which slashing already covers only for double-signs, not for this. Document it as a liveness assumption: fewer than one third of power publish extensions the app rejects.

No CometBFT e2e crash run was done. C13's own test list still has to run that, on a localnet, after the handlers exist.

---

## C0-4 — Ironwood / Orchard FFI

**Unblocks:** C12a can pin the crate. It does **not** unblock a gas number. Verify cost was not measured.

### What was read

No `orchard` crate is in this repo. `rustc` on this machine is `1.96.0-nightly (80282b130 2026-03-06)`. Nothing was compiled. There is no proof vector here, so a build would not produce a verify time.

From upstream, not from a local run:

- `orchard` **0.15.5** (2026-08-02) is the crates.io release that contains Ironwood. 0.15.0 (2026-07-09) added the Ironwood pool. MSRV 1.88. License Apache-2.0 / MIT.
- The verify entry point, behind feature `circuit`, is `Bundle::verify_proof(&self, vk: &VerifyingKey)`. The key has to be the post-NU6.3 key: `VerifyingKey::build` on `OrchardCircuitVersion::PostNu6_3`. `BundleVersion::ironwood_v3()` is the Ironwood pool. It shares that circuit with Orchard V3 and uses V3 note plaintexts.
- Canonical proof length, from the orchard 0.14 changelog (`Proof::expected_proof_size`): **2720 + 2272 × n_actions** bytes. A 2-action proof is 7264 bytes. The plan's "~9KB for a 2-action bundle" is that plus actions and the binding signature. Not re-measured here.
- `ValuePool` in 0.15 is `Orchard` or `Ironwood`. There is no asset id. ZSA is not in this crate.
- ZcashFoundation/zebra PR 10628 (ZSA, still a draft in the maintainer discussion) depends on QEDIT forks of `orchard` and `librustzcash`. The maintainer note says published `orchard` exposes no ZSA types, and the upstream ZSA port (`zcash/orchard` PR 471) was not what 0.15 shipped. Ironwood took transaction v6 (ZIP 229). A multi-asset rebase onto Ironwood is not in the crate this spike can pin.

`InsecurePreNu6_2` is the circuit with the 2026 soundness bug. Nodes must not verify with it.

### Decision

- C12a pins `orchard` **0.15.5** (or a later 0.15 patch if one exists when C12a is written), feature `circuit`, `BundleVersion::ironwood_v3`, verifying key `PostNu6_3` only. Verify-only in the node. Proving stays in the wallet (F7).
- cgo calls `verify_proof` on bytes the ante has already checked for size (`expected_proof_size` for the action count). Reject any other length before the verify call.
- **Multi-asset stays off.** Do not import a QEDIT fork. D19's one-way vote waits on an audited circuit that does not exist in orchard 0.15.5. Single-asset ORAMA shielding is not blocked on that rebase.
- No gas schedule until the command below has been run on linux/amd64 and linux/arm64 with the same proof bytes. Do not copy a millisecond from a blog into C12.

### Not run (parent command)

```
# Not run. No proof vector in this repo. rustc is present; the crate was not built.
cargo new --lib /tmp/orama-ironwood-verify
# Cargo.toml:
#   [dependencies]
#   orchard = { version = "0.15.5", features = ["circuit"] }
# Build a VerifyingKey for OrchardCircuitVersion::PostNu6_3 once.
# Time Bundle::verify_proof on a 1-action and a 2-action Ironwood bundle
# (BundleVersion::ironwood_v3), repeated, on linux/amd64 and linux/arm64.
# Confirm both architectures accept the same bytes and reject one flipped byte.
# Ask QEDIT, in writing, whether OrchardZSA will be rebased onto orchard 0.15
# Ironwood (PostNu6_3) and on what date. Do not block C12 single-asset on the answer.
```

This spike did not email QEDIT.

---

## C0-5 — Mandatory shielding and CosmWasm sends

**Unblocks:** C1's send-restriction hook, and the C9 encoder list that must not punch a hole in it.

### What was read

SDK v0.54.4 `x/bank/keeper/send.go`: `SendCoins` and `InputOutputCoins` both call `sendRestriction.apply`. So does `SendCoinsFromAccountToModule`, `SendCoinsFromModuleToAccount`, and `SendCoinsFromModuleToModule` (`keeper.go`, they call `SendCoins`). `MsgSend` and `MsgMultiSend` (`msg_server.go`) call `IsSendEnabledCoins` and then `SendCoins` / `InputOutputCoins`.

`IsSendEnabledCoins` is **not** called from `SendCoins` itself. Turning the denom off would block `MsgSend` and would not be the hook module code uses.

These paths do **not** call the send restriction:

- `DelegateCoins` / `DelegateCoinsFromAccountToModule` (`keeper.go` around line 138). Staking `Delegate` uses that (`x/staking/keeper/delegation.go`). Self-bond works even if a restriction would have rejected the sender.
- `UndelegateCoins` / `UndelegateCoinsFromModuleToAccount`. Unbonding back to the delegator does not go through the restriction.
- `MintCoins` (separate mint restriction) and `BurnCoins` (no send restriction).

`x/auth/ante/fee.go` `DeductFees` uses `SendCoinsFromAccountToModule` to the fee collector, so a restriction **does** see fee payment. This repo's `x/fees` ante is its own decorator; C1 still has to put the restriction on the bank keeper, not only on `MsgSend`.

`x/distribution` withdraw pays with `SendCoinsFromModuleToAccount` (`keeper/delegation.go`). Default params (`x/distribution/types/params.go`) set `WithdrawAddrEnabled: true`. A delegator can point rewards at another user. The restriction sees module → user, which a "both ends are plain users" rule allows. That is a public payment rail if the distribution module ever holds `norama`.

`chain/x/fees/keeper/earnings.go` `TopUpBondFromEarnings` moves earnings to the signer's **own** bank with `SendCoinsFromModuleToAccount` inside the bond tx. A restriction that rejects every module → plain-user `norama` transfer breaks that top-up. `CreditEarnings` is module → module. `DebitEarningsUpTo` does not move bank coins.

Wasmd **v0.70.3** (read from GitHub, not vendored here):

- `EncodeBankMsg` turns `BankMsg::Send` into `banktypes.MsgSend` from the contract address. The msg server then calls `SendCoins`. The restriction sees contract → recipient.
- `BankCoinTransferrer.TransferCoins` (instantiate/execute funds) checks `IsSendEnabledCoins`, then `SendCoins`. Attached funds hit the restriction.
- `EncodeStakingMsg` emits `MsgDelegate` / `MsgUndelegate`, which use `DelegateCoins` and skip the restriction. A contract can bond its own `norama` and get it back. It cannot redirect the undelegate to someone else; the delegator is the contract.
- `EncodeDistributionMsg` can emit `MsgSetWithdrawAddress`.
- `EncodeAnyMsg` unpacks any registered `sdk.Msg` whose signer is the contract.
- Contract accounts are `BaseAccount`s plus wasm `ContractInfo` (`HasContractInfo`). They are not module accounts. A type switch on `ModuleAccountI` does not see them.

`docs/whitepaper/technical-reference/vol2/39-chain-architecture.md` is still right: this binary has no send restriction yet.

### Decision

C1 registers one `SendRestrictionFn` with `AppendSendRestriction`, for denom `norama` only. Other denoms (C10 user tokens) pass through.

Classify the address:

- module account, if `GetAccount` implements `authtypes.ModuleAccountI`;
- contract, if the wasm keeper's `HasContractInfo` is true (C1's checker returns false until C9 wires wasm);
- otherwise plain.

Reject `norama` when the pair is plain → plain or contract → plain. Allow module on either side, plain → contract, and contract → contract. Do not rewrite the recipient. Return the original `to` or an error.

That allows fees, earnings top-up, module payouts, and the declared public payment into a contract. It blocks user-to-user sends and a contract `BankMsg` that pays a user's bank. Payees of contracts and of the market are credited in `x/fees` earnings (C2), not by `BankMsg`.

Do not use `SendEnabled=false` on `norama`. It is the wrong hook: it misses raw `SendCoins`, and wasmd's `TransferCoins` would then also refuse funding a contract.

Also, in genesis, not as a substitute for the restriction:

- `x/distribution` `withdraw_addr_enabled = false`. Authority is unreachable, so it stays false. Distribution is not the reward path (`x/emission` pays through `x/power` into earnings).
- C9 does not use wasmd's default `Any` encoder or `DistributionMsg::SetWithdrawAddress`. Replace them with a reject. `BankMsg::Send` can stay, because the restriction sees it.
- Staking does not need a hole in the restriction. Delegate and undelegate already skip it, and undelegate returns to the delegator.

`x/authz` stays unwired (C1). A grant is not required for this rule, and it is extra surface.

---

## C0-6 — cNFT leaf availability

**Unblocks:** C11's "leaves come from tx bytes, never only from events."

### What was read and measured

`abci/types/types.go` `DeterministicExecTxResult` copies `Code`, `Data`, `GasWanted`, `GasUsed` and drops everything else. `types/results.go` `NewResults` runs that before the merkle hash. `state/store.go` `TxResultsHash` is that hash. It becomes the next block's `LastResultsHash`.

So events are **not** in the consensus hash. Changing an event, the log, the info string or the codespace does not change `LastResultsHash`. Changing `Data` does. A node can drop events (`DiscardABCIResponses`, and the tx indexer, which C14 turns off on validators) and still agree on the app hash and the results hash. An event is not data availability.

`chain/spikes/events_test.go`:

```
last_results_hash=7e1ab0b6bb09108c0a66ae4112bdd2f4772600ee3b3bb6caa3f82ead54253eb2
events_change_hash=false data_changes_hash=true deterministic_bytes=26
```

The deterministic protobuf of a result that carried a `cnft.leaf` event did not contain that event type or the leaf hex.

Reconstruction cost, `chain/spikes/cnft_tree_test.go`. Leaf is SHA-256 of the C11 fields, fixed width (32+32+32+32+32+8+1), `hash_id` byte `1`. Tree is CometBFT `merkle.HashFromByteSlices` (the same merkle as tx results). One Apple M3, `go1.25.4`, five single-iteration runs of 1,000,000 leaves (field hash + tree), not a warmed loop:

```
348.6 ms, 354.4 ms, 382.3 ms, 382.9 ms, 361.2 ms
```

A separate correctness run of 4,096 leaves, including the field hash and the tree but not the proofs, took 4.72 ms (`root=d899dc0e1691610a0cde577c16716224f1b3de3dfcc117a3625d62977d0dd600`). Proofs for leaf 0 and leaf 4095 verified; a flipped leaf byte did not.

This is an in-memory rebuild of leaf hashes, not a parse of real tx protobufs and not a disk read of a year's blocks.

### Decision

C11 does not treat ABCI events as the leaf. Every leaf is in the tx bytes that `DataHash` commits, and the on-chain root is recomputed from those bytes. Events may mirror a leaf for an indexer. A proof that only an event existed is not an ownership proof.

1,000,000 leaves rebuild in well under a second on this laptop. The C11 1M-leaf test is a CPU check, not a reason to change the design. Parsing tx bytes will add overhead that this spike did not measure. It will not change the "events are not consensus" decision.

---

## C0-7 — Module inventory, store, nullifier disk

**Unblocks:** C1's module list, and C12's "nullifiers are not an IAVL key" (already the plan). IAVLX disk numbers do not exist here.

### Inventory in SDK v0.54.4

| Piece | In this module? | Decision |
|---|---|---|
| `x/epochs` | Yes, `x/epochs`. README and `keeper/abci.go`: after downtime, **one epoch tick per block** until `CurrentEpochStartTime + Duration` catches up to block time. Hook errors are logged and ignored ("purposely ignoring the error"). | **Do not wire it.** `x/emission` `AdvanceBlock` already skips a gap instead of catching up (`keeper/epoch.go`). Wiring epochs to the mint would mint the missed days, one epoch per block. C1's "epochs" line is superseded by this spike. |
| `x/feegrant` | Yes, `x/feegrant`. Already wired. The ante spends the granter's fee allowance to the fee collector. It does not move coins to the grantee. | Keep it. C1 fee sponsorship uses it. |
| PoA | `enterprise/README.md` describes `enterprise/poa`: admin-controlled validator set, Source Available Evaluation License, not Apache-2.0. The v0.54.4 module zip on this machine has the README and **no `enterprise/poa` source**. | **Do not use it.** It is an admin key (D18) and it is not in the module we compile. Bootstrap is `x/power`. |
| `x/crisis` | `contrib/x/crisis`, not `x/crisis`. Keeper comment: deprecated, removed next major release. `MsgVerifyInvariant` is a halt message. | **Do not wire it.** C1's invariant package is the simulation plus `oramad q invariants`. |
| vesting | `x/auth/vesting` exists. `DelegateCoins` tracks vesting and vested amounts. | **Do not register the type.** Zero premine has nothing to vest. A vesting account is extra delegation accounting. |
| `x/authz`, `x/gov`, `x/mint`, `x/nft`, `x/circuit`, `x/group`, `x/protocolpool` | `x/authz`, `x/gov`, `x/mint`, `x/protocolpool` are in the SDK. `nft`, `circuit` and `crisis` are under `contrib/`. `group` is listed next to PoA as an enterprise evaluation-license module; its source is not in this zip either. | Leave them unwired, as C1 already says. `x/mint` is replaced by `x/emission`. `x/gov` is replaced by `x/houses`. No IBC module is in this SDK tree; ibc-go would be a separate module. Do not add it (D25). |
| wasmd v0.70 | Not in `chain/go.mod`. `wasmd` **v0.70.3** `go.mod` (fetched from GitHub) requires `cosmos-sdk` **v0.54.0**, `cometbft` **v0.39.0**, `wasmvm/v3` **v3.0.7**, `ibc-go/v11` **v11.1.0**, `store/v2` **v2.0.0**, `iavl` **v1.2.8**. | C9 may pin wasmd v0.70.3. Go will select this repo's higher SDK v0.54.4 and CometBFT v0.39.4 if both are required. That build was **not** run (it would edit `chain/go.mod`, and wasmvm is cgo). wasmd imports ibc-go. C9 must not register IBC modules or open channels. See C0-5 for the encoders and C0-8 for advisories. |
| Store | `store/v2` v2.0.0 mounts **IAVL v1** (`github.com/cosmos/iavl` v1.2.8), not a different engine. `oramad` already sets `AppDBBackend` to `pebbledb` (`chain/cmd/oramad/cmd/commands.go`) because goleveldb cannot serve versioned queries in this store. Fast node is on unless `--iavl-disable-fastnode` (`server/start.go`, default false). | Stay on IAVL v1.2.8 + PebbleDB. |
| IAVLX | Not a module in this build. The Cosmos Labs 2026 roadmap post (`https://cosmoslabs.io/blog/the-cosmos-stack-roadmap-2026`) uses "IAVLX" as a working name for a rewrite targeted at Q2 2026, with claimed ~20ms commits. Those numbers are theirs, not measured here, and the rewrite is not what v0.54.4 links. | **Not shipped for this binary.** Do not block C1 or C12 on it. Re-check only if a later SDK release actually requires it. |

### Nullifier disk, measured

`chain/spikes/iavl_disk_test.go`. One IAVL version, fast node on, PebbleDB, 32-byte keys, 8-byte values. Directory size after `Close`, including Pebble's own files. Raw Pebble is the same keys with no IAVL.

```
keys=1000  iavl_disk_bytes=198275  iavl_bytes_per_key=198.28  raw_pebble_disk_bytes=67271  raw_bytes_per_key=67.27  iavl_over_raw=2.95
keys=10000 iavl_disk_bytes=1438065 iavl_bytes_per_key=143.81  raw_pebble_disk_bytes=661462 raw_bytes_per_key=66.15  iavl_over_raw=2.17
```

Marginal cost of the extra 9,000 keys (subtract the two rows; this removes most of the fixed file overhead):

- IAVL: 1,239,790 bytes, **138 bytes/key**
- raw Pebble: 594,191 bytes, **66 bytes/key**
- ratio: **2.09×**

That is one version on one laptop. It is not a year of versions, not 1e8 nullifiers, and not IAVLX. Bytes/key at N=1000 is higher because Pebble's files are still in the total. Do not quote 198 bytes/key as the steady cost.

### Decision

C12 keeps nullifiers out of IAVL, in the append-only store the plan already describes. This measurement is why a single IAVL version is the wrong shape to extrapolate, not a reason to wait for IAVLX: the marginal cost is about 2× raw Pebble at one version, and every extra IAVL version retains nodes that a nullifier set does not need. The real per-nullifier number for the append-only store is still unmeasured. C12 measures it on that store, not by multiplying 138 bytes by the year's nullifiers.

---

## C0-8 — CosmWasm advisories CWA-2026-002 … 006

**Unblocks:** C9 may pin a version. It is not a finished security review.

### What was read

`https://github.com/CosmWasm/advisories/blob/main/CWAs/README.md`, fetched 2026-09-28:

| ID | Public content |
|---|---|
| CWA-2026-001 | **Medium**, x/wasm, "Node memory saturation". Affected wasmvm v3.0.2 (and older lines). Patched in wasmvm **v3.0.3** / wasmd v0.61.8, v0.60.5, v0.54.6. Not consensus-breaking. |
| CWA-2026-002 | File exists. Title "TBD". Severity, versions, description, patch: all `TBD`. |
| CWA-2026-003 | Same, TBD. |
| CWA-2026-004 | Same, TBD. |
| CWA-2026-005 | Same, TBD. |
| CWA-2026-006 | Same, TBD. |

The five "Critical" advisories in `plans/open-network.md` D11 are the **Cosmos EVM** advisories, not these CWAs. D11 already rejects the EVM. CWA-2026-002 through CWA-2026-006 are the five the spike names, and all five are TBD. CWA-2026-001 is the one public 2026 CWA.

wasmd v0.70.3 requires wasmvm **v3.0.7**, which is after the 3.0.3 patch for CWA-2026-001. The CosmWasm advisories README still lists wasmvm 3.0.x as supported through **2026-12-31**. That date is inside a mainnet window if genesis is late. It is a maintenance fact, not a bug report.

Wasmd's historical INTEGRATION.md still says the VM is amd64-only. The CWA-2026-001 patch notes name `libwasmvm_muslc.aarch64.a`, so an arm64 static lib exists on the 3.0 line. This spike did not download the v3.0.7 release asset and did not link it.

### Decision

**Go, with a pin, not a no-go.** C9 pins `github.com/CosmWasm/wasmd` **v0.70.3** and `github.com/CosmWasm/wasmvm/v3` **v3.0.7** (or a later 3.0 patch that is not a surprise consensus break; wasmvm's own README says minor bumps are consensus-breaking and patches sometimes are too). Do not pin a wasmvm below 3.0.3.

C9 does not start until someone has re-read `CWAs/README.md`. If 002–006 are still TBD, C9 can still land on a private devnet. Permissionless upload (the genesis sunset, P6) does not open while those five are unpublished, unless the maintainers have answered the question below in writing. A private inquiry was **not** sent.

### Not run (parent)

```
# Not run. Would edit chain/go.mod, and wasmvm's static library is not in this repo.
cd chain
go get github.com/CosmWasm/wasmd@v0.70.3 github.com/CosmWasm/wasmvm/v3@v3.0.7
# Build with CGO_ENABLED=1 against the v3.0.7 libwasmvm asset for linux/amd64
# and linux/arm64. Then:
go list -m github.com/cosmos/cosmos-sdk github.com/cometbft/cometbft github.com/CosmWasm/wasmvm/v3
# Expect cosmos-sdk v0.54.4 and cometbft v0.39.4 (this repo's pins win over
# wasmd's v0.54.0 and v0.39.0). Do not add an IBC module to app.go.
```

Inquiry, not sent:

> To the CosmWasm advisory list (SECURITY.md on CosmWasm/wasmd). We are pinning wasmd v0.70.3 and wasmvm v3.0.7 for a chain that is not launched. CWA-2026-002, 003, 004, 005 and 006 are still "TBD" in CosmWasm/advisories. Please say whether any of them affects wasmvm 3.0.7 or wasmd 0.70.3, and whether a not-yet-launched chain should wait. We are not asking for the embargo text in a public ticket.

---

## What was not run

| Item | Why | Where the command is |
|---|---|---|
| Block time, finality, liveness at 30/60/100/150, three regions, capped power, 32KiB extensions, storage load, shielded verify, TCP vs QUIC | Needs a cluster. localnet.sh is 10 local processes. | C0-2 |
| CometBFT e2e under crashes for inclusion lists | The handlers do not exist yet. | C0-3, last paragraph |
| Ironwood `verify_proof` milliseconds, amd64 and arm64 | No crate and no proof vector in this repo. rustc is installed. It was not used. | C0-4 |
| QEDIT's plan to rebase OrchardZSA onto Ironwood | Not asked. Public evidence says it is not in orchard 0.15.5. | C0-4 |
| `go get` wasmd v0.70.3 against SDK v0.54.4, and a cgo wasmvm link | Would change `chain/go.mod`. Static lib not vendored. | C0-8 |
| IAVLX disk numbers | IAVLX is not linked by SDK v0.54.4. | C0-7 |
| Text of CWA-2026-002…006 | The upstream files are placeholders. | C0-8 |

## What was run

```
cd chain && go test ./spikes/... -count=1
cd chain && go test ./spikes/ -bench=BenchmarkCNFTTreeRebuild -benchtime=1x -count=5
```

Both passed on the Apple M3. The bench figures are the five lines in C0-6.
