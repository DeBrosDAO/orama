//! The Orama sighash. Must equal `orchard.Sighash` in chain/x/shielded/verify/orchard/sighash.go:
//!
//! ```text
//! SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
//! ```
//!
//! `effectingData` is the bundle prefix before the proof: action count, all actions, flags, value
//! balance and anchor. The Go tests recompute the sighash from the committed bundle bytes.

use sha2::{Digest, Sha256};

pub const SIGHASH_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-v1";
/// cv, nf, rk, cmx, epk (32 each), enc ciphertext (580), out ciphertext (80).
pub const ACTION_LEN: usize = 5 * 32 + 580 + 80;
/// flags (1) + value balance (8) + anchor (32).
pub const HEADER_LEN: usize = 1 + 8 + 32;

/// The signed prefix of a canonical v6 bundle. Bundles built here have fewer than 253 actions, so
/// the action count is a one-byte CompactSize.
pub fn effecting_data(bundle: &[u8]) -> &[u8] {
    let n = bundle[0] as usize;
    assert!(n > 0 && n < 253, "single-byte action count");
    &bundle[..1 + n * ACTION_LEN + HEADER_LEN]
}

pub fn sighash(chain_id: &str, bundle: &[u8]) -> [u8; 32] {
    assert!(chain_id.len() <= u16::MAX as usize, "chain id too long");
    let mut h = Sha256::new();
    h.update(SIGHASH_DOMAIN);
    h.update((chain_id.len() as u16).to_be_bytes());
    h.update(chain_id.as_bytes());
    h.update(effecting_data(bundle));
    h.finalize().into()
}
