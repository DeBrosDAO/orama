# Chain messages and queries

> **At a glance.**
>
> - **Generated** from `chain/proto/orama/*/v1/{tx,query}.proto` by `make whitepaper-gen`. Do not edit by hand: the gate fails when this file and the code disagree.

Every transaction message and query of the Orama chain modules, with its request fields and its proto comment. Volume II explains the modules.

## x/archive

### Messages

Source: `chain/proto/orama/archive/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `Attest` | `archiver string`, `start_height int64`, `end_height int64`, `bundle_cid string`, `bundle_hash bytes`, `merkle_root bytes`, `node_id string`, `piece_root bytes`, `real_leaf_count uint64`, `padded_leaf_count uint64`, `piece_bytes uint64` | Attest pins or confirms the bundle CID, content hash and block-hash Merkle root of an inclusive height range. |
| `AttachReplicas` | `archiver string`, `start_height int64`, `end_height int64`, `deal_ids repeated string`, `node_id string` | AttachReplicas records storage-deal ids for a range. The deals themselves live in x/storage; this message only records their ids. |
| `CreateArchiveDeal` | `archiver string`, `start_height int64`, `end_height int64`, `node_id string`, `piece_root bytes`, `real_leaf_count uint64`, `padded_leaf_count uint64`, `piece_bytes uint64` | CreateArchiveDeal opens a protocol ARCHIVE deal in x/storage for the bundle of an attested range and records it on that range in the same message. |

### Queries

Source: `chain/proto/orama/archive/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/archive/v1/params` | none |  |
| `Range` `/orama/archive/v1/range/{start_height}/{end_height}` | `start_height int64`, `end_height int64` |  |
| `LastArchivedHeight` `/orama/archive/v1/last-archived-height` | none |  |
| `RetainHeight` `/orama/archive/v1/retain-height` | none |  |

## x/cnft

### Messages

Source: `chain/proto/orama/cnft/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `CreateCollection` | `creator string`, `name string`, `royalty_bps uint32` |  |
| `CreateTree` | `creator string`, `collection_id uint64`, `depth uint32`, `buffer uint32`, `canopy uint32` |  |
| `Mint` | `creator string`, `tree_id uint64`, `root bytes`, `leaves repeated MintLeaf` |  |
| `Transfer` | `signer string`, `tree_id uint64`, `current Leaf`, `new_owner string`, `new_delegate string`, `proof MerkleProof` |  |
| `Burn` | `signer string`, `tree_id uint64`, `current Leaf`, `proof MerkleProof` |  |
| `UpdateMetadata` | `signer string`, `tree_id uint64`, `current Leaf`, `new_metadata_cid string`, `proof MerkleProof` |  |
| `Decompress` | `owner string`, `tree_id uint64`, `current Leaf`, `proof MerkleProof` |  |
| `Compress` | `owner string`, `asset_id bytes`, `root bytes` |  |
| `RecordSnapshot` | `creator string`, `tree_id uint64`, `cid string` |  |

### Queries

Source: `chain/proto/orama/cnft/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Collection` `/orama/cnft/v1/collection/{id}` | `id uint64` |  |
| `Tree` `/orama/cnft/v1/tree/{id}` | `id uint64` |  |
| `Decompressed` `/orama/cnft/v1/decompressed` | `asset_id bytes` |  |
| `Snapshots` `/orama/cnft/v1/snapshots/{tree_id}` | `tree_id uint64` |  |

## x/emission

### Messages

