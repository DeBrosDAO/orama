//! The framing check of the out-of-process verifier against the in-process library's.
//!
//! The two crates share no source: each reads the CompactSize action count and the proof length on
//! its own, before any cryptography. They must refuse the same bytes with the same code, or two
//! nodes with different verifiers would split on a bundle. Properties: this crate's check never
//! panics; when it refuses, the library's full verify refuses with the very same code; and when it
//! accepts, the library does not complain of the proof length.
#![no_main]

use libfuzzer_sys::fuzz_target;
use orama_orchard::{verify as library_verify, BAD_PROOF_LENGTH as LIBRARY_BAD_PROOF_LENGTH};
use orama_orchard_verifier::{check_framing, BAD_PROOF_LENGTH, MALFORMED};

fuzz_target!(|data: &[u8]| {
    let sighash = [7u8; 32];
    match check_framing(data) {
        Err(code) => {
            assert!(code == MALFORMED || code == BAD_PROOF_LENGTH, "framing code {code}");
            assert_eq!(
                i32::from(code),
                library_verify(data, &sighash),
                "the two verifiers refuse the framing of these {} bytes differently",
                data.len()
            );
        }
        Ok(()) => {
            assert_ne!(library_verify(data, &sighash), LIBRARY_BAD_PROOF_LENGTH);
        }
    }
});
