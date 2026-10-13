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

mod subtree {
    use incrementalmerkletree::{Hashable, Level};
    use orama_orchard::orama_orchard_tree_subtree_root;
    use orama_orchard::tree::{append, empty_root, subtree_root, DEPTH, NODE_LEN, SUBTREE_LEVEL};
    use orchard::tree::MerkleHashOrchard;

    const SUBTREE: u64 = 1 << SUBTREE_LEVEL;

    /// Leaf `i` as a canonical field element: i + 1 in little-endian, far below the modulus.
    fn leaves(from: u64, to: u64) -> Vec<u8> {
        let mut out = Vec::with_capacity(((to - from) as usize) * NODE_LEN);
        for i in from..to {
            let mut cmx = [0u8; NODE_LEN];
            cmx[..8].copy_from_slice(&(i + 1).to_le_bytes());
            out.extend_from_slice(&cmx);
        }
        out
    }

    fn node(bytes: &[u8; NODE_LEN]) -> MerkleHashOrchard {
        Option::from(MerkleHashOrchard::from_bytes(bytes)).unwrap()
    }

    /// Folds the level-16 roots, left to right, up to the depth-32 root, with the empty root as
    /// the right sibling wherever the tree has no leaf.
    fn recombine(roots: &[[u8; NODE_LEN]]) -> [u8; NODE_LEN] {
        let mut level: Vec<MerkleHashOrchard> = roots.iter().map(node).collect();
        for l in SUBTREE_LEVEL..DEPTH {
            let empty = MerkleHashOrchard::empty_root(Level::from(l));
            level = level
                .chunks(2)
                .map(|pair| MerkleHashOrchard::combine(Level::from(l), &pair[0], pair.get(1).unwrap_or(&empty)))
                .collect();
        }
        level[0].to_bytes()
    }

    /// The subtree roots of two filled subtrees recombine to the root the frontier reports for
    /// the same 2 * 2^16 leaves: the shard roots a wallet holds are the tree's own.
    #[test]
    fn subtree_roots_recombine_to_the_anchor() {
        let mut frontier = Vec::new();
        let mut roots = Vec::new();
        let mut anchor = empty_root();
        for k in 0..2u64 {
            let (next, root) = append(&frontier, &leaves(k * SUBTREE, (k + 1) * SUBTREE)).unwrap();
            frontier = next;
            anchor = root;
            roots.push(subtree_root(&frontier).expect("a filled subtree has a root"));
        }
        assert_ne!(roots[0], roots[1]);
        assert_eq!(recombine(&roots), anchor);
    }

    fn hex(bytes: &[u8]) -> String {
        bytes.iter().map(|b| format!("{b:02x}")).collect()
    }

    /// testdata/subtree-roots.txt holds the two subtree roots and the anchor of the tree of leaves
    /// 1 ..= 2 * 2^16 (leaf i is i + 1, little-endian), computed here. The Go tests build the same
    /// tree through the chain (chain/indexer/shielded_chain_cgo_test.go) and must find the same
    /// roots in its state and in the index; this test holds the file to the tree and to the
    /// recombination, so a root the chain served would rebuild the anchor a wallet checks against.
    /// ORAMA_WRITE_GOLDEN=1 rewrites the file.
    #[test]
    fn golden_subtree_roots_are_the_trees_and_recombine_to_its_anchor() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("testdata/subtree-roots.txt");
        let mut frontier = Vec::new();
        let mut anchor = empty_root();
        let mut roots = Vec::new();
        for k in 0..2u64 {
            (frontier, anchor) = append(&frontier, &leaves(k * SUBTREE, (k + 1) * SUBTREE)).unwrap();
            roots.push(subtree_root(&frontier).expect("a filled subtree has a root"));
        }
        assert_eq!(recombine(&roots), anchor);
        assert!(frontier.len() <= orama_orchard::tree::MAX_FRONTIER_LEN);
        let want = format!(
            "leaves {}\nsubtree_root 0 {}\nsubtree_root 1 {}\nanchor {}\n",
            2 * SUBTREE,
            hex(&roots[0]),
            hex(&roots[1]),
            hex(&anchor)
        );
        if std::env::var("ORAMA_WRITE_GOLDEN").is_ok() {
            std::fs::write(&path, &want).unwrap();
        }
        let got = std::fs::read_to_string(&path).expect("testdata/subtree-roots.txt; ORAMA_WRITE_GOLDEN=1 writes it");
        assert_eq!(got, want);
        // The file's own roots recombine to its own anchor, parsed back from the text.
        let value = |prefix: &str| -> [u8; NODE_LEN] {
            let line = got.lines().find(|l| l.starts_with(prefix)).unwrap();
            let hex = line.rsplit(' ').next().unwrap();
            let mut out = [0u8; NODE_LEN];
            for (i, b) in out.iter_mut().enumerate() {
                *b = u8::from_str_radix(&hex[2 * i..2 * i + 2], 16).unwrap();
            }
            out
        };
        assert_eq!(recombine(&[value("subtree_root 0"), value("subtree_root 1")]), value("anchor"));
    }

    /// The frontier of a tree of exactly 2^16 leaves with made-up nodes: position 2^16 - 1, the leaf
    /// and one ommer per set bit. The nodes are not a real tree's; the entry point only needs a
    /// frontier that decodes.
    fn filled_frontier() -> Vec<u8> {
        let mut out = (SUBTREE - 1).to_le_bytes().to_vec();
        out.extend_from_slice(&leaves(0, 1 + SUBTREE_LEVEL as u64));
        out
    }

    /// A frontier that has not just filled a subtree has no subtree root to give, and neither has
    /// an empty one.
    #[test]
    fn a_frontier_that_closes_no_subtree_is_refused() {
        assert!(subtree_root(&[]).is_none());
        for n in [1, 2, 3, 5, 255] {
            let (short, _) = append(&[], &leaves(0, n)).unwrap();
            assert!(subtree_root(&short).is_none(), "{n} leaves close no subtree");
        }
        assert!(subtree_root(&[1, 2, 3]).is_none());
    }

    #[test]
    fn c_entry_point() {
        let full = filled_frontier();
        let want = subtree_root(&full).unwrap();
        let mut root = [0u8; NODE_LEN];
        let code = unsafe { orama_orchard_tree_subtree_root(full.as_ptr(), full.len(), root.as_mut_ptr()) };
        assert_eq!(code, orama_orchard::OK);
        assert_eq!(root, want);
        let code = unsafe { orama_orchard_tree_subtree_root(std::ptr::null(), 0, root.as_mut_ptr()) };
        assert_eq!(code, orama_orchard::BAD_ARGUMENT);
        let (short, _) = append(&[], &leaves(0, 5)).unwrap();
        let code = unsafe { orama_orchard_tree_subtree_root(short.as_ptr(), short.len(), root.as_mut_ptr()) };
        assert_eq!(code, orama_orchard::MALFORMED);
    }
}
