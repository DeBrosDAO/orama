//! Arbitrary bytes through `orama_orchard_verify`, the entry point oramad calls over cgo.
//!
//! Input: 32 bytes of sighash, then the bundle. The safe wrapper catches panics and answers PANIC,
//! so a panic inside the orchard parser shows here as a PANIC code, and that is a finding: bytes
//! from the network must be answered with a verdict, never with a fault.
#![no_main]

use libfuzzer_sys::fuzz_target;
use orama_orchard::{
    orama_orchard_verify, verify, BAD_ARGUMENT, BAD_PROOF_LENGTH, MALFORMED, OK, PANIC, PROOF_REJECTED,
    SIGNATURE_REJECTED,
};

fuzz_target!(|data: &[u8]| {
    // A null pointer or an empty bundle is a bad argument, never a read.
    let zero = [0u8; 32];
    assert_eq!(unsafe { orama_orchard_verify(std::ptr::null(), 0, zero.as_ptr()) }, BAD_ARGUMENT);
    assert_eq!(unsafe { orama_orchard_verify(zero.as_ptr(), 0, zero.as_ptr()) }, BAD_ARGUMENT);
    assert_eq!(unsafe { orama_orchard_verify(zero.as_ptr(), 32, std::ptr::null()) }, BAD_ARGUMENT);

    if data.len() < 33 {
        return;
    }
    let (sighash, bundle) = data.split_at(32);
    let hash: [u8; 32] = sighash.try_into().unwrap();
    let code = unsafe { orama_orchard_verify(bundle.as_ptr(), bundle.len(), sighash.as_ptr()) };
    assert_ne!(code, PANIC, "the verifier panicked on {} bytes of bundle", bundle.len());
    assert!(
        [OK, MALFORMED, BAD_PROOF_LENGTH, PROOF_REJECTED, SIGNATURE_REJECTED].contains(&code),
        "result code {code} is not a verdict"
    );
    // The C entry point and the Rust function agree, and the answer does not depend on a second call.
    assert_eq!(code, verify(bundle, &hash));
});
