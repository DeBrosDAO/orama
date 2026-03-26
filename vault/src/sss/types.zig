/// Types for Shamir Secret Sharing.
const std = @import("std");

/// A single share from Shamir's Secret Sharing.
/// x is the evaluation point (1..N), y is the evaluated polynomial values (one per secret byte).
pub const Share = struct {
    /// Evaluation point (1..255, never 0)
    x: u8,
    /// Share data (same length as original secret)
    y: []const u8,

    pub fn deinit(self: Share, allocator: std.mem.Allocator) void {
        // Zero before freeing
        const mutable: []u8 = @constCast(self.y);
        @memset(mutable, 0);
        allocator.free(mutable);
    }
};

/// A set of shares with metadata.
pub const ShareSet = struct {
    /// Threshold (K) — minimum shares needed to reconstruct
    threshold: u8,
    /// Total shares (N)
    total: u8,
    /// The shares themselves
    shares: []Share,

    pub fn deinit(self: ShareSet, allocator: std.mem.Allocator) void {
        for (self.shares) |share| {
            share.deinit(allocator);
        }
        allocator.free(self.shares);
    }
};

/// Merkle commitment for share integrity verification.
pub const CommitmentTree = struct {
    /// Root hash of the Merkle tree (SHA-256, 32 bytes)
    root: [32]u8,
    /// Individual leaf hashes (one per share)
    leaves: []const [32]u8,

    pub fn deinit(self: CommitmentTree, allocator: std.mem.Allocator) void {
        allocator.free(self.leaves);
    }
};

/// Merkle proof for a single share.
pub const MerkleProof = struct {
    /// Index of the leaf in the tree
    leaf_index: usize,
    /// Sibling hashes along the path to the root
    siblings: []const [32]u8,
    /// Direction flags: false = sibling is on the left, true = sibling is on the right
    directions: []const bool,

    pub fn deinit(self: MerkleProof, allocator: std.mem.Allocator) void {
        allocator.free(self.siblings);
        allocator.free(self.directions);
    }
};