Source: `chain/proto/orama/emission/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `Faucet` | `signer string`, `recipient string`, `amount string` | Faucet mints norama to a recipient. It is refused on every production chain-id and unless the genesis enabled it (Params.faucet_enabled). |

### Queries

Source: `chain/proto/orama/emission/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/emission/v1/params` | none |  |
| `CurrentEpoch` `/orama/emission/v1/current-epoch` | none |  |
| `ScheduleAt` `/orama/emission/v1/schedule-at/{epoch}` | `epoch uint64` | QueryScheduleAtRequest asks for the schedule (maximum per-epoch mint and its split) at a given epoch number. |
| `CumulativeMinted` `/orama/emission/v1/cumulative-minted` | none |  |
| `SupplyCapSoFar` `/orama/emission/v1/supply-cap-so-far/{epoch}` | `epoch uint64` | QuerySupplyCapSoFarRequest asks for the maximum cumulative supply the schedule allows up to and including the given epoch (the schedule's maximum, not necessarily what was actually minted). |
| `Invariants` `/orama/emission/v1/invariants` | none |  |

## x/fees

### Messages

Source: `chain/proto/orama/fees/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `WithdrawEarnings` | `signer string`, `amount string` | MsgWithdrawEarnings moves amount from the signer's own earnings account to the signer's own bank balance, where an ordinary public MsgSend can spend it. The destination is never a field: it is always the signer, so earnings cannot be aimed at another address by this message. The amount is positive and at most the signer's earnings balance; a larger amount fails the whole message and moves nothing. |

### Queries

Source: `chain/proto/orama/fees/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/fees/v1/params` | none |  |
| `BaseFee` `/orama/fees/v1/base-fee` | none |  |
| `Earnings` `/orama/fees/v1/earnings/{address}` | `address string` |  |
| `FeeBalance` `/orama/fees/v1/fee-balance/{address}` | `address string` |  |
| `Deposit` `/orama/fees/v1/deposit` | `id string` |  |
| `Invariants` `/orama/fees/v1/invariants` | none |  |

## x/houses

### Messages

Source: `chain/proto/orama/houses/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `SubmitProposal` | `proposer string`, `content ProposalContent` | MsgSubmitProposal opens a proposal. The tier for its content must already be open. During bootstrap this fails. |
| `VoteToken` | `voter string`, `proposal_id uint64`, `option VoteOption` | MsgVoteToken is a direct token-house vote. It overrides the voter's validator for the voter's own bonded stake. Delegated weight is capped separately at tally time. |
| `VoteOperator` | `voter string`, `proposal_id uint64`, `option VoteOption` | MsgVoteOperator is one operator identity's vote. A second message on the same proposal with a different option burns the house bond and is not a vote change. |
| `LockHouseBond` | `signer string`, `amount string` | MsgLockHouseBond locks amount norama from the signer's bank balance into the houses module account, added to any bond already locked. |
| `UnlockHouseBond` | `signer string` | MsgUnlockHouseBond returns the full locked bond. It fails while the signer has a vote on a proposal that is still in voting or the veto window. |
| `ExecuteProposal` | `signer string`, `proposal_id uint64` | MsgExecuteProposal executes a proposal whose timelock has elapsed. Any account may submit it. There is no shorter path. |

### Queries

Source: `chain/proto/orama/houses/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/houses/v1/params` | none |  |
| `Proposal` `/orama/houses/v1/proposal/{proposal_id}` | `proposal_id uint64` |  |
| `Vote` `/orama/houses/v1/vote/{proposal_id}/{voter}` | `proposal_id uint64`, `voter string` |  |
| `HouseBond` `/orama/houses/v1/house-bond/{address}` | `address string` |  |
| `Tiers` `/orama/houses/v1/tiers` | none |  |
| `Enacted` `/orama/houses/v1/enacted` | none |  |
| `Invariants` `/orama/houses/v1/invariants` | none |  |

## x/market

### Messages

Source: `chain/proto/orama/market/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `List` | `seller string`, `tree_id uint64`, `leaf orama.cnft.v1.Leaf`, `proof orama.cnft.v1.MerkleProof`, `price string` |  |
| `CancelListing` | `seller string`, `listing_id uint64` |  |
| `Bid` | `bidder string`, `listing_id uint64`, `amount string` |  |
| `CancelBid` | `bidder string`, `listing_id uint64`, `bid_id uint64` |  |
| `Settle` | `signer string`, `listing_id uint64`, `bid_id uint64`, `leaf orama.cnft.v1.Leaf`, `proof orama.cnft.v1.MerkleProof` | MsgSettle buys a listing at its price when bid_id is 0. Otherwise the seller accepts that bid. The signer is the buyer in the first case and the seller in the second. |

### Queries

