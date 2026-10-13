//! The two `unsafe extern "C"` entry points, called with arguments that are refused before any
//! cryptography runs: null pointers, zero lengths, a length product that overflows, bytes that are
//! not a field element or a frontier. Nothing here hashes a Merkle level or checks a proof, so the
//! whole file runs in seconds under Miri (`make -C chain rust-miri`), which checks every raw-pointer
//! read and write the entry points make. The proof and tree vectors are in vectors.rs and tree.rs
//! and are far too slow to interpret.

use orama_orchard::tree::{MAX_FRONTIER_LEN, NODE_LEN};
use orama_orchard::{
    orama_orchard_tree_append, orama_orchard_verify, BAD_ARGUMENT, MALFORMED,
};

const HASH: [u8; 32] = [7; 32];

#[test]
fn verify_refuses_null_pointers_and_empty_bundles() {
    let bundle = [0u8; 8];
    unsafe {
        assert_eq!(orama_orchard_verify(std::ptr::null(), 8, HASH.as_ptr()), BAD_ARGUMENT);
        assert_eq!(orama_orchard_verify(bundle.as_ptr(), 8, std::ptr::null()), BAD_ARGUMENT);
        assert_eq!(orama_orchard_verify(bundle.as_ptr(), 0, HASH.as_ptr()), BAD_ARGUMENT);
    }
}

#[test]
fn verify_reads_exactly_the_bytes_it_is_given() {
    unsafe {
        // One byte, zero actions: parsed, found malformed, never reaches the proof.
        assert_eq!(orama_orchard_verify([0u8].as_ptr(), 1, HASH.as_ptr()), MALFORMED);
        // A CompactSize that claims 2^64 - 1 actions in a nine-byte buffer.
        let mut huge = vec![0xffu8];
        huge.extend_from_slice(&u64::MAX.to_le_bytes());
        assert_eq!(orama_orchard_verify(huge.as_ptr(), huge.len(), HASH.as_ptr()), MALFORMED);
    }
}

#[test]
fn tree_append_refuses_null_and_overflowing_arguments() {
    let mut out = [0u8; MAX_FRONTIER_LEN];
    let mut out_len = 0usize;
    let mut root = [0u8; NODE_LEN];
    let cmx = [1u8; NODE_LEN];
    unsafe {
        // A frontier length with no frontier behind it.
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 1, cmx.as_ptr(), 1,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
        // A commitment count with no commitments behind it.
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, std::ptr::null(), 1,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
        // Every output pointer is required.
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, cmx.as_ptr(), 1,
                std::ptr::null_mut(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, cmx.as_ptr(), 1,
                out.as_mut_ptr(), out.len(), std::ptr::null_mut(), root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, cmx.as_ptr(), 1,
                out.as_mut_ptr(), out.len(), &mut out_len, std::ptr::null_mut(),
            ),
            BAD_ARGUMENT
        );
        // n_commitments * 32 overflows usize: refused before a slice is built from it.
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, cmx.as_ptr(), usize::MAX,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
    }
}

#[test]
fn tree_append_refuses_bytes_that_are_not_a_frontier_or_a_field_element() {
    let mut out = [0u8; MAX_FRONTIER_LEN];
    let mut out_len = 0usize;
    let mut root = [0u8; NODE_LEN];
    unsafe {
        // A frontier of three bytes is neither empty nor a position and a leaf.
        let frontier = [1u8, 2, 3];
        assert_eq!(
            orama_orchard_tree_append(
                frontier.as_ptr(), frontier.len(), std::ptr::null(), 0,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            MALFORMED
        );
        // All 0xff is above the field modulus.
        let cmx = [0xffu8; NODE_LEN];
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, cmx.as_ptr(), 1,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            MALFORMED
        );
        // A commitment count of one with the commitments pointer null is a bad argument, not a read.
        assert_eq!(
            orama_orchard_tree_append(
                std::ptr::null(), 0, std::ptr::null(), 1,
                out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
            ),
            BAD_ARGUMENT
        );
    }
}
