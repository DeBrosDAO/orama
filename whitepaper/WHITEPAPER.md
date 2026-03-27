# Orama Network: The Eternal Decentralized Computer and Financial System

**Whitepaper Version 4.0**
**Date:** March 2026
**Author:** DeBros

## 1. Abstract

Orama Network is a standalone Layer-1 blockchain designed to serve as humanity's eternal decentralized computer and financial system. It combines the security and scarcity of Bitcoin with the full power of a global, censorship-resistant cloud infrastructure — all in one protocol.

Built from first principles for a 1,000-year horizon, Orama delivers:
- **Native BTC compatibility** from genesis (deposit, use, and withdraw BTC with progressively trust-minimized security).
- **Namespace-based execution** — smart contracts deploy into isolated environments with dedicated SQL databases, caches, storage, and API gateways. No shared bottleneck. No external indexing infrastructure. The namespace IS the backend.
- **Pure WASM smart contracts** so developers can write in any language they want (Rust, Go, TypeScript, C++, and any language that compiles to WebAssembly).
- **Per-transaction public/private toggle** using PLONK zk-SNARKs for optional privacy, built natively into the dual-state account model.
- **HotStuff-based BFT consensus** with Hybrid PoS + Proof of Contribution + Proof of Infrastructure, giving real power to ordinary people running nodes with OramaOS.
- **210 million $ORAMA** hard-capped supply with zero pre-mine — 100% of tokens are earned through mining, just like Bitcoin.

Orama is not an upgrade to existing chains. It is the base layer that millions of people and billions of devices will rely on for compute, storage, payments, and data ownership for centuries to come.

## 2. Introduction & Problem Statement

Centralized cloud providers control the internet's infrastructure. They can censor, surveil, or shut down services at will. At the same time, Bitcoin remains the most secure digital money ever created, yet it lacks a native programmable computer.

Existing Layer-1 blockchains force developers into rigid languages, expensive gas models, or centralized validator sets. Most projects also suffer from unfair token launches, infinite inflation, or governance capture.

Orama solves both problems at once:
- It is the **decentralized world computer** — but unlike Ethereum's "one global computer" model where every validator executes every contract, Orama gives each application its own isolated execution environment (namespace) with dedicated databases, caches, storage, and APIs. No application can clog another. No external infrastructure needed.
- It is the **Bitcoin-grade financial system** — BTC-only economy, native BTC bridge, scarce $ORAMA token, and per-transaction privacy.

## 3. Orama Network Solution & High-Level Architecture

Orama is a single Layer-1 chain with a unique two-layer architecture that solves the biggest problem in blockchain: **shared resource contention**. On Ethereum, a viral game clogs the entire network. On Orama, every application gets its own isolated infrastructure.

### The Two Layers

1. **Global Chain** (Immutable Financial Core) — all nodes participate. Handles $ORAMA and BTC balances, the native DEX, the BTC bridge, staking, slashing, and the namespace registry. Designed to be unchangeable for 1,000 years.

2. **Namespaces** (Isolated Execution Environments) — smart contracts deploy into namespaces, not onto the global chain. Each namespace is a dedicated cluster of nodes with its own database, cache, storage, API gateway, and WASM execution engine. Namespaces periodically commit state roots to the global chain, providing cryptographic proof that their execution is correct.

```
┌──────────────────────────────────────────────────────────────┐
│  GLOBAL CHAIN (all nodes participate via HotStuff consensus) │
│                                                              │
│  • $ORAMA / BTC balances       • Namespace registry          │
│  • Native DEX order book       • State root commitments      │
│  • BTC bridge                  • Staking & slashing          │
│  • Block rewards & emission    • Governance                  │
└─────────────┬──────────────────┬──────────────────┬──────────┘
              │                  │                  │
     ┌────────▼───────┐ ┌───────▼────────┐ ┌──────▼─────────┐
     │  Namespace A   │ │  Namespace B   │ │  Namespace C   │
     │  "GameFi App"  │ │  "DeFi Proto"  │ │  "NFT Market"  │
     │                │ │                │ │                │
     │  Own DB        │ │  Own DB        │ │  Own DB        │
     │  Own cache     │ │  Own cache     │ │  Own cache     │
     │  Own gateway   │ │  Own gateway   │ │  Own gateway   │
     │  Own WASM VM   │ │  Own WASM VM   │ │  Own WASM VM   │
     │  Own IPFS      │ │  Own IPFS      │ │  Own IPFS      │
     └────────────────┘ └────────────────┘ └────────────────┘
```

### Why This Architecture

Every other Layer-1 blockchain uses a single global state machine — every validator executes every smart contract. This creates a fundamental bottleneck: all applications compete for the same block space, the same throughput, and the same compute. A popular NFT mint can make a DeFi protocol unusable.

Orama's namespace model eliminates this problem by design:

- **No shared bottleneck** — each namespace runs on its own cluster of nodes with dedicated resources. One namespace cannot affect another.
- **Real developer infrastructure** — contracts in a namespace have access to a real SQL database (RQLite with Raft consensus), a real distributed cache (Olric), real IPFS storage, and a dedicated API gateway with rich query capabilities. No external indexing infrastructure needed.
- **Natural scaling** — adding nodes to the network allows more namespaces to be provisioned. No sharding complexity, no rollup escape hatches.
- **The global chain stays lean** — consensus only processes financial transactions and state commitments, not contract execution. This means higher throughput for the operations that matter most.

### Namespace Trust Model

Namespaces commit cryptographic state roots to the global chain every epoch. The global chain does not re-execute namespace transactions — it verifies that the committed state is correct.

- **Testnet & Mainnet V1**: Namespace nodes are staked validators. If they commit an incorrect state root, they are slashed. The number of nodes per namespace scales with the value at stake (minimum 3, higher for high-value protocols).
- **Mainnet V2 (future)**: Optimistic fraud proofs — a challenge period during which anyone can prove a state root is incorrect, triggering slashing.
- **Long-term (future)**: ZK validity proofs — namespaces generate zero-knowledge proofs that their state transitions are correct. Trustless verification without re-execution.

