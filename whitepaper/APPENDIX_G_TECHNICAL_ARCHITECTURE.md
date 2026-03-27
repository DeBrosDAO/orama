# Appendix G: Technical Architecture

This appendix covers the implementation-level technical decisions for the Orama blockchain. These choices are informed by the design principles in the main whitepaper and reflect the specific tradeoffs appropriate for Orama's unique two-layer architecture (global chain + namespaces).

## 1. Dual-State Account Model

Every address on Orama has a single account with two compartments:

```
Account {
    Address          [20]byte    // Account identifier (secp256k1-derived, Ethereum-compatible)
    Username         string      // Optional, immutable once claimed, 3-32 chars [a-z0-9_-]
    Nonce            uint64      // Replay protection

    // Public compartment — visible to everyone
    OramaBalance     uint64      // Balance in rays (1 $ORAMA = 1,000,000 rays)
    BTCBalance       uint64      // Balance in satoshis
    CodeHash         [32]byte    // Hash of contract WASM bytecode (zero for non-contracts)
    StorageRoot      [32]byte    // Merkle root of contract KV storage

    // Private compartment (Phase 2 — zero hashes at genesis)
    CommitmentRoot   [32]byte    // Merkle root of hidden value commitments
    NullifierRoot    [32]byte    // Merkle root of spent-tracking nullifiers
}
```

### Signatures & Addresses

- **Curve**: secp256k1 (same as Bitcoin and Ethereum)
- **Signature**: 65 bytes (r, s, v) — ECDSA
- **Address**: 20 bytes, derived from `Keccak256(public_key)[12:]` (Ethereum-compatible)
- **Display format**: `orama:` prefix for human readability (e.g., `orama:742d35Cc...`)
- **Compatibility**: Same keys and addresses work in RootWallet, Ethereum, and any hardware wallet

### Usernames

Orama accounts can optionally claim a **permanent, human-readable username** tied to their address. Usernames are a protocol-level feature, not a smart contract.

- **Optional**: Accounts can exist without a username. Claim anytime via a `ClaimUsername` transaction.
- **Immutable**: Once claimed, a username can never be changed, transferred, or sold.
- **Unique**: 1:1 mapping between username and address.
- **Format**: Lowercase alphanumeric, hyphens, underscores. 3-32 characters.
- **Free**: Only costs standard transaction gas.
- **Resolvable**: Send to `@alex` instead of `orama:742d35Cc...`. The chain resolves the lookup natively.

The SMT stores a reverse-lookup leaf at `hash(username)` → `address`, enabling O(1) resolution.

### Phase 1 (Testnet): Public Only

All transactions operate on the public compartment. `CommitmentRoot` and `NullifierRoot` are zero hashes. The account structure ships with these fields from genesis — no migration or fork is needed when privacy is activated.

### Phase 2 (Post-PLONK Ceremony): Privacy Activation

The private compartment activates. Funds can be moved between compartments:

- **Shield** (public → private): Deduct from `OramaBalance`, add a Pedersen commitment to the account's commitment tree.
- **Unshield** (private → public): Spend a commitment (publish its nullifier), add to `OramaBalance`. A PLONK proof verifies the commitment was valid.
- **Private transfer**: Spend a commitment in sender's account, create a new commitment in receiver's account. PLONK proof verifies validity without revealing sender, receiver, or amount.

### Why Not a Separate Shielded Pool?

Other chains (Ethereum + Tornado Cash, Aztec) bolt privacy onto an account model via an external "shielded pool" contract. This creates:
- UX friction (explicit deposit/withdraw from pool)
- A privacy leak at the boundary (observers see pool deposits/withdrawals)
- Two incompatible codepaths for wallets

Orama's dual-state model avoids this: privacy is native to each account. One account, two views, no external pool.

## 2. State Tree: Sparse Merkle Tree (SMT)

All state is organized in a Sparse Merkle Tree — a binary tree with 256 levels where most branches are empty (sparse). Each account lives at a leaf determined by `hash(address)`.

