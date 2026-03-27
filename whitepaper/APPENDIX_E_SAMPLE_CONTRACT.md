# Appendix E: Sample WASM Contracts

## Example: Private BTC Transfer Contract (Rust)

This example shows a WASM smart contract written in Rust that accepts BTC deposits and allows private withdrawals using Orama's privacy toggle. This contract deploys into a namespace.

```rust
use orama_sdk::prelude::*;

/// A simple vault contract that accepts BTC deposits (public)
/// and allows private withdrawals to any address.
#[orama_contract]
pub struct PrivateVault {
    /// Maps depositor address to their BTC balance (in satoshis)
    balances: StorageMap<Address, u64>,
}

#[orama_contract]
impl PrivateVault {
    /// Initialize the contract
    #[init]
    pub fn new() -> Self {
        Self {
            balances: StorageMap::new("balances"),
        }
    }

    /// Deposit BTC into the vault (public transaction)
    #[payable(BTC)]
    pub fn deposit(&mut self, ctx: &Context) -> Result<()> {
        let sender = ctx.caller();
        let amount = ctx.btc_value(); // Amount of BTC sent with this call

        let current = self.balances.get(&sender).unwrap_or(0);
        self.balances.set(&sender, current + amount);

        emit!(Deposit {
            from: sender,
            amount: amount,
        });

        Ok(())
    }

    /// Withdraw BTC privately — sender, receiver, and amount are shielded
    #[private] // This annotation enables the zk-SNARK privacy toggle
    pub fn withdraw(&mut self, ctx: &Context, to: Address, amount: u64) -> Result<()> {
        let sender = ctx.caller();
        let balance = self.balances.get(&sender).ok_or(Error::InsufficientBalance)?;

        if balance < amount {
            return Err(Error::InsufficientBalance);
        }

        self.balances.set(&sender, balance - amount);

        // Transfer BTC to recipient — this transfer is private
        ctx.transfer_btc(to, amount)?;

        Ok(())
    }

    /// Check your own balance (public, read-only)
    #[view]
    pub fn balance_of(&self, address: Address) -> u64 {
        self.balances.get(&address).unwrap_or(0)
    }
}
```

## How It Works

1. **Deposit (public):** User calls `deposit()` and sends BTC. The deposit is visible on-chain — everyone can see who deposited and how much.

2. **Withdraw (private):** User calls `withdraw()` with the `#[private]` annotation. The Orama runtime automatically generates a PLONK zk-SNARK proof that:
   - The caller has sufficient balance
   - The withdrawal amount is valid
   - The recipient address is valid

   But the proof reveals **none** of these details to observers. The transaction appears on-chain but the sender, recipient, and amount are shielded.

3. **Gas:** The private withdrawal costs 4x the gas of a public withdrawal (covers ZK proof generation).

## Example: AI Angel Contract (Rust)

This example shows an Angel (AI agent) that monitors BTC bridge activity and automatically places buy orders.

```rust
use orama_sdk::prelude::*;
use orama_sdk::dex::OrderBook;

/// An Angel that watches bridge deposits and places DEX buy orders
#[orama_contract]
pub struct BridgeWatcherAngel {
    /// The Angel's own $ORAMA balance for placing orders
    wallet: TokenBalance,
    /// Minimum bridge deposit size to trigger a buy (in satoshis)
    min_trigger: u64,
    /// Percentage of bridge amount to buy in $ORAMA
    buy_percentage: u8,
}

#[orama_contract]
impl BridgeWatcherAngel {
    #[init]
    pub fn new(min_trigger: u64, buy_percentage: u8) -> Self {
        Self {
            wallet: TokenBalance::new(),
            min_trigger,
            buy_percentage,
        }
    }

    /// Called automatically by the Angel runtime when a bridge deposit event occurs
    #[on_event(BridgeDeposit)]
    pub fn on_bridge_deposit(&mut self, ctx: &Context, event: BridgeDepositEvent) -> Result<()> {
        if event.amount < self.min_trigger {
            return Ok(()); // Ignore small deposits
        }

        let buy_amount = (event.amount as u128 * self.buy_percentage as u128 / 100) as u64;

        // Place a market buy order on the native DEX
        let order = OrderBook::market_order(
            Pair::ORAMA_BTC,
            Side::Buy,
            buy_amount,
        )?;

        emit!(AngelAction {
            action: "buy_triggered",
            trigger_amount: event.amount,
            order_id: order.id,
        });

        Ok(())
    }

    /// Owner can deposit $ORAMA for the Angel to use
    #[payable(ORAMA)]
    pub fn fund(&mut self, ctx: &Context) -> Result<()> {
        self.wallet.add(ctx.orama_value());
        Ok(())
    }

    /// Owner can withdraw unused funds
    pub fn withdraw_funds(&mut self, ctx: &Context, amount: u64) -> Result<()> {
        ctx.require_owner()?;
        self.wallet.subtract(amount)?;
        ctx.transfer_orama(ctx.caller(), amount)?;
        Ok(())
    }
}
```

