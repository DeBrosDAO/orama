//! Verify-only C ABI over upstream `orchard` for Ironwood bundles.
//!
//! The verifying key is the post-NU6.3 key and nothing else. The insecure pre-NU6.2 circuit is
//! never built here. There is no prover in this crate: proofs are built by wallets.

use std::io::Cursor;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::slice;
use std::sync::OnceLock;

use orchard::bundle::Authorized;
use orchard::circuit::{OrchardCircuitVersion, VerifyingKey};
use orchard::{Bundle, Proof, ValuePool};
use zcash_protocol::consensus::BranchId;
use zcash_protocol::value::ZatBalance;
use zcash_primitives::transaction::components::orchard::read_v6_bundle;

pub const OK: i32 = 0;
pub const MALFORMED: i32 = 1;
pub const BAD_PROOF_LENGTH: i32 = 2;
pub const PROOF_REJECTED: i32 = 3;
pub const SIGNATURE_REJECTED: i32 = 4;
pub const PANIC: i32 = 5;
pub const BAD_ARGUMENT: i32 = 6;

/// Bytes in one serialized action: cv, nf, rk, cmx, epk (32 each), enc (580), out (80).
const ACTION_LEN: usize = 5 * 32 + 580 + 80;
/// flags (1) + value balance (8) + anchor (32), between the actions and the proof.
const BUNDLE_HEADER_LEN: usize = 1 + 8 + 32;

/// The consensus branch whose rules the node verifies under: NU6.3, the Ironwood pool.
const BRANCH: BranchId = BranchId::Nu6_3;
const POOL: ValuePool = ValuePool::Ironwood;

static VERIFYING_KEY: OnceLock<VerifyingKey> = OnceLock::new();

fn verifying_key() -> &'static VerifyingKey {
    VERIFYING_KEY.get_or_init(|| VerifyingKey::build(OrchardCircuitVersion::PostNu6_3))
}

/// Reads a Bitcoin-style CompactSize at `*pos`, canonically encoded only.
fn read_compact_size(data: &[u8], pos: &mut usize) -> Option<u64> {
    let first = *data.get(*pos)?;
    *pos += 1;
    let (width, min) = match first {
        0..=252 => return Some(first as u64),
        253 => (2, 253),
        254 => (4, 0x1_0000),
        255 => (8, 0x1_0000_0000),
    };
    let raw = data.get(*pos..pos.checked_add(width)?)?;
    *pos += width;
    let mut value = 0u64;
    for (i, b) in raw.iter().enumerate() {
        value |= (*b as u64) << (8 * i);
    }
    (value >= min).then_some(value)
}

/// Checks the framing and the proof length before any cryptography runs. The proof must be
/// exactly `Proof::expected_proof_size(n_actions)` bytes.
fn check_framing(data: &[u8]) -> Result<(), i32> {
    let mut pos = 0;
    let n = read_compact_size(data, &mut pos).ok_or(MALFORMED)?;
    let n = usize::try_from(n).map_err(|_| MALFORMED)?;
    if n == 0 {
        return Err(MALFORMED);
    }
    let after_actions = n
        .checked_mul(ACTION_LEN)
        .and_then(|a| a.checked_add(BUNDLE_HEADER_LEN))
        .and_then(|a| a.checked_add(pos))
        .ok_or(MALFORMED)?;
    if after_actions > data.len() {
        return Err(MALFORMED);
    }
    pos = after_actions;
    let proof_len = read_compact_size(data, &mut pos).ok_or(MALFORMED)?;
    if proof_len != Proof::expected_proof_size(n) as u64 {
        return Err(BAD_PROOF_LENGTH);
    }
    Ok(())
}

fn parse(data: &[u8]) -> Result<Bundle<Authorized, ZatBalance>, i32> {
    let mut cursor = Cursor::new(data);
    let bundle = read_v6_bundle(&mut cursor, BRANCH, POOL)
        .map_err(|_| MALFORMED)?
        .ok_or(MALFORMED)?;
    if cursor.position() as usize != data.len() {
        return Err(MALFORMED);
    }
    Ok(bundle)
}

/// Verifies `data` against `sighash`. Returns one of the result codes.
pub fn verify(data: &[u8], sighash: &[u8; 32]) -> i32 {
    if let Err(code) = check_framing(data) {
        return code;
    }
    let bundle = match parse(data) {
        Ok(b) => b,
        Err(code) => return code,
    };
    for action in bundle.actions() {
        if action.rk().verify(sighash, action.authorization()).is_err() {
            return SIGNATURE_REJECTED;
        }
    }
    let binding = bundle.authorization().binding_signature();
    if bundle
        .binding_validating_key()
        .verify(sighash, binding)
        .is_err()
    {
        return SIGNATURE_REJECTED;
    }
    match bundle.verify_proof(verifying_key()) {
        Ok(()) => OK,
        Err(_) => PROOF_REJECTED,
    }
}

/// C entry point. See `include/orama_orchard.h`.
///
/// # Safety
/// `bundle` must point to `bundle_len` readable bytes and `sighash` to 32 readable bytes.
#[no_mangle]
pub unsafe extern "C" fn orama_orchard_verify(
    bundle: *const u8,
    bundle_len: usize,
    sighash: *const u8,
) -> i32 {
    if bundle.is_null() || sighash.is_null() || bundle_len == 0 {
        return BAD_ARGUMENT;
    }
    let data = slice::from_raw_parts(bundle, bundle_len);
    let mut hash = [0u8; 32];
    hash.copy_from_slice(slice::from_raw_parts(sighash, 32));
    catch_unwind(AssertUnwindSafe(|| verify(data, &hash))).unwrap_or(PANIC)
}

/// C entry point. Builds the verifying key so the first bundle does not pay for it.
#[no_mangle]
pub extern "C" fn orama_orchard_warm() -> i32 {
    catch_unwind(|| {
        verifying_key();
        OK
    })
    .unwrap_or(PANIC)
}