All nodes run on real hardware with OramaOS, creating true "power to the people" instead of stake-weighted whales.

## 4. Consensus Mechanism

Orama uses a **HotStuff-based BFT consensus protocol** combined with a **Hybrid PoS + Proof of Contribution + Proof of Infrastructure** validator selection model. The consensus protocol determines how blocks are produced and agreed upon. The hybrid model determines who gets to participate and how rewards are distributed.

### Consensus Protocol: HotStuff BFT

The global chain uses a pipelined BFT (Byzantine Fault Tolerant) protocol based on HotStuff. Unlike classical BFT protocols (Tendermint/PBFT) that require O(n²) messages per block — where every validator talks to every other validator — HotStuff achieves O(n) message complexity through leader-driven voting. This scales cleanly to 300+ validators.

**How a block is produced (every 6 seconds):**

1. **Leader Selection** — a block proposer is selected deterministically, weighted by Effective Power. Every node computes the same leader independently.
2. **Propose** — the leader collects transactions from the mempool, executes them against the current state, computes the new state root, and broadcasts the block.
3. **Vote** — all validators verify the block (re-execute transactions, confirm the state root matches) and send a signed vote to the next leader.
4. **Aggregate** — the next leader collects votes. When 2/3+ of Effective Power has voted, a Quorum Certificate (QC) is formed.
5. **Finalize** — the QC is included in the next block, finalizing the block from 2 rounds ago.

This pipelined approach means a new block is proposed every 6 seconds, with finality achieved in 18 seconds (3 blocks). There is no dead time between rounds.

### Effective Power Formula
$$
\text{Effective Power} = \text{Staked \$ORAMA} \times (1 + \text{Contribution Score}) \times \text{Infrastructure Multiplier}
$$

- **Proof-of-Stake**: Classic staking for economic security.
- **Proof of Contribution**: Real work performed (measured on-chain every epoch).
- **Proof of Infrastructure**: Nodes running OramaOS receive a multiplier, rewarding operators who run the hardened, secure operating system.

### Infrastructure Multiplier
- Running official OramaOS = **1.5× multiplier**
- Running without OramaOS = **1.0× (no bonus)**

A node runner with modest stake but perfect uptime, real contribution, and OramaOS can earn significantly more than a whale who only stakes large amounts of $ORAMA with no real infrastructure.

### Contribution Score (weighted every 1-hour epoch)
- Uptime: 40%
- Bandwidth served: 30%
- Compute/storage/namespace queries served: 20%
- Low latency & reliability: 10%

**Block time**: 6 seconds (14,400 blocks per day)
**Block capacity**: 1,000 transactions per block
**Epoch length**: 1 hour (600 blocks per epoch)
**Finality**: 18 seconds (3 blocks via HotStuff pipeline)
**Epoch checkpoints**: Every epoch, an additional BFT checkpoint is signed by 2/3+ of Effective Power as an extra layer of irreversibility.
**Minimum stake to validate**: 1,000 $ORAMA (mainnet only — see bootstrap below)
**Slashing**:
- Double-signing or cheating → 100% slash
- Downtime > 20% → progressive slash (5–30%)
- False infrastructure attestation → 50% slash

OramaOS attestation uses TPM-based remote attestation — cryptographically verified on-chain.

### Why HotStuff Over Other Protocols

| Protocol | Message Complexity | Finality | Max Validators | Used By |
|---|---|---|---|---|
| PBFT / Tendermint | O(n²) | 1 block (~6s) | ~200 | Cosmos |
| **HotStuff (Orama)** | **O(n)** | **3 blocks (~18s)** | **1,000+** | **Orama, Aptos (variant)** |
| Nakamoto (PoW) | O(n) | ~60 min (6 blocks) | Unlimited | Bitcoin |
| DAG-based | O(n) | 1-2 rounds | 1,000+ | Sui |

HotStuff gives Orama the best balance: linear scaling for 300+ validators today with room to grow to 1,000+, near-instant finality, and a clean leader rotation mechanism that integrates naturally with the Effective Power model.

### Staking Bootstrap

During testnet, **no staking is required** to run a node. Any node operator can participate and earn $ORAMA block rewards with zero stake. Testnet tokens carry over to mainnet — there is no reset. The tokens earned during testnet are real $ORAMA on the real chain.

At mainnet launch, the 1,000 $ORAMA minimum stake activates. By then, every testnet node runner will have earned more than enough to stake.

For new node runners joining after mainnet: acquire BTC, bridge it onto Orama, purchase $ORAMA on the native order book or bonding curve, stake, and begin earning.

## 5. Network Primitives & Execution Environment

### Dual-State Account Model

Every address on Orama has a single account with two compartments:

```
Account {
    address:     0xABC...
    nonce:       42

    // Public compartment — visible to everyone
    public: {
        orama_balance: 500,000,000 rays
        btc_balance:   50,000 sats
        code_hash:     0x...        // for contract accounts
        storage_root:  0x...        // Merkle root of contract storage
    }

    // Private compartment (Phase 2 — activated post-PLONK ceremony)
    private: {
        commitment_root: 0x...      // Merkle root of hidden value commitments
        nullifier_root:  0x...      // Merkle root of spent-tracking nullifiers
    }
}
```

**Public transactions** operate on the public compartment — visible, fast, and cheap. **Private transactions** (Phase 2) operate on the private compartment using PLONK zk-SNARKs, with funds moved between compartments via explicit shield/unshield operations.

This dual-state design is unique to Orama. Unlike hybrid approaches that bolt a separate "shielded pool" onto an account model, Orama's privacy is native to each account — one account, two views, no external pool contract. See Section 6 for details.

### Smart Contract Execution: Namespace Model

**Execution VM**: Pure WebAssembly (WASM) from genesis. Developers can write smart contracts in **any language** that compiles to WebAssembly — Rust, Go, TypeScript, C++, and more. No EVM, no Solidity required.