## Example: Full-Stack App with SQL Database (Rust)

This example demonstrates Orama's unique namespace model — contracts have access to a real SQL database (RQLite), distributed cache (Olric), and IPFS storage. No external indexing infrastructure needed.

```rust
use orama_sdk::prelude::*;
use orama_sdk::namespace::{Database, Cache, Storage};

/// A full-stack marketplace contract deployed into a namespace.
/// The namespace provides dedicated SQL, cache, and storage.
#[orama_contract]
pub struct Marketplace {
    db: Database,
    cache: Cache,
    storage: Storage,
}

#[orama_contract]
impl Marketplace {
    #[init]
    pub fn new(ctx: &Context) -> Result<Self> {
        let db = Database::connect()?;

        // Create tables — real SQL, powered by the namespace's RQLite cluster
        db.execute("
            CREATE TABLE IF NOT EXISTS listings (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                seller TEXT NOT NULL,
                title TEXT NOT NULL,
                description TEXT,
                price_rays INTEGER NOT NULL,
                image_cid TEXT,
                status TEXT DEFAULT 'active',
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
        ", &[])?;

        db.execute("
            CREATE TABLE IF NOT EXISTS purchases (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                listing_id INTEGER NOT NULL,
                buyer TEXT NOT NULL,
                price_rays INTEGER NOT NULL,
                purchased_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                FOREIGN KEY (listing_id) REFERENCES listings(id)
            )
        ", &[])?;

        Ok(Self {
            db: Database::connect()?,
            cache: Cache::connect()?,
            storage: Storage::connect()?,
        })
    }

    /// List an item for sale — stores metadata in SQL, image in IPFS
    pub fn create_listing(
        &mut self,
        ctx: &Context,
        title: String,
        description: String,
        price_rays: u64,
        image_bytes: Vec<u8>,
    ) -> Result<u64> {
        let seller = ctx.caller();

        // Store image in the namespace's IPFS storage
        let image_cid = self.storage.put(&image_bytes)?;

        // Insert listing into the namespace's SQL database
        let listing_id = self.db.execute(
            "INSERT INTO listings (seller, title, description, price_rays, image_cid) VALUES (?, ?, ?, ?, ?)",
            &[&seller.to_string(), &title, &description, &price_rays.to_string(), &image_cid],
        )?;

        // Invalidate the cache so the next query gets fresh data
        self.cache.delete("listings:active")?;

        emit!(ListingCreated {
            id: listing_id,
            seller: seller,
            title: title,
            price_rays: price_rays,
        });

        Ok(listing_id)
    }

    /// Buy a listed item
    #[payable(ORAMA)]
    pub fn purchase(&mut self, ctx: &Context, listing_id: u64) -> Result<()> {
        let buyer = ctx.caller();
        let payment = ctx.orama_value();

        // Query the listing from SQL
        let row = self.db.query_one(
            "SELECT seller, price_rays, status FROM listings WHERE id = ?",
            &[&listing_id.to_string()],
        )?;

        let seller: Address = row.get("seller")?;
        let price: u64 = row.get("price_rays")?;
        let status: String = row.get("status")?;

        if status != "active" {
            return Err(Error::Custom("Listing is not active".into()));
        }
        if payment < price {
            return Err(Error::InsufficientPayment);
        }

        // Transfer payment to seller
        ctx.transfer_orama(seller, price)?;

        // Update listing status in SQL
        self.db.execute(
            "UPDATE listings SET status = 'sold' WHERE id = ?",
            &[&listing_id.to_string()],
        )?;

        // Record the purchase
        self.db.execute(
            "INSERT INTO purchases (listing_id, buyer, price_rays) VALUES (?, ?, ?)",
            &[&listing_id.to_string(), &buyer.to_string(), &price.to_string()],
        )?;

        Ok(())
    }

    /// Get active listings — uses cache for performance
    /// This data is also queryable via the namespace's RPC:
    ///   GET /v1/contracts/{address}/listings?status=active&sort=price_rays&order=asc
    #[view]
    pub fn get_active_listings(&self, limit: u32, offset: u32) -> Result<Vec<Listing>> {
        // Check cache first
        let cache_key = format!("listings:active:{}:{}", limit, offset);
        if let Some(cached) = self.cache.get(&cache_key)? {
            return Ok(cached);
        }

        // Cache miss — query SQL
        let rows = self.db.query(
            "SELECT id, seller, title, price_rays, image_cid, created_at
             FROM listings WHERE status = 'active'
             ORDER BY created_at DESC LIMIT ? OFFSET ?",
            &[&limit.to_string(), &offset.to_string()],
        )?;

        let listings: Vec<Listing> = rows.iter().map(|r| Listing {
            id: r.get("id").unwrap(),
            seller: r.get("seller").unwrap(),
            title: r.get("title").unwrap(),
            price_rays: r.get("price_rays").unwrap(),
            image_cid: r.get("image_cid").unwrap(),
        }).collect();

        // Cache for 60 seconds
        self.cache.set(&cache_key, &listings, 60)?;

        Ok(listings)
    }
}
```

