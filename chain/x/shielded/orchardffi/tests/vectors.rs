use std::fs;
use std::path::Path;

use orama_orchard::{
    orama_orchard_verify, verify, BAD_ARGUMENT, BAD_PROOF_LENGTH, MALFORMED, OK, PROOF_REJECTED,
    SIGNATURE_REJECTED,
};

const ACTION_LEN: usize = 820;
const HEADER_LEN: usize = 41;

fn load(name: &str) -> (Vec<u8>, [u8; 32]) {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("testdata");
    let bundle = fs::read(dir.join(format!("{name}.bundle"))).unwrap();
    let hash = fs::read(dir.join(format!("{name}.sighash"))).unwrap();
    (bundle, hash.try_into().unwrap())
}

const VECTORS: [&str; 5] = [
    "ironwood-1-action",
    "ironwood-2-action",
    "ironwood-transfer",
    "ironwood-unshield",
    "ironwood-unshield-bond",
];

fn flipped(bundle: &[u8], at: usize) -> Vec<u8> {
    let mut b = bundle.to_vec();
    b[at] ^= 0x01;
    b
}

#[test]
fn vectors_verify() {
    for name in VECTORS {
        let (bundle, hash) = load(name);
        assert_eq!(verify(&bundle, &hash), OK, "{name}");
    }
}

#[test]
fn flipped_proof_byte_is_rejected() {
    for name in VECTORS {
        let (bundle, hash) = load(name);
        let n = bundle[0] as usize;
        let proof_start = 1 + n * ACTION_LEN + HEADER_LEN + 3; // 3-byte CompactSize length
        assert_eq!(verify(&flipped(&bundle, proof_start + 100), &hash), PROOF_REJECTED, "{name}");
    }
}

#[test]
fn flipped_action_byte_is_rejected() {
    for name in VECTORS {
        let (bundle, hash) = load(name);
        // Inside the cmx of action 0 (a public input of the proof). The epk and ciphertexts are
        // not proof inputs: they are bound by the sighash the caller recomputes from the bytes.
        let code = verify(&flipped(&bundle, 1 + 32 * 3 + 5), &hash);
        assert!(
            code == SIGNATURE_REJECTED || code == PROOF_REJECTED || code == MALFORMED,
            "{name}: {code}"
        );
        assert_ne!(code, OK);
    }
}

#[test]
fn flipped_binding_signature_byte_is_rejected() {
    for name in VECTORS {
        let (bundle, hash) = load(name);
        let last = bundle.len() - 10;
        assert_eq!(verify(&flipped(&bundle, last), &hash), SIGNATURE_REJECTED, "{name}");
    }
}

#[test]
fn flipped_sighash_is_rejected() {
    for name in VECTORS {
        let (bundle, mut hash) = load(name);
        hash[0] ^= 0x01;
        assert_eq!(verify(&bundle, &hash), SIGNATURE_REJECTED, "{name}");
    }
}

#[test]
fn wrong_proof_length_is_refused_before_verification() {
    for name in VECTORS {
        let (bundle, hash) = load(name);
        let n = bundle[0] as usize;
        let len_at = 1 + n * ACTION_LEN + HEADER_LEN;
        // The proof length is a 3-byte CompactSize (0xfd + u16). Claim one byte fewer.
        assert_eq!(bundle[len_at], 0xfd, "{name}");
        let claimed = u16::from_le_bytes([bundle[len_at + 1], bundle[len_at + 2]]);
        let mut short = bundle.clone();
        short[len_at + 1..len_at + 3].copy_from_slice(&(claimed - 1).to_le_bytes());
        assert_eq!(verify(&short, &hash), BAD_PROOF_LENGTH, "{name}");

        // A padded proof: claim and carry one extra byte.
        let mut padded = bundle[..len_at].to_vec();
        padded.extend_from_slice(&[0xfd]);
        padded.extend_from_slice(&(claimed + 1).to_le_bytes());
        padded.extend_from_slice(&bundle[len_at + 3..len_at + 3 + claimed as usize]);
        padded.push(0);
        padded.extend_from_slice(&bundle[len_at + 3 + claimed as usize..]);
        assert_eq!(verify(&padded, &hash), BAD_PROOF_LENGTH, "{name}");
    }
}

#[test]
fn empty_truncated_and_trailing_input_is_refused() {
    let (bundle, hash) = load("ironwood-1-action");
    assert_eq!(verify(&[], &hash), MALFORMED);
    assert_eq!(verify(&[0], &hash), MALFORMED, "zero actions");
    for cut in [1, 10, 500, bundle.len() - 1] {
        let code = verify(&bundle[..cut], &hash);
        assert!(code == MALFORMED || code == BAD_PROOF_LENGTH, "cut {cut}: {code}");
    }
    let mut trailing = bundle.clone();
    trailing.push(0);
    assert_eq!(verify(&trailing, &hash), MALFORMED);
}

#[test]
fn absurd_action_count_is_malformed_without_allocating() {
    let hash = [0u8; 32];
    let mut b = vec![0xff];
    b.extend_from_slice(&u64::MAX.to_le_bytes());
    assert_eq!(verify(&b, &hash), MALFORMED);
}

#[test]
fn c_entry_point_checks_arguments_and_verifies() {
    let (bundle, hash) = load("ironwood-1-action");
    unsafe {
        assert_eq!(orama_orchard_verify(std::ptr::null(), 0, hash.as_ptr()), BAD_ARGUMENT);
        assert_eq!(orama_orchard_verify(bundle.as_ptr(), bundle.len(), std::ptr::null()), BAD_ARGUMENT);
        assert_eq!(orama_orchard_verify(bundle.as_ptr(), bundle.len(), hash.as_ptr()), OK);
    }
}