Unlike other blockchains where every validator executes every contract on a single shared state machine, **Orama smart contracts deploy into namespaces** — isolated execution environments with dedicated infrastructure.

**What a namespace provides to a smart contract:**

| Primitive | Implementation | What It Gives Developers |
|---|---|---|
| **SQL Database** | RQLite (distributed SQL with Raft consensus) | Real SQL queries — SELECT, INSERT, UPDATE, DELETE with JOINs, indexes, and transactions |
| **Key-Value Cache** | Olric (distributed, consistent hashing) | Sub-millisecond reads, TTL-based expiry, perfect for hot data |
| **Object Storage** | IPFS (content-addressed, replicated) | Store files, images, metadata — addressed by content hash |
| **API Gateway** | Dedicated HTTP/WebSocket gateway | Rich query API with filtering, pagination, sorting — no external indexer needed |
| **WASM Runtime** | wazero (pure Go WebAssembly engine) | Sandboxed execution with host functions for all primitives |
| **WebRTC** | Pion SFU + TURN (optional) | Real-time voice, video, and data channels |

**This solves Ethereum's biggest developer pain point.** On Ethereum, deploying a contract is only the beginning — you then need The Graph for indexing, Alchemy for RPC, IPFS pinning services for storage, and a separate backend for anything that requires a database. On Orama, the namespace IS your backend. Deploy a contract and you immediately have a database, cache, storage, and a queryable API.

### How Contract Deployment Works

```
1. Developer writes a WASM contract (Rust, Go, TypeScript, etc.)
2. Developer deploys to a namespace (creates one or uses existing)
3. The namespace provisions a dedicated cluster:
   - 3+ nodes with RQLite, Olric, IPFS access, Gateway, WASM VM
   - Dedicated ports, DNS subdomain, isolated from other namespaces
4. The contract runs in the namespace's WASM engine
5. The namespace commits state roots to the global chain every epoch
6. The global chain records the commitment — it does not re-execute
```

### Rich Query RPC

Every namespace's API gateway serves powerful, filterable queries over contract data — eliminating the need for external indexing infrastructure:

```
GET /v1/contracts/{address}/state/users?sort=created_at&order=desc&limit=20
GET /v1/transactions?address=0xABC...&limit=50
GET /v1/nfts?owner=0xABC...&collection=0xDEF...
GET /v1/blocks/12345
```

The node indexes contract state changes locally and serves them through the RPC. No Graph. No subgraphs. No external infrastructure. One API call.

### Global Chain Primitives

The following primitives live on the global chain (not in namespaces) because they are part of the immutable financial core:

- **$ORAMA and BTC transfers** — direct balance changes on the global state
- **Native DEX order book** — the $ORAMA/BTC trading pair (see Section 9)
- **BTC bridge** — deposit and withdrawal of BTC (see Section 7)
- **Staking and slashing** — validator economics
- **Governance** — on-chain voting (see Section 12)
- **Namespace registry** — which namespaces exist, their owners, and their committed state roots

Gas for global chain transactions is paid in $ORAMA. Base fee is burned. All global primitives integrate seamlessly with the public/private toggle.

### AI Marketplace & Angels

Orama has a native **AI Marketplace** — a protocol-level primitive for hosting and consuming AI models and AI agents (called **Angels**).

**For compute providers:**
- Register compute capacity on the network and host AI models or Angels (autonomous AI agents).
- Get paid per API call in $ORAMA — pricing is set by the provider and visible to all callers.
- Compute providers do **not** receive extra block rewards. Their revenue comes entirely from marketplace demand.

**For developers and users:**
- Call any hosted AI model or Angel from WASM contracts or via RPC.
- Pay per use in $ORAMA — transparent pricing, competitive marketplace.

**For Angel builders:**
- Deploy autonomous AI agents that can interact with Orama's namespace primitives (SQL, storage, cache, BTC bridge, DEX).
- Angels can hold $ORAMA, execute transactions, manage data, and interact with other Angels.
- Revenue model: builders set per-request or subscription pricing in $ORAMA.

**Compute provider economics:**
- Compute providers run standard Orama nodes (earning normal block rewards) plus optional AI capacity.
- AI revenue is purely market-driven — if demand is high, providers earn well. If demand is low, they still earn normal mining rewards from their standard node.
- The protocol caps compute-provider nodes at **10% of total network nodes** to prevent disproportionate influence.
- A provider with expensive hardware earns the same block rewards as any other node — plus whatever the marketplace pays them. The market decides if the investment is worth it.

## 6. Privacy Model

Every transaction has a simple **public/private toggle**:

- **Public** (default): Fully transparent, lowest cost.
- **Private**: Uses PLONK-based zk-SNARKs to shield sender, receiver, and amount. Only the participants know the details.

### Why PLONK

Zero-knowledge proofs require a cryptographic setup. Older systems (Groth16) need a new trusted setup ceremony for every circuit change — if any participant in the ceremony is dishonest, the system can be compromised. PLONK uses a **universal trusted setup**: performed once at genesis with hundreds of public participants, and valid for all future circuit upgrades. As long as one participant was honest and destroyed their contribution, the system is secure forever.

This means Orama can upgrade its privacy features (new transaction types, improved circuits) without ever needing another ceremony.

### Privacy Mechanics

- Gas cost for private mode = **4×** public mode (covers ZK proving).
- Smart contracts can enforce "private-only" mode for sensitive applications.
- Private transactions hide sender, receiver, and amount — but the transaction's existence is still visible on-chain.
- No network-level onion routing — privacy is pure cryptography, not traffic obfuscation.

## 7. Native BTC Integration & Bridge

### BTC-Only Economy

Orama has exactly two assets: **BTC** and **$ORAMA**. No stablecoins. No wrapped altcoins. No fiat pegs. Nothing else.

