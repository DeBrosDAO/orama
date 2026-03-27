# Appendix E: Sample WASM Contract

## Example: Private BTC Transfer Contract (Rust)

This example shows a WASM smart contract written in Rust that accepts BTC deposits and allows private withdrawals using Orama's privacy toggle.

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

## Example: SQL Database Query from WASM

```rust
use orama_sdk::prelude::*;
use orama_sdk::sql::Database;

#[orama_contract]
impl MyApp {
    /// Store user data in Orama's distributed SQL database
    pub fn create_user(&mut self, ctx: &Context, name: String, email: String) -> Result<u64> {
        let db = Database::connect("myapp")?;

        let user_id = db.execute(
            "INSERT INTO users (name, email, created_at) VALUES (?, ?, ?)",
            &[&name, &email, &ctx.block_timestamp().to_string()],
        )?;

        Ok(user_id)
    }

    /// Query users from the distributed SQL database
    #[view]
    pub fn get_user(&self, user_id: u64) -> Result<User> {
        let db = Database::connect("myapp")?;

        let row = db.query_one(
            "SELECT id, name, email FROM users WHERE id = ?",
            &[&user_id.to_string()],
        )?;

        Ok(User {
            id: row.get("id")?,
            name: row.get("name")?,
            email: row.get("email")?,
        })
    }
}
```

## Compiling and Deploying

```bash
# Install the Orama SDK
cargo install orama-cli

# Create a new contract project
orama new my-contract
cd my-contract

# Build to WASM
orama build --release

# Deploy to Orama network (costs gas in $ORAMA)
orama deploy --network mainnet ./target/wasm/my_contract.wasm

# Call a contract function
orama call <contract-address> deposit --value 0.01btc
orama call <contract-address> withdraw --private --to <address> --amount 0.005btc
```

## Supported Languages

While these examples are in Rust, any language that compiles to WebAssembly can be used:

- **Rust** (recommended — best tooling and performance)
- **Go** (via TinyGo)
- **TypeScript** (via AssemblyScript)
- **C/C++** (via Emscripten)
- **Python** (experimental)

The Orama SDK provides bindings for all supported languages, giving access to the full set of on-chain primitives (SQL, KV store, IPFS, BTC bridge, DEX, AI Marketplace). As new languages gain WebAssembly compilation support, they become available for Orama contract development automatically.
