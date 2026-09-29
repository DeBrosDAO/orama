//! Verify-only check of one Ironwood bundle, the same canonical checks as the in-process
//! verifier (x/shielded/orchardffi) in the same order: framing and proof length, the
//! spend-authorization signatures, the binding signature, then the Halo 2 proof. Post-NU6.3
//! verifying key only; the insecure pre-NU6.2 circuit is never built.

use std::io::Cursor;
use std::sync::OnceLock;

use orchard::bundle::Authorized;
use orchard::circuit::{OrchardCircuitVersion, VerifyingKey};
use orchard::{Bundle, Proof, ValuePool};
use zcash_primitives::transaction::components::orchard::read_v6_bundle;
use zcash_protocol::consensus::BranchId;
use zcash_protocol::value::ZatBalance;

pub const OK: u8 = 0;
pub const MALFORMED: u8 = 1;
pub const BAD_PROOF_LENGTH: u8 = 2;
pub const PROOF_REJECTED: u8 = 3;
pub const SIGNATURE_REJECTED: u8 = 4;
pub const PANIC: u8 = 5;

/// Bytes in one serialized action: cv, nf, rk, cmx, epk (32 each), enc (580), out (80).
const ACTION_LEN: usize = 5 * 32 + 580 + 80;
/// flags (1) + value balance (8) + anchor (32), between the actions and the proof.
const HEADER_LEN: usize = 1 + 8 + 32;

const BRANCH: BranchId = BranchId::Nu6_3;
const POOL: ValuePool = ValuePool::Ironwood;

static KEY: OnceLock<VerifyingKey> = OnceLock::new();

/// The post-NU6.3 verifying key, built once. Call it at startup so no request pays for it.
pub fn verifying_key() -> &'static VerifyingKey {
    KEY.get_or_init(|| VerifyingKey::build(OrchardCircuitVersion::PostNu6_3))
}

fn compact_size(data: &[u8], pos: &mut usize) -> Option<u64> {
    let first = *data.get(*pos)?;
    *pos += 1;
    let (width, min): (usize, u64) = match first {
        0..=252 => return Some(u64::from(first)),
        253 => (2, 253),
        254 => (4, 0x1_0000),
        255 => (8, 0x1_0000_0000),
    };
    let raw = data.get(*pos..pos.checked_add(width)?)?;
    *pos += width;
    let value = raw.iter().rev().fold(0u64, |acc, b| (acc << 8) | u64::from(*b));
    (value >= min).then_some(value)
}

fn check_framing(data: &[u8]) -> Result<(), u8> {
    let mut pos = 0;
    let n = usize::try_from(compact_size(data, &mut pos).ok_or(MALFORMED)?).map_err(|_| MALFORMED)?;
    if n == 0 {
        return Err(MALFORMED);
    }
    let end = n
        .checked_mul(ACTION_LEN)
        .and_then(|a| a.checked_add(HEADER_LEN))
        .and_then(|a| a.checked_add(pos))
        .ok_or(MALFORMED)?;
    if end > data.len() {
        return Err(MALFORMED);
    }
    pos = end;
    let proof_len = compact_size(data, &mut pos).ok_or(MALFORMED)?;
    if proof_len != Proof::expected_proof_size(n) as u64 {
        return Err(BAD_PROOF_LENGTH);
    }
    Ok(())
}

fn parse(data: &[u8]) -> Result<Bundle<Authorized, ZatBalance>, u8> {
    let mut cursor = Cursor::new(data);
    let bundle = read_v6_bundle(&mut cursor, BRANCH, POOL)
        .map_err(|_| MALFORMED)?
        .ok_or(MALFORMED)?;
    if cursor.position() as usize != data.len() {
        return Err(MALFORMED);
    }
    Ok(bundle)
}

/// Verifies `data` against `sighash` and returns a result code.
pub fn verify(data: &[u8], sighash: &[u8; 32]) -> u8 {
    if let Err(code) = check_framing(data) {
        return code;
    }
    let bundle = match parse(data) {
        Ok(b) => b,
        Err(code) => return code,
    };
    if bundle
        .actions()
        .iter()
        .any(|a| a.rk().verify(sighash, a.authorization()).is_err())
    {
        return SIGNATURE_REJECTED;
    }
    if bundle
        .binding_validating_key()
        .verify(sighash, bundle.authorization().binding_signature())
        .is_err()
    {
        return SIGNATURE_REJECTED;
    }
    match bundle.verify_proof(verifying_key()) {
        Ok(()) => OK,
        Err(_) => PROOF_REJECTED,
    }
}