```
Global State SMT (root goes in block header)
│
├── leaf[hash(Alice)] → Account data (serialized)
├── leaf[hash(Bob)] → Account data
├── leaf[hash(Contract)] → Account data
│   └── StorageRoot → Contract Storage SMT
│       ├── key1 → value1
│       ├── key2 → value2
│       └── ...
└── (2^256 - N empty leaves, represented by known zero hashes)
```

### Properties

| Property | Value |
|---|---|
| Depth | 256 levels (fixed) |
| Leaf position | `SHA256(address)` |
| Proof size | 256 × 32 bytes = 8 KB (compressible to ~1-2 KB) |
| Non-existence proofs | Built-in (prove an account does NOT exist) |
| Algorithm complexity | ~500 lines of Go |

### Why SMT Over Alternatives

| Tree | Used By | Pros | Cons |
|---|---|---|---|
| Merkle Patricia Trie | Ethereum | Battle-tested 10+ years | Complex (~3000 lines), deep trees, Ethereum is migrating away from it |
| **Sparse Merkle Tree** | **Orama**, Celestia, Mina | Simple, fixed depth, non-existence proofs, predictable performance | Larger raw proofs (mitigated by compression) |
| Verkle Tree | Ethereum (future) | Tiny proofs (~150 bytes) | Requires elliptic curve math, not yet shipped in production, quantum-vulnerable |

SMT's built-in non-existence proofs are critical for the privacy layer — proving a nullifier hasn't been spent requires proving a key does NOT exist in the nullifier tree.

### Nested SMTs

The same algorithm is used at every level of the state:

1. **Global State SMT** — accounts indexed by `hash(address)`, root in block header
2. **Contract Storage SMT** — per-contract KV data, root in account's `StorageRoot`
3. **Commitment SMT** (Phase 2) — per-account hidden commitments, root in `CommitmentRoot`
4. **Nullifier SMT** (Phase 2) — per-account spent nullifiers, root in `NullifierRoot`

One data structure. One algorithm. Clean.

## 3. State Storage: BadgerDB

The underlying key-value store for all chain state is **BadgerDB** — a pure Go, high-performance, LSM-tree-based database.

### Why BadgerDB

