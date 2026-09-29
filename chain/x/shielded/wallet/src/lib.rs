//! Orama shielded wallet core (F7): the key tree, a note tree, and an Ironwood bundle builder.
//!
//! Wallet-side only. This crate proves; nodes only verify (`../orchardffi`). It is not a
//! dependency of `oramad` and is never linked into it.

pub mod bundle;
pub mod keys;
pub mod scenario;
pub mod sighash;
pub mod tree;
pub mod vectors;
