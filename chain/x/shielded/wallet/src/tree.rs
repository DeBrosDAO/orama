//! An in-memory Orchard note commitment tree (depth 32) for the dev builder and tests. It keeps
//! every leaf and recomputes roots and paths on demand, which is fine for the handful of notes a
//! vector scenario holds. A real wallet keeps witnesses incrementally.

use incrementalmerkletree::{Hashable, Level};
use orchard::note::ExtractedNoteCommitment;
use orchard::tree::{Anchor, MerkleHashOrchard, MerklePath};
use orchard::NOTE_COMMITMENT_TREE_DEPTH;

#[derive(Default, Clone)]
pub struct NoteTree {
    leaves: Vec<MerkleHashOrchard>,
}

impl NoteTree {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn len(&self) -> usize {
        self.leaves.len()
    }

    pub fn is_empty(&self) -> bool {
        self.leaves.is_empty()
    }

    /// Appends a note commitment and returns its position.
    pub fn append(&mut self, cmx: &ExtractedNoteCommitment) -> u32 {
        self.leaves.push(MerkleHashOrchard::from_cmx(cmx));
        u32::try_from(self.leaves.len() - 1).expect("tree position fits u32")
    }

    fn node(&self, level: u8, index: u64) -> MerkleHashOrchard {
        if (index << level) >= self.leaves.len() as u64 {
            return MerkleHashOrchard::empty_root(Level::from(level));
        }
        if level == 0 {
            return self.leaves[index as usize];
        }
        let left = self.node(level - 1, 2 * index);
        let right = self.node(level - 1, 2 * index + 1);
        MerkleHashOrchard::combine(Level::from(level - 1), &left, &right)
    }

    pub fn root(&self) -> Anchor {
        Anchor::from(self.node(NOTE_COMMITMENT_TREE_DEPTH as u8, 0))
    }

    /// The authentication path of the leaf at `position`, against the current root.
    pub fn witness(&self, position: u32) -> Option<MerklePath> {
        if position as usize >= self.leaves.len() {
            return None;
        }
        let mut path = [MerkleHashOrchard::empty_root(Level::from(0)); NOTE_COMMITMENT_TREE_DEPTH];
        for (level, slot) in path.iter_mut().enumerate() {
            let sibling = (u64::from(position) >> level) ^ 1;
            *slot = self.node(level as u8, sibling);
        }
        Some(MerklePath::from_parts(position, path))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cmx(i: u8) -> ExtractedNoteCommitment {
        let mut b = [0u8; 32];
        b[0] = i + 10; // the empty leaf is the field element 2, so stay clear of it
        Option::from(ExtractedNoteCommitment::from_bytes(&b)).expect("small integers are field elements")
    }

    #[test]
    fn empty_tree_root_is_the_empty_anchor() {
        assert_eq!(NoteTree::new().root(), Anchor::empty_tree());
        assert!(NoteTree::new().witness(0).is_none());
    }

    #[test]
    fn every_witness_roots_to_the_current_root() {
        let mut tree = NoteTree::new();
        for i in 0..9u8 {
            tree.append(&cmx(i));
            let root = tree.root();
            for p in 0..=i {
                let path = tree.witness(u32::from(p)).expect("leaf present");
                assert_eq!(path.root(cmx(p)), root, "leaf {p} of {}", i + 1);
            }
        }
    }

    #[test]
    fn root_changes_with_each_append_and_witness_rejects_out_of_range() {
        let mut tree = NoteTree::new();
        let mut roots = vec![tree.root().to_bytes()];
        for i in 0..4u8 {
            tree.append(&cmx(i));
            roots.push(tree.root().to_bytes());
        }
        roots.sort();
        roots.dedup();
        assert_eq!(roots.len(), 5);
        assert!(tree.witness(4).is_none());
    }
}