Source: `chain/proto/orama/market/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Listing` `/orama/market/v1/listing/{id}` | `id uint64` |  |
| `Bid` `/orama/market/v1/bid/{listing_id}/{bid_id}` | `listing_id uint64`, `bid_id uint64` |  |
| `Invariants` `/orama/market/v1/invariants` | none |  |

## x/nodes

### Messages

Source: `chain/proto/orama/nodes/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `RegisterOperator` | `operator string` |  |
| `RegisterNode` | `operator string`, `node_id string`, `roles repeated Role`, `hot_key string`, `bindings repeated Binding`, `endpoints repeated string`, `region_hint string`, `asn uint32` | MsgRegisterNode verifies every binding signature, including ed25519 (C6, plans/open-network.md "Keys"). |
| `UpdateNode` | `operator string`, `node_id string`, `hot_key string`, `bindings repeated Binding`, `set_endpoints bool`, `endpoints repeated string`, `set_region_hint bool`, `region_hint string`, `set_asn bool`, `asn uint32` | MsgUpdateNode rotates the hot key and bindings. It is the sensitive rotation path (C6, track B4): replaced service pubkeys are retired. |
| `RetireNode` | `operator string`, `node_id string` |  |
| `BondNode` | `operator string`, `node_id string`, `role Role`, `amount string` |  |
| `UnbondNode` | `operator string`, `node_id string`, `role Role`, `amount string` |  |
| `DeclareCapacity` | `operator string`, `node_id string`, `capacity_bytes uint64` | MsgDeclareCapacity sets STORAGE declared_capacity_bytes. The keeper rejects a value above the bond-backed cap (C6). |
| `FundHotKey` | `operator string`, `node_id string`, `amount string` | MsgFundHotKey moves amount from the operator's own earnings account to the earnings (fee) balance of the hot key registered on the operator's own node (C2 item 5). The target is never a field: it is always the node's hot key. |
| `RegisterCluster` | `operator string`, `cluster_id string`, `base_domain string`, `public_endpoints repeated string`, `metadata_uri string` | MsgRegisterCluster adds an optional discovery row. It does not join any node to a cluster (D1, track A8). |
| `UpdateCluster` | `operator string`, `cluster_id string`, `base_domain string`, `public_endpoints repeated string`, `metadata_uri string` |  |
| `RetireCluster` | `operator string`, `cluster_id string` |  |
| `ClaimNodeName` | `operator string`, `node_id string`, `name string` | MsgClaimNodeName claims name for one of the operator's nodes and locks name_deposit. A node holds at most one name, and a name belongs to one node (first come, first served). The name is a DNS label of 3 to 32 characters from a-z, 0-9 and '-', without a leading or trailing '-', and not reserved. |
| `ReleaseNodeName` | `operator string`, `node_id string` | MsgReleaseNodeName gives the node's name up and returns its deposit to the operator. A node that retires releases its name the same way. |

### Queries

Source: `chain/proto/orama/nodes/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/nodes/v1/params` | none |  |
| `Operator` `/orama/nodes/v1/operator/{address}` | `address string` |  |
| `Node` `/orama/nodes/v1/node/{node_id}` | `node_id string` |  |
| `Cluster` `/orama/nodes/v1/cluster/{cluster_id}` | `cluster_id string` |  |
| `NodeUnbondings` `/orama/nodes/v1/node-unbondings/{node_id}` | `node_id string` |  |
| `Invariants` `/orama/nodes/v1/invariants` | none |  |
| `NodeByName` `/orama/nodes/v1/node-by-name/{name}` | `name string` |  |
| `NameOfNode` `/orama/nodes/v1/name-of-node/{node_id}` | `node_id string` |  |
| `NodeNames` `/orama/nodes/v1/node-names` | `pagination cosmos.base.query.v1beta1.PageRequest` | QueryNodeNamesRequest pages through every claimed name in name order. |

## x/power

### Messages

`x/power` has no Msg service.

### Queries

Source: `chain/proto/orama/power/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/power/v1/params` | none |  |
| `BootstrapCommittee` `/orama/power/v1/bootstrap-committee` | none |  |
| `Lambda` `/orama/power/v1/lambda` | none |  |
| `ValidatorPower` `/orama/power/v1/validator-power/{operator_address}` | `operator_address string` |  |
| `Invariants` `/orama/power/v1/invariants` | none |  |

## x/relay

### Messages

Source: `chain/proto/orama/relay/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `RegisterRelay` | `operator string`, `node_id string`, `rsa_fingerprint bytes`, `exit bool`, `ed25519_signature bytes` | RegisterRelay records a relay's RSA identity digest, cross-signed by the ed25519 identity bound to node_id. The per-relay bond is not escrowed here (that is x/nodes). |
| `ReportEpoch` | `reporter string`, `epoch uint64`, `chunk_index uint32`, `chunk_count uint32`, `entries repeated RelayObservation`, `inputs_root bytes` | ReportEpoch submits one chunk of an epoch report. The report counts once every chunk is present and inputs_root matches the canonical entries. |
| `UpdateReporters` | `signer string`, `reporters repeated string` | UpdateReporters replaces the reporter set. It is rejected unless the caller has set AllowReporterChange on the context. x/houses must do that only while executing a passed structural proposal. This module never sets the flag. signer is not an authority. |