To acquire $ORAMA, you must use BTC. This is a deliberate design choice:
- **Zero counterparty risk** beyond Bitcoin itself — no exposure to stablecoin depegs, altcoin crashes, or centralized token issuers.
- **No dependence on external exchanges** — Orama's economy is self-contained.
- **Hard money priced in hard money** — $ORAMA's value is always denominated in BTC, the most battle-tested digital asset in existence.

If someone wants to buy $ORAMA, they acquire BTC (anywhere in the world), bridge it onto Orama, and trade on the native order book. If they want to exit, they sell $ORAMA for BTC and bridge it back to Bitcoin mainnet.

### Trust-Minimized BTC Bridge

Orama has a **native BTC bridge built into the protocol from genesis**, designed to progressively increase its trust guarantees as the network matures.

- Deposit BTC → receive native BTC on Orama (1:1).
- Use BTC to buy $ORAMA, pay for services, or use in smart contracts.
- Withdraw back to Bitcoin mainnet.

The bridge is further backed by a **protocol reserve** — BTC accumulated from bonding curve sales (see Section 9). This reserve provides additional collateral beyond the 1:1 deposits, ensuring the bridge remains solvent even under extreme conditions.

### Bridge Phases

The bridge security model strengthens over time, from validator-secured to cryptographically verified:

**Testnet → Mainnet V1: Validator Threshold Bridge**

The top validators by Effective Power form a threshold signature group. Deposits are detected by validators watching the Bitcoin chain; withdrawals require a supermajority (e.g., 7-of-10) of the validator group to sign the Bitcoin transaction. Validators are staked — cheating means losing their entire stake. This model is proven, ships fast, and enables the full BTC economy (DEX, bonding curve, bridge fees) from day one.

**Mainnet V2: Light Client + Optimistic Fraud Proofs**

A Bitcoin SPV (Simple Payment Verification) light client is embedded in the Orama protocol. The chain verifies Bitcoin block headers and Merkle inclusion proofs natively — deposits are verified cryptographically with no human attestation required.

Withdrawals use an optimistic model: a withdrawal is posted and enters a challenge period (e.g., 24 hours). If any validator can prove the withdrawal is invalid, it is reverted and the submitter is slashed. Security assumption: **1-of-N honest** — as long as one honest watcher exists in the entire world, fraud cannot succeed.

**Long-term: Cryptographic Validity Proofs**

As BTC bridge research matures (BitVM, ZK validity proofs), the bridge upgrades to fully cryptographic verification. No challenge period — the proof IS the verification. This is the ultimate trust-minimized design.

**Each phase is a strict upgrade** — the bridge fee structure, user experience, and economic model remain identical. Only the trust model under the hood improves.

### Testing

The bridge code is environment-aware, connecting to different Bitcoin networks per Orama environment:

| Orama Environment | Bitcoin Network | BTC Source |
|---|---|---|
| Devnet | Bitcoin Regtest (local) | Mine instantly, unlimited test BTC |
| Testnet | Bitcoin Testnet4 (public) | Free from faucets, zero value |
| Mainnet | Bitcoin Mainnet | Real BTC |

The same bridge code runs across all environments — only the configuration changes.

### Bridge Fee

**Fee: 0.25%** of every bridge transaction (deposit or withdrawal).

| Share | Recipient | Mechanism |
|---|---|---|
| 50% | Validators | Paid directly in BTC, distributed by Effective Power |
| 50% | DeBros Team NFT holders | Auto-swapped to $ORAMA on the native order book, sent to holders' RootWallet |

The NFT holder share creates a perpetual buy engine for $ORAMA: every bridge transaction automatically purchases $ORAMA on the open market, creating constant buy pressure that grows with network usage.

Minimum bridge amount: 0.001 BTC. No maximum.

## 8. Tokenomics & Economic Model

**Token**: $ORAMA
**Total Supply**: **210,000,000** (hard cap forever).

### Zero Pre-mine. Zero Airdrop. 100% Mined.

Every single $ORAMA token is earned by running a node — no exceptions. There is no team allocation, no investor round, no foundation reserve, no airdrop, and no pre-mine. The creators earn tokens the same way as everyone else: by running nodes.

This is the only truly fair model. Nobody starts with an advantage. Nobody dumps on you.

### Block Reward Distribution

Each block reward is split:

- **80%** → directly to the block proposer (the node runner who produced the block, weighted by Effective Power)
- **20%** → into the protocol bonding curve inventory (see Section 9: Native DEX), capped at 21,000,000 $ORAMA total. Once the curve has accumulated 21M tokens, the 20% share redirects to the block proposer (miners receive 100%).

### Emission Schedule

$ORAMA uses a fixed block reward with a Bitcoin-style halving:

| Era | Years | Block Reward | Approx. Annual Emission | Cumulative Supply |
|-----|-------|-------------|------------------------|-------------------|
| 1 | 1–2 | 100 $ORAMA | ~52.5M | ~105M |
| 2 | 3–4 | 50 $ORAMA | ~26.25M | ~157.5M |
| 3 | 5–6 | 25 $ORAMA | ~13.1M | ~183.7M |
| 4 | 7–8 | 12.5 $ORAMA | ~6.6M | ~196.9M |
| 5 | 9–10 | 6.25 $ORAMA | ~3.3M | ~203.5M |
| 6+ | 11+ | Continues halving | Asymptotically approaches 210M | 210M cap |

50% of the total supply is emitted in the first 2 years — rewarding the earliest node runners who take the biggest risk. The halving creates predictable, decreasing issuance that anyone can verify at any block height. When the remaining emittable supply is less than the block reward, the block reward equals the remaining supply — ensuring the 210M cap is never exceeded.

### Transaction Fees

**Smallest unit:** 1 $ORAMA = **1,000,000 rays**. All fees are denominated in rays.

**Genesis fee schedule:**

| Operation | Cost | Layer |
|---|---|---|
| $ORAMA / BTC transfer | 1,000 rays (0.001 $ORAMA) | Global chain |
| DEX order book trade | 1,000 rays | Global chain |
| Namespace state commitment | 2,000 rays | Global chain |
| Private transaction (zk-SNARK) | 4× the public equivalent | Global chain |
| Namespace operations (SQL, KV, IPFS, WASM) | Paid via namespace billing in $ORAMA | Namespace |