| Requirement | BadgerDB |
|---|---|
| Pure Go (no CGO) | Yes — compiles cleanly on any platform including OramaOS |
| Concurrent reads | Lock-free MVCC reads |
| Batched writes | Transaction-based batch writes |
| Crash safety | Write-ahead log with checksums |
| Used in blockchain | IPFS, libp2p ecosystem (already in Orama's dependency tree) |
| Maintenance | Actively maintained by Dgraph team |

### Storage Layout

```
/var/lib/orama/chain/
├── blocks/           # Block storage (BadgerDB)
│   └── (block headers, transaction lists, receipts)
├── state/            # Current state (BadgerDB)
│   └── (SMT nodes, account data, contract storage)
└── index/            # Query indexes (optional, per-node)
    └── (tx-by-address, blocks-by-height, etc.)
```

The `blocks/` and `state/` databases are consensus-critical — every node computes identical content. The `index/` database is per-node, non-consensus, and supports the rich query RPC layer.

## 4. Serialization: Borsh

All data that is hashed, stored, or transmitted over the wire uses **Borsh** (Binary Object Representation Serializer for Hashing) — a deterministic binary serialization format created specifically for blockchain use.

### Why Borsh

| Requirement | Borsh | Protobuf | RLP (Ethereum) |
|---|---|---|---|
| Deterministic | By design | No (maps unordered) | By design |
| Code generation | None (struct tags) | Required (.proto files) | None |
| Schema | Implicit from struct | External .proto | None |
| Created for | Blockchain hashing | RPC/APIs | Ethereum |
| Compact | Yes | Yes | Yes |

### Example

```go
type Block struct {
    Height       uint64        `borsh:"height"`
    ParentHash   [32]byte      `borsh:"parent_hash"`
    StateRoot    [32]byte      `borsh:"state_root"`
    Timestamp    uint64        `borsh:"timestamp"`
    ProposerAddr [20]byte      `borsh:"proposer"`
    Transactions []Transaction `borsh:"transactions"`
    QC           QuorumCert    `borsh:"qc"`
}

// Deterministic: same data always produces identical bytes
bytes := borsh.Serialize(block)
blockHash := sha256(bytes)
```

## 5. Consensus: HotStuff BFT

The global chain uses a pipelined BFT protocol based on HotStuff. See Section 4 of the main whitepaper for the full protocol description.

### Key Implementation Details

**Message types:**

| Message | Direction | Content |
|---|---|---|
| `Propose` | Leader → All | Block + QC for previous round |
| `Vote` | Validator → Next Leader | Signed block hash + validator ID |
| `NewView` | Validator → New Leader | Timeout certificate (on leader failure) |

**Quorum Certificate (QC):** An aggregation of 2/3+ votes (by Effective Power) for a specific block. The QC is the proof that consensus was reached.

**View change:** If the current leader fails to propose within the timeout (configurable, default 4 seconds), validators send a `NewView` message to the next leader in the rotation. The next leader can immediately propose, including the timeout certificate as justification. No rounds are wasted on view-change voting.

**Pipelining:** Votes for block N serve as the QC for block N-1, which finalizes block N-2. This means:
- Block produced: every 6 seconds
- Block finalized: 18 seconds later (3 blocks)
- No dead time between rounds

### P2P Transport

Consensus messages are Borsh-serialized and transported over LibP2P pubsub topics, running on top of the WireGuard encrypted mesh. All validator-to-validator communication is authenticated and encrypted at the network layer.

**Topics:**

| Topic | Content |
|---|---|
| `/orama/chain/1/blocks` | Block proposals from leaders |
| `/orama/chain/1/votes` | HotStuff votes from validators |
| `/orama/chain/1/txs` | New transactions (mempool gossip) |
| `/orama/chain/1/newview` | View-change messages (leader timeout) |

**Message envelope:**
```
Message {
    Type:      uint8       // Block, Vote, Transaction, NewView
    Payload:   []byte      // Borsh-serialized content
    Sender:    [20]byte    // Sender address
    Signature: [65]byte    // ECDSA signature over payload
}
```

## 6. Namespace Architecture

Namespaces are provisioned from the global node pool. They come in tiers based on trust requirements.

### Namespace Tiers

| Tier | Name | Purpose | Blockchain Interaction |
|---|---|---|---|
| 0 | Cloud | App deployment (what namespaces are today) | None |
| 1 | Secured | Smart contracts, tokens, NFTs, anything with value | State commitments to global chain, staked validators |
| 2 | Trustless (future) | High-value protocols needing maximum security | ZK validity proofs |

**Tier 0** is the default. Existing namespaces are Tier 0. No changes to current behavior. Developers deploy apps just like today — no blockchain knowledge needed.

**Tier 1** adds verifiable state on top of the same infrastructure. It's Tier 0 plus three additions: a StateDB (BadgerDB + SMT) for cryptographic state proofs, a TxLog for replay/verification, and staked validators who submit state commitments to the global chain every epoch.

### Tier 0 Cluster Composition (unchanged)

| Service | Nodes | Purpose |
|---|---|---|
| RQLite | 3+ | SQL database with Raft consensus |
| Olric | 3+ | Distributed KV cache |
| Gateway | 3+ | HTTP/WebSocket API, WASM VM, RPC |
| IPFS | Shared | Content-addressed storage |
| SFU + TURN | Optional | WebRTC voice/video |

### Tier 1 Cluster Composition (adds verifiable state)

| Service | Nodes | Purpose |
|---|---|---|
| RQLite | 3+ | SQL database — developer queries, powers the RPC |
| **StateDB** | **3+ (per node)** | **BadgerDB + SMT — verifiable state, produces Merkle root** |
| **TxLog** | **3+ (per node)** | **Append-only operation log for replay/verification** |
| Olric | 3+ | Distributed KV cache |
| Gateway | 3+ | HTTP/WebSocket API, WASM VM, RPC |
| IPFS | Shared | Content-addressed storage |
| SFU + TURN | Optional | WebRTC voice/video |

**How the three storage layers work together in Tier 1:**

- **RQLite** = the read layer. Developers query with SQL. The RPC serves data from it. Fast and flexible, but not cryptographically verifiable.
- **StateDB** = the proof layer. Stores the same data in a Sparse Merkle Tree (BadgerDB). Produces the state fingerprint (Merkle root) that gets committed to the global chain. Verifiable but not queryable with SQL.
- **TxLog** = the replay layer. Ordered list of every state-changing operation. If someone challenges the namespace, they replay the log, compute the expected state, and compare with the submitted fingerprint.

Contracts write to both RQLite and StateDB atomically. They always agree on the current state.

### State Commitment Flow

The commitment mechanism strengthens across phases:

**Testnet: Attested Commitment**
```
1. Namespace executes transactions against its local state
2. At each epoch boundary, the namespace computes a state root:
   hash(rqlite_state || olric_state || contract_storage || metadata)
3. 2/3+ of the namespace's validators sign the state root
4. Submit StateCommitment transaction to the global chain:
   { namespace_id, epoch, state_root, validator_signatures }
5. Global chain records the commitment in the namespace registry
```

**Mainnet: Optimistic with Challenge Period**
```
1-4. Same as above
5. State root enters "pending" state for ~100 blocks (10 minutes)
6. During pending period, anyone can submit a fraud proof:
   → provide the transactions + correct state root
   → if challenge is valid, namespace validators are slashed, state root rejected
7. After challenge period with no valid challenge → state root finalized
```

**Future: ZK Validity Proofs**
```
1-2. Same as above
3. Namespace generates a zero-knowledge proof that state transitions are correct
4. Submit proof + new state root to global chain
5. Global chain verifies proof (~5ms) → immediately finalized
```

### Namespace Provisioning

Namespace clusters are provisioned dynamically from the global node pool. Each physical node can host multiple namespace instances (up to 20 per node, constrained by port allocation). The cluster manager handles:

- Node selection (balanced load distribution)
- Port allocation (dedicated port block per namespace)
- Service bootstrapping (RQLite → Olric → Gateway, in dependency order)
- DNS record creation (namespace subdomain)
- Health monitoring and recovery

This infrastructure already exists in the Orama Network codebase (`pkg/namespace/`).

## 7. Block Structure

```
Block {
    // Header
    Height:          uint64
    ParentHash:      [32]byte
    StateRoot:       [32]byte      // SMT root after executing all transactions
    TransactionsRoot:[32]byte      // Merkle root of transaction list
    ReceiptsRoot:    [32]byte      // Merkle root of execution receipts
    Timestamp:       uint64        // Unix timestamp (seconds)
    ProposerAddr:    [20]byte      // Block proposer address
    EpochNumber:     uint64        // Current epoch
    QC:              QuorumCert    // Quorum Certificate from previous round

    // Body
    Transactions:    []Transaction
}

Transaction {
    Type:        uint8          // Transfer, Stake, Unstake, DEXOrder, BridgeDeposit,
                                // BridgeWithdraw, NamespaceCommit, GovernanceVote, ...
    From:        [20]byte
    To:          [20]byte
    Amount:      uint64         // In rays or satoshis depending on asset
    Asset:       uint8          // 0 = $ORAMA, 1 = BTC
    Nonce:       uint64
    GasLimit:    uint64
    GasTipCap:   uint64         // Priority fee (EIP-1559)
    GasFeeCap:   uint64         // Max total fee (EIP-1559)
    Data:        []byte         // Type-specific payload
    Signature:   [65]byte       // ECDSA signature (secp256k1)
}
```

## 8. RPC API

Orama uses a **REST API** as its primary interface. No JSON-RPC.

### Global Chain Endpoints

```
GET  /v1/accounts/{address_or_username}
GET  /v1/accounts/{address_or_username}/balance
GET  /v1/accounts/{address_or_username}/transactions?limit=20&sort=timestamp&order=desc
POST /v1/transactions/send
GET  /v1/transactions/{hash}
GET  /v1/blocks/{height_or_hash}
GET  /v1/blocks/latest
GET  /v1/dex/orderbook
GET  /v1/dex/orders?owner=@alex
POST /v1/dex/orders
GET  /v1/bridge/status
GET  /v1/validators
GET  /v1/chain/status
```

### Namespace Endpoints (served by namespace gateway)

```
GET  /v1/namespace/{name}/contracts
GET  /v1/namespace/{name}/contracts/{address}/state/{key}
POST /v1/namespace/{name}/contracts/{address}/call
GET  /v1/namespace/{name}/query?sql=SELECT...
```

Usernames are resolvable in any endpoint: `@alex` is equivalent to `orama:742d35Cc...`.

All responses support filtering, sorting, and pagination via query parameters. This matches the existing Gateway pattern in `pkg/gateway/`.

## 9. WASM Contract Host Functions

These are the "system calls" available to WASM contracts running in a namespace.

### Database (RQLite)
```
orama.db.execute(query_ptr, query_len, params_ptr, params_len) → result_ptr
orama.db.query(query_ptr, query_len, params_ptr, params_len) → result_ptr
orama.db.query_one(query_ptr, query_len, params_ptr, params_len) → result_ptr
```

### Cache (Olric)
```
orama.cache.get(key_ptr, key_len) → result_ptr
orama.cache.set(key_ptr, key_len, val_ptr, val_len, ttl_seconds) → status
orama.cache.delete(key_ptr, key_len) → status
```

### Storage (IPFS)
```
orama.storage.put(data_ptr, data_len) → cid_ptr
orama.storage.get(cid_ptr, cid_len) → data_ptr
```

### Token Operations (global chain interaction)
```
orama.transfer_orama(to_ptr, amount) → status
orama.transfer_btc(to_ptr, amount) → status
orama.get_balance(address_ptr, asset) → amount
```

### Context (read-only)
```
orama.ctx.caller() → address_ptr
orama.ctx.block_height() → uint64
orama.ctx.block_timestamp() → uint64
orama.ctx.orama_value() → uint64
orama.ctx.btc_value() → uint64
```

### Events, Logging, Crypto
```
orama.emit(event_ptr, event_len) → status
orama.log.info(msg_ptr, msg_len)
orama.log.error(msg_ptr, msg_len)
orama.crypto.sha256(data_ptr, data_len, out_ptr)
orama.crypto.keccak256(data_ptr, data_len, out_ptr)
orama.crypto.verify_signature(msg_ptr, msg_len, sig_ptr, addr_ptr) → bool
```

**Design principles:**
- No filesystem, no raw network, no non-deterministic operations
- `block_timestamp` is the block's timestamp (deterministic), not system time
- No `random` — use `block_hash` as a deterministic seed if needed
- Contracts write SQL directly — no ORM or query builder in the host layer
- Events are indexed by the namespace gateway for rich RPC queries

## 10. Implementation Language

The entire blockchain layer is implemented in **Go**, consistent with the existing Orama Network codebase. Key dependencies:

| Dependency | Purpose | Status |
|---|---|---|
| BadgerDB | State and block storage | To be added |
| wazero | WASM contract execution | Already in project |
| LibP2P | P2P networking, gossip | Already in project |
| go-ethereum/crypto | secp256k1 signatures | Already in project |
| gnark (future) | PLONK zk-SNARKs | Phase 2 |

The blockchain packages will be added to the existing monorepo under `pkg/chain/`.