### Queries

Source: `chain/proto/orama/relay/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/relay/v1/params` | none |  |
| `Reporters` `/orama/relay/v1/reporters` | none |  |
| `Relay` `/orama/relay/v1/relay/{rsa_fingerprint_hex}` | `rsa_fingerprint_hex string` |  |
| `EpochResult` `/orama/relay/v1/epoch-result/{epoch}` | `epoch uint64` |  |
| `Invariants` `/orama/relay/v1/invariants` | none |  |

## x/shielded

### Messages

Source: `chain/proto/orama/shielded/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `ShieldedTransfer` | `signer string`, `bundle bytes` | MsgShieldedTransfer moves value inside the shielded pool. It has no Cosmos signer: `signer` must be the protocol's fixed signer-less address (types.SignerlessAddress), the tx carries no signature and no fee, and it must be the only message in its tx. The fee is the bundle's value balance: the base part is burned and the rest goes to the block proposer. |
| `Shield` | `signer string`, `bundle bytes` | MsgShield moves norama from the signer's own bank balance into the pool. The bundle's value balance is minus the amount. |
| `ShieldEarnings` | `signer string`, `bundle bytes` | MsgShieldEarnings moves norama from the signer's own earnings account into the pool. The bundle's value balance is minus the amount. |
| `Unshield` | `signer string`, `bundle bytes`, `target UnshieldTarget`, `validator string`, `node_id string`, `role orama.nodes.v1.Role` | MsgUnshield moves value out of the pool to a target owned by the signer. The bundle's value balance is the amount that leaves the pool. |

### Queries

Source: `chain/proto/orama/shielded/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/shielded/v1/params` | none |  |
| `Pools` `/orama/shielded/v1/pools` | none |  |
| `TreeState` `/orama/shielded/v1/tree-state` | none |  |
| `NullifierSpent` `/orama/shielded/v1/nullifier-spent` | `nullifier bytes` |  |
| `Invariants` `/orama/shielded/v1/invariants` | none |  |

## x/storage

### Messages

Source: `chain/proto/orama/storage/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `CreateDeal` | `signer string`, `granter string`, `class DealClass`, `deal_nonce bytes`, `repair_delegate string`, `replicas uint32`, `price_per_epoch string`, `duration_epochs uint64`, `pieces repeated PieceCommitment` | MsgCreateDeal opens a PRIVATE or PUBLIC_PIN user deal. ARCHIVE is rejected. granter, when set, must have granted the signer a DealAuthorization; the granter pays and the grant's limits apply. |
| `ExtendDeal` | `signer string`, `deal_id uint64`, `extra_epochs uint64` | MsgExtendDeal adds epochs to a user deal and escrows the extra price. It does not change the current epoch's subsidy. |
| `GrantDealAuthorization` | `signer string`, `grantee string`, `spend_limit string`, `period_epochs uint64`, `max_piece_bytes uint64`, `max_duration_epochs uint64`, `replicas uint32`, `expiry_epoch uint64` |  |
| `RevokeDealAuthorization` | `signer string`, `grantee string` |  |
| `AcceptDeal` | `signer string`, `node_id string`, `deal_id uint64`, `slot uint32` | MsgAcceptDeal is signed by the assigned node's hot key. |
| `DeclineDeal` | `signer string`, `node_id string`, `deal_id uint64`, `slot uint32`, `reason string` | MsgDeclineDeal is signed by the assigned node's hot key. It has no penalty; the slot is reassigned. |
| `SubmitProofs` | `signer string`, `node_id string`, `proofs repeated ReplicaProof` | MsgSubmitProofs carries one or more challenge proofs. A provider may submit several of these transactions in the same epoch. |
| `ReleaseReplica` | `signer string`, `node_id string`, `deal_id uint64`, `slot uint32`, `reason ReleaseReason` | MsgReleaseReplica drops a replica for a legal reason. It is rate-limited and does not slash. |

