use std::fs;
use std::path::Path;

use orama_orchard::tree::{append, decode, empty_root, MAX_FRONTIER_LEN, NODE_LEN};
use orama_orchard::{orama_orchard_tree_append, BAD_ARGUMENT, MALFORMED, OK};

const ACTION_LEN: usize = 820;

fn vector(name: &str) -> Vec<u8> {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("testdata");
    fs::read(dir.join(format!("{name}.bundle"))).unwrap()
}

/// The anchor of a shielding bundle built on an empty tree is the empty root.
#[test]
fn empty_root_is_the_vector_anchor() {
    let bundle = vector("ironwood-1-action");
    let anchor_at = 1 + ACTION_LEN + 1 + 8;
    assert_eq!(&bundle[anchor_at..anchor_at + NODE_LEN], &empty_root());
}

#[test]
fn appending_nothing_to_an_empty_tree_gives_the_empty_root() {
    let (frontier, root) = append(&[], &[]).unwrap();
    assert!(frontier.is_empty());
    assert_eq!(root, empty_root());
}

fn cmx(bundle: &[u8], action: usize) -> &[u8] {
    let at = 1 + action * ACTION_LEN + 96;
    &bundle[at..at + NODE_LEN]
}

#[test]
fn append_is_incremental() {
    let bundle = vector("ironwood-2-action");
    let both = [cmx(&bundle, 0), cmx(&bundle, 1)].concat();
    let (all_at_once, root_once) = append(&[], &both).unwrap();
    let (one, _) = append(&[], cmx(&bundle, 0)).unwrap();
    let (two, root_two) = append(&one, cmx(&bundle, 1)).unwrap();
    assert_eq!(all_at_once, two);
    assert_eq!(root_once, root_two);
    assert_ne!(root_once, empty_root());
    assert!(all_at_once.len() <= MAX_FRONTIER_LEN);
    assert!(decode(&all_at_once).is_some());
}

#[test]
fn bad_input_is_refused() {
    let bundle = vector("ironwood-1-action");
    let (frontier, _) = append(&[], cmx(&bundle, 0)).unwrap();
    // A commitment that is not a canonical field element (all 0xff is above the modulus).
    assert!(append(&[], &[0xff; NODE_LEN]).is_none());
    // A commitment of the wrong length.
    assert!(append(&[], &[1; NODE_LEN - 1]).is_none());
    // A frontier with a byte missing, or one ommer too many.
    assert!(decode(&frontier[..frontier.len() - 1]).is_none());
    let mut long = frontier.clone();
    long.extend_from_slice(&[0; NODE_LEN]);
    assert!(decode(&long).is_none());
    assert!(decode(&[1, 2, 3]).is_none());
}

#[test]
fn c_entry_point() {
    let bundle = vector("ironwood-1-action");
    let mut out = [0u8; MAX_FRONTIER_LEN];
    let mut out_len = 0usize;
    let mut root = [0u8; NODE_LEN];
    let code = unsafe {
        orama_orchard_tree_append(
            std::ptr::null(),
            0,
            cmx(&bundle, 0).as_ptr(),
            1,
            out.as_mut_ptr(),
            out.len(),
            &mut out_len,
            root.as_mut_ptr(),
        )
    };
    assert_eq!(code, OK);
    let (want, want_root) = append(&[], cmx(&bundle, 0)).unwrap();
    assert_eq!(&out[..out_len], &want[..]);
    assert_eq!(root, want_root);

    let short = unsafe {
        orama_orchard_tree_append(
            std::ptr::null(), 0, cmx(&bundle, 0).as_ptr(), 1,
            out.as_mut_ptr(), 4, &mut out_len, root.as_mut_ptr(),
        )
    };
    assert_eq!(short, BAD_ARGUMENT);
    let bad = unsafe {
        orama_orchard_tree_append(
            std::ptr::null(), 0, [0xffu8; NODE_LEN].as_ptr(), 1,
            out.as_mut_ptr(), out.len(), &mut out_len, root.as_mut_ptr(),
        )
    };
    assert_eq!(bad, MALFORMED);
}
