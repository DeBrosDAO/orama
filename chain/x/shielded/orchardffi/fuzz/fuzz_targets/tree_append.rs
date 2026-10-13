//! Arbitrary frontier bytes and commitments through `orama_orchard_tree_append`, the note-commitment
//! tree entry point.
//!
//! Input: a u8 that picks how many leading bytes are the frontier (the rest are commitments, 32 bytes
//! each, at most three) and a u8 that picks where the commitments are split. Properties: a verdict
//! (OK or MALFORMED) for any bytes, never a panic or a bad argument; an accepted frontier fits the
//! output capacity; appending in two steps equals appending at once, root included.
#![no_main]

use libfuzzer_sys::fuzz_target;
use orama_orchard::{orama_orchard_tree_append, MALFORMED, OK};

const NODE_LEN: usize = 32;
const CAP: usize = 8 + 32 + 32 * 32;

fn append(frontier: &[u8], commitments: &[u8]) -> (i32, Vec<u8>, [u8; NODE_LEN]) {
    let mut out = vec![0u8; CAP];
    let mut out_len = 0usize;
    let mut root = [0u8; NODE_LEN];
    let code = unsafe {
        orama_orchard_tree_append(
            if frontier.is_empty() { std::ptr::null() } else { frontier.as_ptr() },
            frontier.len(),
            if commitments.is_empty() { std::ptr::null() } else { commitments.as_ptr() },
            commitments.len() / NODE_LEN,
            out.as_mut_ptr(),
            out.len(),
            &mut out_len,
            root.as_mut_ptr(),
        )
    };
    if code == OK {
        assert!(out_len <= CAP, "the frontier is {out_len} bytes");
        out.truncate(out_len);
    }
    (code, out, root)
}

fuzz_target!(|data: &[u8]| {
    if data.len() < 2 {
        return;
    }
    let (cut, split, rest) = (data[0] as usize, data[1] as usize, &data[2..]);
    let frontier_len = (cut * 5).min(rest.len());
    let (frontier, tail) = rest.split_at(frontier_len);
    let n = (tail.len() / NODE_LEN).min(3);
    let commitments = &tail[..n * NODE_LEN];

    let (code, all, root) = append(frontier, commitments);
    assert!(code == OK || code == MALFORMED, "result code {code}");
    if code != OK {
        return;
    }
    let k = if n == 0 { 0 } else { split % (n + 1) };
    let (first_code, first, _) = append(frontier, &commitments[..k * NODE_LEN]);
    assert_eq!(first_code, OK, "a prefix of an accepted append is refused");
    let (second_code, second, second_root) = append(&first, &commitments[k * NODE_LEN..]);
    assert_eq!(second_code, OK);
    assert_eq!(all, second, "two-step append differs from one step");
    assert_eq!(root, second_root, "two-step append has another root");
});
