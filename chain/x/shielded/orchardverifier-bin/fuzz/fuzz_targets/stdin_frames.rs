//! Arbitrary bytes on the verifier process's stdin.
//!
//! The node writes length-prefixed requests; a bug in either side, or a hostile bundle, is read
//! here. Properties: the loop never panics; whatever it wrote is whole 13-byte response frames,
//! the ready frame first, one frame per request that was fully read, each carrying the request's
//! id and a verdict code; and no verdict is PANIC (a fault inside the orchard parser on bytes the
//! node passed on).
#![no_main]

use libfuzzer_sys::fuzz_target;
use orama_orchard_verifier::protocol::{serve, READY_ID};
use orama_orchard_verifier::{BAD_PROOF_LENGTH, MALFORMED, OK, PANIC, PROOF_REJECTED, SIGNATURE_REJECTED};

const RESPONSE_FRAME: usize = 4 + 9;

fuzz_target!(|data: &[u8]| {
    let mut output = Vec::new();
    let _ = serve(data, &mut output);

    assert_eq!(output.len() % RESPONSE_FRAME, 0, "a partial response frame was written");
    let mut frames = output.chunks(RESPONSE_FRAME);
    let ready = frames.next().expect("the ready frame is always written");
    assert_eq!(&ready[..4], &9u32.to_be_bytes());
    assert_eq!(u64::from_be_bytes(ready[4..12].try_into().unwrap()), READY_ID);
    assert_eq!(ready[12], OK);

    // The ids the requests carried, in order, for the frames that were read whole.
    let mut ids = Vec::new();
    let mut rest = data;
    while rest.len() >= 4 {
        let len = u32::from_be_bytes(rest[..4].try_into().unwrap()) as usize;
        if rest.len() < 4 + len || len > orama_orchard_verifier::protocol::MAX_FRAME || len < 8 {
            break;
        }
        ids.push(u64::from_be_bytes(rest[4..12].try_into().unwrap()));
        rest = &rest[4 + len..];
    }
    let answered: Vec<&[u8]> = frames.collect();
    assert_eq!(answered.len(), ids.len(), "one response per request that was read whole");
    for (frame, id) in answered.iter().zip(ids) {
        assert_eq!(&frame[..4], &9u32.to_be_bytes());
        assert_eq!(u64::from_be_bytes(frame[4..12].try_into().unwrap()), id, "the id is echoed");
        let code = frame[12];
        assert_ne!(code, PANIC, "the verifier panicked on a request");
        assert!([OK, MALFORMED, BAD_PROOF_LENGTH, PROOF_REJECTED, SIGNATURE_REJECTED].contains(&code));
    }
});
