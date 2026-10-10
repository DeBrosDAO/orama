//! The Orama sighash. Must equal `orchard.Sighash` in chain/x/shielded/verify/orchard/sighash.go:
//!
//! ```text
//! SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
//! ```
//!
//! An **unshield** also commits to its signer and target, under its own domain, so a bundle copied
//! out of the mempool cannot be redirected to another account:
//!
//! ```text
//! SHA-256( "orama-shielded-ironwood-sighash-bound-v1" || u16be(len(chainID)) || chainID
//!          || u32be(len(binding)) || binding || effectingData )
//! ```
//!
//! The binding is built by [`unshield_binding`] and must equal `MsgUnshield.Binding` in
//! chain/x/shielded/types/msgs.go.
//!
//! `effectingData` is the bundle prefix before the proof: action count, all actions, flags, value
//! balance and anchor. The Go tests recompute the sighash from the committed bundle bytes.

use sha2::{Digest, Sha256};

pub const SIGHASH_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-v1";
pub const SIGHASH_BOUND_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-bound-v1";
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

/// Where an unshield sends its value. The numbers are `UnshieldTarget` in
/// proto/orama/shielded/v1/shielded.proto.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Target<'a> {
    /// The signer's own delegation to a validator (its operator address bytes).
    Bond { validator: &'a [u8] },
    /// The signer's own role bond on a node.
    NodeBond { node_id: &'a str, role: u8 },
    /// The signer's own fee-only balance.
    FeeTopup,
}

/// The binding of an unshield signed by `signer` (address bytes) for `target`:
/// `u8(len(signer)) || signer || u8(target) || u8(len(validator)) || validator ||
/// u16be(len(node_id)) || node_id || u8(role)`.
pub fn unshield_binding(signer: &[u8], target: Target<'_>) -> Vec<u8> {
    let (kind, validator, node_id, role): (u8, &[u8], &str, u8) = match target {
        Target::Bond { validator } => (1, validator, "", 0),
        Target::NodeBond { node_id, role } => (2, &[], node_id, role),
        Target::FeeTopup => (3, &[], "", 0),
    };
    let mut b = vec![signer.len() as u8];
    b.extend_from_slice(signer);
    b.push(kind);
    b.push(validator.len() as u8);
    b.extend_from_slice(validator);
    b.extend_from_slice(&(node_id.len() as u16).to_be_bytes());
    b.extend_from_slice(node_id.as_bytes());
    b.push(role);
    b
}

/// The sighash of a bundle. `binding` is `None` for a transfer or a shield and the unshield
/// binding for an unshield.
pub fn sighash(chain_id: &str, binding: Option<&[u8]>, bundle: &[u8]) -> [u8; 32] {
    assert!(chain_id.len() <= u16::MAX as usize, "chain id too long");
    let mut h = Sha256::new();
    h.update(if binding.is_some() { SIGHASH_BOUND_DOMAIN } else { SIGHASH_DOMAIN });
    h.update((chain_id.len() as u16).to_be_bytes());
    h.update(chain_id.as_bytes());
    if let Some(binding) = binding {
        h.update((binding.len() as u32).to_be_bytes());
        h.update(binding);
    }
    h.update(effecting_data(bundle));
    h.finalize().into()
}