### Queries

Source: `chain/proto/orama/storage/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/storage/v1/params` | none |  |
| `Deal` `/orama/storage/v1/deal/{deal_id}` | `deal_id uint64` |  |
| `Slot` `/orama/storage/v1/slot/{deal_id}/{slot}` | `deal_id uint64`, `slot uint32` |  |
| `Authorization` `/orama/storage/v1/authorization/{granter}/{grantee}` | `granter string`, `grantee string` |  |
| `Challenges` `/orama/storage/v1/challenges/{epoch}/{node_id}` | `epoch uint64`, `node_id string` |  |
| `EpochMint` `/orama/storage/v1/epoch-mint/{epoch}` | `epoch uint64` |  |
| `Queue` `/orama/storage/v1/queue` | none |  |
| `NodeFailures` `/orama/storage/v1/node-failures/{node_id}` | `node_id string` |  |
| `Invariants` `/orama/storage/v1/invariants` | none |  |

## x/token

### Messages

Source: `chain/proto/orama/token/v1/tx.proto`

| Msg | Request fields | Description |
|---|---|---|
| `CreateToken` | `creator string`, `subdenom string`, `name string`, `symbol string`, `description string`, `mint bool`, `freeze bool`, `permanent_delegate string`, `transfer_fee_bps uint32`, `non_transferable bool`, `pause bool`, `transfer_hook string` | MsgCreateToken creates factory/&#123;creator&#125;/&#123;subdenom&#125;. The creation fee is burned and a metadata deposit is locked. Capabilities in this message are fixed for the life of the token except by renounce. |
| `Mint` | `sender string`, `denom string`, `recipient string`, `amount string` | MsgMint mints amount of denom to recipient. The signer must still hold mint. |
| `Burn` | `sender string`, `denom string`, `amount string` | MsgBurn burns amount of denom from the signer's own bank balance. |
| `Transfer` | `sender string`, `from string`, `to string`, `denom string`, `amount string` | MsgTransfer moves amount of denom from `from` to `to` by a bank send, after the pause, freeze, non-transferable, and transfer-fee checks. `from` must be the signer, unless the signer is the current permanent delegate. |
| `SetFrozen` | `sender string`, `denom string`, `account string`, `frozen bool` | MsgSetFrozen freezes or unfreezes account. The freeze capability must still be held, and the signer must be the creator. |
| `SetPaused` | `sender string`, `denom string`, `paused bool` | MsgSetPaused pauses or unpauses transfers. The pause capability must still be held, and the signer must be the creator. |
| `Renounce` | `sender string`, `denom string`, `extension Extension` | MsgRenounce drops one capability that is still held. The signer must be the creator, except for permanent delegate, which only that delegate can renounce. |
| `SetShieldable` | `sender string`, `denom string` | MsgSetShieldable sets the one-way shieldable flag. It does not move tokens into a shielded pool. Any signer may call it; it is refused while freeze, permanent delegate, or pause is still held. |
| `DeleteToken` | `sender string`, `denom string` | DeleteToken removes a zero-supply token and releases its metadata deposit. |

### Queries

Source: `chain/proto/orama/token/v1/query.proto`

| Query | Request fields | Description |
|---|---|---|
| `Params` `/orama/token/v1/params` | none |  |
| `Token` `/orama/token/v1/token` | `denom string` |  |
| `Frozen` `/orama/token/v1/frozen` | `denom string`, `account string` |  |
| `Invariants` `/orama/token/v1/invariants` | none |  |