**Congestion multiplier:** Fees adjust dynamically based on block fullness (EIP-1559 model). When blocks are at 50% capacity (~500 transactions), the multiplier is 1×. As blocks fill toward the 1,000 transaction limit, the multiplier rises (up to 10×). When blocks are under half full, it drops below 1×. This prevents spam during peak demand and keeps fees low during normal usage.

**Fee distribution:**
- **Base fee** → burned. The more the network is used, the more $ORAMA is permanently removed from supply.
- **Priority fee** → 100% to block proposer (weighted by Effective Power).
- **BTC bridge fee** (0.25%) → 50% to validators, 50% auto-swapped to $ORAMA for DeBros Team NFT holders (see Section 10).

**Governance-adjustable:** The fee schedule is a compute layer parameter, not part of the immutable financial core. Governance can vote to adjust fee amounts to ensure the network remains affordable as $ORAMA appreciates in value. The community has a strong incentive to keep fees low — expensive fees drive users away, hurting the network and the token.

As usage grows and emissions shrink, $ORAMA becomes increasingly deflationary — a self-reinforcing flywheel for centuries.

## 9. Native DEX & Liquidity

Orama does not rely on external exchanges. The chain has its own **protocol-native exchange** built into the global chain as a first-class primitive.

### The Bootstrap Problem

At genesis, supply is near zero. Node runners are earning $ORAMA for the first time. Buyers who bridge BTC onto Orama need a way to purchase $ORAMA. The protocol solves this with two mechanisms that work together:

### Protocol Bonding Curve

The protocol itself acts as the first market maker — not a person, not a DAO, but pure math.

20% of every block reward flows into the bonding curve's sell-side inventory (capped at 21,000,000 $ORAMA total). Anyone can buy $ORAMA from the curve by sending BTC (bridged onto Orama). The price follows a square root function:

$$
\text{Price} = k \times \sqrt{\text{total\_sold\_from\_curve}}
$$

Where `k = 0.0000000006 BTC` — calibrated at genesis so the curve starts cheap (rewarding early risk-takers) and rises aggressively as demand grows.

**Curve price schedule:**

| Tokens Sold from Curve | Price per $ORAMA | Cumulative BTC Spent |
|---|---|---|
| 10,000 | 0.00000006 BTC | 0.0004 BTC |
| 100,000 | 0.00000019 BTC | 0.013 BTC |
| 1,000,000 | 0.0000006 BTC | 0.4 BTC |
| 5,000,000 | 0.00000134 BTC | 4.5 BTC |
| 10,000,000 | 0.0000019 BTC | 12.7 BTC |
| 21,000,000 (max) | 0.00000275 BTC | ~38.5 BTC |

Total BTC to fill the entire curve: **~38.5 BTC**. This BTC flows into the protocol reserve, directly backing the BTC bridge.

