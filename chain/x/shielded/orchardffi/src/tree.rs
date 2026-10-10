//! The Orchard note-commitment tree frontier, as a stateless function over bytes.
//!
//! The chain stores the frontier (the right edge of the depth-32 Sinsemilla tree) and never the
//! whole tree. Sinsemilla hashing exists only in Rust, so appending commitments and reading the
//! root happen here; Go keeps the bytes.
//!
//! Frontier encoding (all little-endian): an empty tree is zero bytes. Otherwise
//! `position (u64) || leaf (32) || ommers (32 each)`; the ommer count is fixed by the position
//! (one per set bit), and a length that disagrees is refused.

use incrementalmerkletree::frontier::Frontier;
use incrementalmerkletree::{Hashable, Level, Position};
use orchard::tree::MerkleHashOrchard;

/// The depth of the Orchard note-commitment tree.
pub const DEPTH: u8 = orchard::NOTE_COMMITMENT_TREE_DEPTH as u8;

/// Bytes in a commitment, a tree node and a root.
pub const NODE_LEN: usize = 32;
/// Largest encoded frontier: position, leaf and one ommer per level.
pub const MAX_FRONTIER_LEN: usize = 8 + NODE_LEN + NODE_LEN * DEPTH as usize;

type Tree = Frontier<MerkleHashOrchard, DEPTH>;

fn node(bytes: &[u8]) -> Option<MerkleHashOrchard> {
    let raw: [u8; NODE_LEN] = bytes.try_into().ok()?;
    Option::from(MerkleHashOrchard::from_bytes(&raw))
}

/// Decodes a frontier, refusing anything that is not exactly what `encode` writes.
pub fn decode(data: &[u8]) -> Option<Tree> {
    if data.is_empty() {
        return Some(Tree::empty());
    }
    if data.len() < 8 + NODE_LEN {
        return None;
    }
    let position = u64::from_le_bytes(data[..8].try_into().ok()?);
    let leaf = node(&data[8..8 + NODE_LEN])?;
    let rest = &data[8 + NODE_LEN..];
    if rest.len() % NODE_LEN != 0 {
        return None;
    }
    let ommers = rest
        .chunks_exact(NODE_LEN)
        .map(node)
        .collect::<Option<Vec<_>>>()?;
    Tree::from_parts(Position::from(position), leaf, ommers).ok()
}

/// Encodes a frontier the way `decode` reads it.
pub fn encode(tree: &Tree) -> Vec<u8> {
    let Some(frontier) = tree.value() else {
        return Vec::new();
    };
    let mut out = Vec::with_capacity(MAX_FRONTIER_LEN);
    out.extend_from_slice(&u64::from(frontier.position()).to_le_bytes());
    out.extend_from_slice(&frontier.leaf().to_bytes());
    for ommer in frontier.ommers() {
        out.extend_from_slice(&ommer.to_bytes());
    }
    out
}

/// The root of a tree with no commitments.
pub fn empty_root() -> [u8; NODE_LEN] {
    MerkleHashOrchard::empty_root(Level::from(DEPTH)).to_bytes()
}

/// Appends each commitment in order. Returns the new frontier and its root, or `None` when the
/// frontier does not decode, a commitment is not a canonical field element, or the tree is full.
pub fn append(frontier: &[u8], commitments: &[u8]) -> Option<(Vec<u8>, [u8; NODE_LEN])> {
    if commitments.len() % NODE_LEN != 0 {
        return None;
    }
    let mut tree = decode(frontier)?;
    for cmx in commitments.chunks_exact(NODE_LEN) {
        if !tree.append(node(cmx)?) {
            return None;
        }
    }
    Some((encode(&tree), tree.root().to_bytes()))
}