### What This Contract Gets (From the Namespace)

This contract deploys into a namespace and automatically receives:

| Primitive | What It Does | Ethereum Equivalent |
|---|---|---|
| `Database::connect()` | Real SQL database (RQLite with Raft consensus across 3+ nodes) | Nothing — need The Graph or custom indexer |
| `Cache::connect()` | Distributed cache (Olric with consistent hashing) | Nothing — need Redis or Memcached externally |
| `Storage::connect()` | IPFS storage (content-addressed, replicated) | Need Pinata/Infura IPFS externally |
| Namespace RPC | Rich query API with filtering, sorting, pagination | Need The Graph subgraph |

**The namespace IS the backend.** No external infrastructure needed.

## Compiling and Deploying

```bash
# Install the Orama SDK
cargo install orama-cli

# Create a new contract project
orama new my-contract
cd my-contract

# Build to WASM
orama build --release

# Deploy to a namespace on the Orama network
# This creates a namespace (or uses an existing one) with dedicated
# RQLite, Olric, IPFS, and Gateway infrastructure
orama deploy --namespace my-marketplace --network mainnet ./target/wasm/my_contract.wasm

# Call a contract function
orama call my-marketplace::Marketplace create_listing --title "Cool NFT" --price 1000000
orama call my-marketplace::Marketplace purchase --listing-id 1 --value 1000000rays

# Query via the namespace's RPC (no indexer needed)
curl https://my-marketplace.orama.network/v1/contracts/Marketplace/listings?status=active&sort=price_rays
```

## Supported Languages

While these examples are in Rust, any language that compiles to WebAssembly can be used:

- **Rust** (recommended — best tooling and performance)
- **Go** (via TinyGo)
- **TypeScript** (via AssemblyScript)
- **C/C++** (via Emscripten)
- **Python** (experimental)

The Orama SDK provides bindings for all supported languages, giving access to the full set of namespace primitives (SQL, KV cache, IPFS, BTC bridge, DEX, AI Marketplace). As new languages gain WebAssembly compilation support, they become available for Orama contract development automatically.