**Properties:**
- Always available — the curve always has a price and always has inventory (as long as blocks are being produced and the 21M cap hasn't been reached).
- Cheap early, expensive later — the first tokens cost fractions of a cent, rewarding those who take the earliest risk.
- The curve is a **guaranteed liquidity backstop** — when the order book is thin, buyers can always purchase from the curve. The free market (order book) determines the real price.
- Self-reinforcing: more people buy $ORAMA → more BTC in the protocol reserve → stronger bridge → more confidence → more demand.

**Sunset mechanism:** When the native order book achieves sufficient organic liquidity (average daily volume exceeds a governance-defined threshold for 30 consecutive days), the curve's share of block rewards drops to 0%. All rewards flow directly to node runners. The curve keeps its remaining inventory and stays available, but stops being refilled. The free market takes over entirely.

### Protocol-Native Order Book

The primary trading venue is a **native order book** — a chain primitive, not a smart contract.

Any holder of $ORAMA can place sell orders. Any holder of BTC (bridged onto Orama) can place buy orders. The protocol matches orders when prices cross. No intermediary. No privileged LP class. Pure price discovery. One pair: **$ORAMA/BTC**.

**Standard interface (callable from WASM contracts and external RPC):**
- `place_order(pair, side, amount, price)` — place a limit order
- `market_order(pair, side, amount)` — execute at best available price
- `cancel_order(order_id)` — cancel an open order
- `get_orderbook(pair)` → current bids and asks
- `quote(pair, side, amount)` → expected fill price and size

**Third-party integration:** Any wallet, aggregator, or exchange in the world can integrate by calling these functions over Orama's RPC. No permission required. No listing fees. No gatekeepers.

### Why an Order Book, Not an AMM

| | AMM | Order Book |
|---|---|---|
| Bootstrap | Needs liquidity providers with both assets — chicken-and-egg | Just needs sellers and buyers — works from block 1 |
| Fairness | LPs earn special yield (creates a privileged class) | No special roles — everyone is a trader |
| Capital efficiency | Liquidity spread across entire price curve | Concentrated at actual price levels |
| Philosophy | Complex, opaque | Simple, transparent, free |

### Permissionless WASM DEX Contracts

The protocol-native order book handles the core pair: **$ORAMA/BTC**. For tokens created on Orama via WASM contracts deployed in namespaces, anyone can deploy AMMs or order books as WASM smart contracts. Custom tokens trade against $ORAMA — creating a clear asset hierarchy:

```
BTC (bridged from Bitcoin mainnet)
  ↕ protocol-native order book
$ORAMA (gas token, earned through mining)
  ↕ permissionless WASM DEX contracts
Custom tokens (created on Orama)
```

The protocol defines a standard swap interface that all DEX contracts can implement, enabling aggregation and composability across the ecosystem.

## 10. DeBros NFT Collections

### DeBros Team NFTs (100) — The Founding Collection

**100 DeBros Team NFTs** — the founding collection of the Orama Network. These NFTs were originally minted on Solana and will be migrated to Orama at mainnet launch via snapshot.

**Supply:** 100 (fixed forever — no more will ever be minted).
**Revenue:** 50% of all BTC bridge fees, auto-swapped to $ORAMA and distributed to holders every epoch.
**Governance:** 40% of total voting power (5 votes per NFT). See Section 12.
**Tradeable:** Yes, freely on Orama's native marketplace. Anyone can buy one and receive their share of bridge revenue and governance power.

### DeBros NFTs (700) — The Community Collection

**700 DeBros NFTs** — the broader community collection. Migrated from Solana to Orama at mainnet via snapshot, alongside the Team collection.

**Supply:** 700 (fixed forever).
**Governance:** 35% of total voting power (1 vote per NFT). See Section 12.
**Tradeable:** Yes, freely on Orama's native marketplace.

These NFTs represent the wider community that supported the network in its earliest days. They carry significant governance weight — together with the Team NFTs, NFT holders control 75% of all voting power.

### Migration from Solana

At mainnet launch, a snapshot of all DeBros NFT holders (both collections) on Solana will be taken. Equivalent NFTs are minted natively on Orama and linked to holders' RootWallet addresses. The Solana originals remain as historical artifacts — the Orama versions are the ones connected to revenue and governance.

### Why These Exist

These NFTs honor the community that believed in and built the Orama Network before the blockchain existed. They took the earliest risk. The bridge fee share and governance power are their reward — not a privilege granted in secret, but open, transparent, and tradeable instruments that anyone can participate in by purchasing an NFT on the open market.

### Revenue Flywheel

```
More bridge usage → more BTC fees collected
  → more $ORAMA auto-bought on order book → buy pressure on $ORAMA
  → NFT holders receive more $ORAMA → NFTs become more valuable
  → more attention on Orama → more users → more bridge usage
```

## 11. Token Standards & Scaling

### OTS-1: Orama Token Standard (Fungible Tokens)

Fungible tokens are WASM contracts deployed in namespaces that implement the **OTS-1** interface. Designed from scratch to fix ERC-20's known problems:

- **No unlimited approvals** — the operator model requires an explicit spending limit. No "approve MAX_UINT" footgun that leads to wallet drains.
- **Memo field on transfers** — attach context (invoice ID, payment reason) to any transfer.
- **Explicit revoke** — `revoke_operator()` is a first-class operation, not `approve(addr, 0)`.
- **Global token registry** — tokens register on the global chain so wallets and explorers discover them. Symbol uniqueness is enforced (no impersonation).
- **Queryable** — token balances, transfer history, and holder lists are queryable via the namespace RPC. No external indexer needed.

$ORAMA and BTC are global chain native assets — they are not OTS-1 contracts.

### ONS-1: Orama NFT Standard (Non-Fungible Tokens)

NFTs are WASM contracts deployed in namespaces that implement the **ONS-1** interface. Designed from scratch to fix ERC-721's known problems:

- **On-chain metadata** — stored in the namespace's database and IPFS. No external URLs that can break. Queryable by attribute via SQL.
- **Batch operations** — `batch_transfer` and `batch_mint` are part of the standard. Moving 50 NFTs = 1 transaction.
- **Built-in royalties** — `royalty_info()` is mandatory, not optional. Enforced by marketplaces.
- **Enumerable by default** — `tokens_of(owner)` is part of the standard. List someone's NFTs without an indexer.
- **Two-level operators** — collection-wide approval OR per-token approval.
- **Global NFT registry** — collections register on the global chain for discovery.

**DeBros NFTs** (100 Team + 700 Community) are a special case — they live on the global chain (not in a namespace) because they carry governance power and bridge revenue rights. They are minted once at mainnet genesis from the Solana snapshot.

### Scaling

Orama's namespace architecture provides natural horizontal scaling — each namespace is an isolated execution environment with dedicated resources. Adding nodes to the network allows more namespaces to be provisioned. No sharding, no rollup escape hatches. For extreme-scale use cases, namespaces can optionally implement optimistic or zk-rollup patterns internally, with finality settling on the global chain.

## 12. Governance

Orama governance is fully on-chain — no off-chain snapshots, no forum polls, no "the foundation decided." Every proposal, every vote, and every result is recorded on-chain and verifiable by anyone.

### Voting Power Distribution

Total voting power is split by category, not by individual token math. NFT holders always control 75% of governance — no whale can ever outweigh them.

| Group | Voting Power | Per Unit | Total Units |
|---|---|---|---|
| DeBros Team NFTs (100) | **40%** | 5 votes per NFT | 500 votes within pool |
| DeBros NFTs (700) | **35%** | 1 vote per NFT | 700 votes within pool |
| $ORAMA token holders | **25%** | Quadratic: √(tokens held) | Proportional within pool |

Within each group, voting power is distributed proportionally. A whale who buys all circulating $ORAMA still only controls 25% of total governance — they can never override NFT holders.

### Testnet Governance

During testnet (before NFT migration), governance operates through the DeBros team directly. This is the only period where governance is not fully on-chain. At mainnet launch, when NFTs are migrated and the governance contracts are live, all decision-making transitions to the on-chain system permanently.

### Three Tiers of Decisions

Not all decisions need the same process. Governance is split by urgency and impact:

#### Tier 1: Emergency Actions (24 hours)

*Security patches, active exploit response, critical network fixes.*

**Who decides:** DeBros Team NFT holders only (40% pool).
**Threshold:** 60% of Team NFT votes cast within 24 hours.
**Safeguard:** All Tier 1 actions are logged on-chain and can be reversed by a Tier 2 vote within 7 days. The Team can act fast, but the community can override them.

Emergency is strictly defined: security vulnerabilities, active exploits, and network-critical bugs. The Team cannot use Tier 1 for non-emergency changes.

#### Tier 2: Protocol Upgrades (3 days)

*Fee schedule changes, new primitives, compute layer upgrades, merging significant core changes.*

**Who decides:** All three groups vote.
**Threshold:** 66% approval over a 3-day voting period.

#### Tier 3: Constitutional Changes (14 days)

*Changes to governance structure, bridge fee percentages, bonding curve parameters.*

**Who decides:** All three groups vote.
**Threshold:** 90% approval over a 14-day voting period.

**Truly immutable (no tier can change these):**
- 210 million $ORAMA supply cap
- Emission schedule and halving
- BTC-only economy
- 100% mining distribution (zero pre-mine)
- BTC bridge core security model

### How Voting Works

```
1. Any wallet with voting power submits a proposal (with tier classification)
2. Proposal enters voting period (24h / 3 days / 14 days)
3. Holders vote from their RootWallet
4. Votes are weighted by group allocation (40% / 35% / 25%)
5. If threshold is met, change executes automatically on-chain
6. All votes and results are permanently recorded
```

### Why This Model

Most blockchains have governance captured by whales or controlled by a handful of insiders pretending to be decentralized. Orama's model ensures:

- **No whale capture** — token holders are capped at 25% voting power regardless of holdings.
- **Fast emergency response** — Team NFT holders can act within hours, not days.
- **Community check** — 700 DeBros NFT holders can block the Team (35% vs 40% — Team can't pass Tier 2 alone).
- **Public voice** — token holders have meaningful input but can never overpower the community that built the network.
- **Open participation** — all NFTs are freely tradeable. Anyone can buy voting power on the open market.

## 13. Security Model & Attack Resistance

### Consensus Attacks

- **51% attack**: Requires controlling a majority of Effective Power — which means real uptime, real contribution, and real stake. An attacker can't just buy tokens; they need physical infrastructure and months of contribution history. This makes attacks orders of magnitude more expensive than pure PoS chains.
- **Nothing-at-stake**: Prevented by double-slashing — validators who sign conflicting blocks lose both their stake and their accumulated contribution score. The contribution score takes months to build, making it a meaningful deterrent.
- **Long-range attacks**: HotStuff BFT finalizes every block within 3 rounds (18 seconds). Additionally, epoch-level checkpoints are signed by 2/3+ of Effective Power every hour. Reorganizing beyond a finalized block is impossible.
- **Sybil attacks**: OramaOS attestation is verified via TPM — an attacker can't fake infrastructure multipliers without the real hardware and software.
- **Leader failure**: HotStuff's view-change mechanism handles unresponsive leaders cleanly — if the selected proposer fails to produce a block within the timeout, the next leader takes over without stalling the chain.

### BTC Bridge Security

The bridge security model strengthens across phases (see Section 7):

- **Phase 1 (testnet)**: Validator threshold signatures — staked validators sign bridge operations. Cheating means losing their entire stake.
- **Phase 2 (mainnet)**: Bitcoin SPV light client verifies deposits cryptographically. Withdrawals use optimistic fraud proofs with a challenge period (1-of-N honest assumption).
- **Phase 3 (future)**: Full cryptographic validity proofs — trustless verification with no challenge period.
- **Protocol reserve**: BTC accumulated from bonding curve sales provides additional collateral beyond 1:1 deposits.
- **Bridge halt**: If anomalous withdrawal patterns are detected (e.g., more than 10% of bridged BTC withdrawn in a single epoch), the bridge automatically pauses and requires a Tier 1 governance vote to resume.

### Namespace Security

- **Isolation**: Each namespace runs on a dedicated cluster with its own database, cache, and gateway. A compromised or malicious contract in one namespace cannot affect any other namespace or the global chain.
- **State commitments**: Namespaces commit state roots to the global chain. Invalid state roots result in slashing of the namespace's validator nodes.
- **Staked validators**: Namespace nodes are staked — the economic cost of attacking a namespace is proportional to the stake at risk.
- **Scaling trust**: High-value namespaces can require more validator nodes (e.g., 5 or 10 instead of the default 3), increasing the cost of collusion.

### DEX & Order Book Security

- **Front-running prevention**: Order book transactions within the same block are processed in a randomized order (using the block hash as a deterministic seed), not by gas price. This eliminates MEV (Miner Extractable Value) — block proposers cannot reorder transactions to front-run traders.
- **Price manipulation**: The bonding curve provides a reference price that cannot be manipulated by wash trading on the order book.

### Network Security

- **Encrypted mesh**: All inter-node communication is encrypted via WireGuard VPN tunnel. Internal services (database, cache, gateways) are never exposed on public IPs.
- **OramaOS hardening**: No SSH, read-only rootfs, service sandboxing — the attack surface per node is minimal (see Section 14).
- **Forged attestation**: Nodes submitting fake infrastructure proofs are slashed 50% and permanently flagged.

### Code Security

- All critical protocol components are open-source.
- Formal verification applied to consensus, bridge, and token contract logic where possible.
- Bug bounty program from testnet launch.

## 14. OramaOS & Orama One

### OramaOS — The Hardened Node Operating System

OramaOS is a custom minimal operating system purpose-built for Orama nodes. Running OramaOS provides the **1.5× Infrastructure Multiplier**.

**Security architecture:**
- **No remote shell access** — operators cannot access the filesystem. There is zero attack surface for remote exploitation.
- **Read-only root filesystem** — the OS cannot be tampered with, even by the node operator.
- **Full-disk encryption** with Shamir secret sharing for key distribution across the network.
- **Atomic updates** with automatic rollback if a new version fails. Every update is cryptographically signed and verified before applying.
- **Single root process** (orama-agent) — manages boot, encrypted mesh connectivity, and all service lifecycle. No other process runs as root.
- **Process isolation** — each service is sandboxed and isolated from every other.

**Runs anywhere:** OramaOS can be installed on any modest cloud server or on dedicated hardware. The same image runs on a cloud instance and on Orama One.

Most blockchain nodes run on stock operating systems with full remote access and services running as root. OramaOS is hardened like a hardware wallet operating system — because the node IS a financial system.

### Orama One — The People's Hardware Node

Orama One is a purpose-built, 3D-printed hardware node designed for the Orama Network. It ships pre-loaded with OramaOS.

**Design philosophy:** Anyone should be able to own and run a node. Not a mining rig. Not a server rack. A quiet, low-power device that sits on your desk and earns $ORAMA.

**Key features:**
- Pre-loaded with OramaOS — plug in power, connect to internet, it joins the network automatically.
- Open-source hardware — the enclosure design and bill of materials will be published so anyone can build their own.
- Low power consumption — designed to run continuously without significant energy costs.
- Compact and silent — suitable for home use.

Minimum hardware specifications are published in [Appendix B](APPENDIX_B_HARDWARE_SPECS.md).

### Infrastructure Attestation

The network must verify that a node is genuinely running OramaOS — not faking the multiplier. Attestation uses **TPM-based remote attestation**:

1. The node's TPM chip generates a cryptographic measurement of the boot chain and running software.
2. This measurement is submitted on-chain every epoch.
3. The protocol verifies the measurement against known-good OramaOS signatures.
4. Nodes that fail attestation lose their Infrastructure Multiplier immediately.
5. Nodes that submit forged attestations are slashed 50%.

This is cryptographic proof, not trust — the network doesn't take the node's word for it.

## 15. Genesis, Bootstrap & Launch Mechanics

### 300-Node Genesis Requirement

Orama does not launch mainnet until a minimum of **300 independent nodes** are running and verified. This is a hard requirement — no shortcuts, no exceptions. Most L1 blockchains launch with a handful of validators controlled by insiders. Orama launches with 300 real nodes operated by real people on real hardware, making it one of the most decentralized networks from block one.

### Timeline

- **Testnet**: Existing Orama network nodes upgrade and begin earning $ORAMA block rewards immediately. No staking required. New node operators join during testnet to reach the 300-node threshold. Testnet tokens are real — they carry over to mainnet.
- **Mainnet**: Full production launch with BTC bridge and native DEX live. Staking activated (1,000 $ORAMA minimum). DeBros NFT migration from Solana. On-chain governance begins. Every token in existence was earned through mining — no snapshots, no discretion, no privilege.

## 16. Roadmap & Implementation Plan

| Phase | Milestones |
|---|---|
| **Testnet** | Network launch, no staking required, node runners begin earning $ORAMA, PLONK trusted setup ceremony, bug bounty program |
| **Testnet Expansion** | AI Marketplace beta, Angels framework, compute provider registration, Orama One pre-orders |
| **Testnet Maturity** | 300-node threshold target, DeBros NFT migration preparation, bonding curve live on testnet, native order book testing |
| **Mainnet** | Full production launch, BTC bridge live, native DEX live, staking activated, DeBros NFT bridge revenue begins, on-chain governance live |
| **Post-Launch** | L2 rollup support, AI Marketplace expansion, post-quantum signature upgrade, Orama One general availability |
| **Long-Term** | Governance-driven improvements, bonding curve sunset when organic liquidity is sufficient, financial core remains immutable forever |

## 17. Risks, Mitigations & Eternal Safeguards

| Risk | Severity | Mitigation |
|---|---|---|
| **51% attack** | High | Proof of Infrastructure requires real uptime + contribution, not just stake. TPM attestation prevents fake nodes. |
| **BTC bridge exploit** | Critical | Phased security model: validator threshold signatures (testnet) → Bitcoin light-client + optimistic fraud proofs (mainnet) → cryptographic validity proofs (future). Automatic bridge halt on anomalous withdrawals. Protocol reserve as additional collateral. |
| **Governance capture** | High | NFT holders control 75% of voting power. Quadratic voting for token holders prevents whale dominance. Immutable financial core cannot be changed by any vote. |
| **Quantum computing** | Medium | Post-quantum signature upgrade on roadmap. PLONK proof system can be upgraded to quantum-resistant circuits via universal setup. |
| **Regulatory risk** | Medium | Fully decentralized, no single legal entity. OramaOS nodes have no remote access — even the operator can't be compelled to modify the software. |
| **AI Marketplace abuse** | Medium | Compute nodes capped at 10% of network. Marketplace is purely opt-in. Malicious models can be flagged via governance. |
| **Bonding curve manipulation** | Low | Curve price is mathematical (√n) — cannot be manipulated. Order book has randomized transaction ordering to prevent front-running. |
| **Namespace isolation failure** | Medium | Each namespace runs on a dedicated cluster with separate database, cache, and gateway. State roots are committed to the global chain and verified. Namespace validators are staked — incorrect state roots trigger slashing. |

The protocol is designed to outlive any single person, company, or government.

## 18. Conclusion & Call to Build

Orama Network is not another blockchain experiment. It is the base layer for the next thousand years of human digital life — a true decentralized computer that anyone with modest hardware can help secure, and a financial system as scarce and sovereign as Bitcoin.

We invite every node runner, developer, and user to join at mainnet launch. No tokens to buy beforehand. No presale to miss. Just run a node, earn $ORAMA, and be part of the only blockchain where everyone starts equal. The code is open-source. The rules are set in stone. The power belongs to the people.

**rootwallet.io** will be the official wallet from day one.

Together we build the eternal system.

— DeBros

---

- [Appendix A: Emission Curve & Halving Schedule](APPENDIX_A_EMISSION_CURVE.md)
- [Appendix B: Orama One Hardware Specs](APPENDIX_B_HARDWARE_SPECS.md)
- [Appendix C: Bonding Curve Price Table & BTC Reserve Projections](APPENDIX_C_BONDING_CURVE.md)
- [Appendix D: PLONK Trusted Setup Ceremony Specification](APPENDIX_D_PLONK_SETUP.md)
- [Appendix E: Sample WASM Contract](APPENDIX_E_SAMPLE_CONTRACT.md)
- [Appendix F: Effective Power & Slashing Math](APPENDIX_F_MATH_PROOFS.md)
- [Appendix G: Technical Architecture](APPENDIX_G_TECHNICAL_ARCHITECTURE.md)
