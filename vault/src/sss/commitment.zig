/// Merkle commitment tree over Shamir shares.
///
/// Each share is hashed (SHA-256) to form a leaf. Leaves are paired and hashed
/// up to a single root. Guardians cross-check roots to detect tampering.
///
/// Tree is padded to the next power of 2 with zero-hashes.
const std = @import("std");
const Sha256 = std.crypto.hash.sha2.Sha256;
const types = @import("types.zig");

/// Builds a Merkle commitment tree from shares.
/// Returns a CommitmentTree with the root hash and all leaf hashes.
pub fn buildTree(
    allocator: std.mem.Allocator,
    shares: []const types.Share,
) !types.CommitmentTree {
    if (shares.len == 0) return error.EmptyShares;

    // Compute leaf hashes: H(x || y)
    const leaves = try allocator.alloc([32]u8, shares.len);
    errdefer allocator.free(leaves);

    for (shares, 0..) |share, i| {
        var h = Sha256.init(.{});
        h.update(&[_]u8{share.x});
        h.update(share.y);
        leaves[i] = h.finalResult();
    }

    // Compute root
    const root = try computeRoot(allocator, leaves);

    return types.CommitmentTree{
        .root = root,
        .leaves = leaves,
    };
}

/// Generates a Merkle proof for a specific share index.
pub fn generateProof(
    allocator: std.mem.Allocator,
    shares: []const types.Share,
    index: usize,
) !types.MerkleProof {
    if (index >= shares.len) return error.IndexOutOfBounds;

    const tree = try buildTree(allocator, shares);
    defer tree.deinit(allocator);

    // Compute proof path
    const depth = ceilLog2(shares.len);
    var siblings = try allocator.alloc([32]u8, depth);
    errdefer allocator.free(siblings);
    var directions = try allocator.alloc(bool, depth);
    errdefer allocator.free(directions);

    // Pad leaves to next power of 2
    const padded_len = nextPow2(shares.len);
    var current_level = try allocator.alloc([32]u8, padded_len);
    defer allocator.free(current_level);

    // Copy leaf hashes, pad with zeros
    for (0..padded_len) |i| {
        if (i < tree.leaves.len) {
            current_level[i] = tree.leaves[i];
        } else {
            current_level[i] = [_]u8{0} ** 32;
        }
    }

    var current_index = index;
    var level_len = padded_len;
    var proof_idx: usize = 0;

    while (level_len > 1) {
        const sibling_index = current_index ^ 1; // flip last bit
        siblings[proof_idx] = current_level[sibling_index];
        directions[proof_idx] = (current_index & 1) == 1; // true if we're on the right

        // Compute next level
        const next_len = level_len / 2;
        for (0..next_len) |i| {
            current_level[i] = hashPair(current_level[i * 2], current_level[i * 2 + 1]);
        }

        current_index /= 2;
        level_len = next_len;
        proof_idx += 1;
    }

    return types.MerkleProof{
        .leaf_index = index,
        .siblings = siblings[0..proof_idx],
        .directions = directions[0..proof_idx],
    };
}

/// Verifies a Merkle proof against an expected root.
pub fn verifyProof(
    share: types.Share,
    proof: types.MerkleProof,
    expected_root: [32]u8,
) bool {
    // Compute leaf hash
    var h = Sha256.init(.{});
    h.update(&[_]u8{share.x});
    h.update(share.y);
    var current = h.finalResult();

    // Walk up the tree
    for (0..proof.siblings.len) |i| {
        if (proof.directions[i]) {
            // We're on the right, sibling is on the left
            current = hashPair(proof.siblings[i], current);
        } else {
            // We're on the left, sibling is on the right
            current = hashPair(current, proof.siblings[i]);
        }
    }

    return std.mem.eql(u8, &current, &expected_root);
}

// ── Internal helpers ─────────────────────────────────────────────────────────

fn hashPair(left: [32]u8, right: [32]u8) [32]u8 {
    var h = Sha256.init(.{});
    h.update(&left);
    h.update(&right);
    return h.finalResult();
}

fn computeRoot(allocator: std.mem.Allocator, leaves: []const [32]u8) ![32]u8 {
    const padded_len = nextPow2(leaves.len);
    var current = try allocator.alloc([32]u8, padded_len);
    defer allocator.free(current);

    for (0..padded_len) |i| {
        if (i < leaves.len) {
            current[i] = leaves[i];
        } else {
            current[i] = [_]u8{0} ** 32;
        }
    }

    var len = padded_len;
    while (len > 1) {
        const next_len = len / 2;
        for (0..next_len) |i| {
            current[i] = hashPair(current[i * 2], current[i * 2 + 1]);
        }
        len = next_len;
    }

    return current[0];
}

fn nextPow2(n: usize) usize {
    if (n <= 1) return 1;
    var v = n - 1;
    v |= v >> 1;
    v |= v >> 2;
    v |= v >> 4;
    v |= v >> 8;
    v |= v >> 16;
    v |= v >> 32;
    return v + 1;
}

fn ceilLog2(n: usize) usize {
    if (n <= 1) return 0;
    var v = n - 1;
    var result: usize = 0;
    while (v > 0) {
        v >>= 1;
        result += 1;
    }
    return result;
}

// ── Tests ────────────────────────────────────────────────────────────────────

const split_mod = @import("split.zig");

test "commitment: build tree from shares" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    const tree = try buildTree(allocator, share_set.shares);
    defer tree.deinit(allocator);

    // Root should be 32 bytes (SHA-256)
    try std.testing.expectEqual(@as(usize, 32), tree.root.len);
    // Should have one leaf per share
    try std.testing.expectEqual(@as(usize, 5), tree.leaves.len);
}

test "commitment: same shares produce same root" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 3, 2);
    defer share_set.deinit(allocator);

    const tree1 = try buildTree(allocator, share_set.shares);
    defer tree1.deinit(allocator);
    const tree2 = try buildTree(allocator, share_set.shares);
    defer tree2.deinit(allocator);

    try std.testing.expectEqualSlices(u8, &tree1.root, &tree2.root);
}

test "commitment: proof generation and verification" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 1, 2, 3, 4, 5 };
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    const tree = try buildTree(allocator, share_set.shares);
    defer tree.deinit(allocator);

    // Generate and verify proof for each share
    for (share_set.shares, 0..) |share, i| {
        const proof = try generateProof(allocator, share_set.shares, i);
        defer proof.deinit(allocator);

        try std.testing.expect(verifyProof(share, proof, tree.root));
    }
}

test "commitment: tampered share fails verification" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 3, 2);
    defer share_set.deinit(allocator);

    const tree = try buildTree(allocator, share_set.shares);
    defer tree.deinit(allocator);

    const proof = try generateProof(allocator, share_set.shares, 0);
    defer proof.deinit(allocator);

    // Tamper with the share
    const tampered = types.Share{
        .x = share_set.shares[0].x,
        .y = &[_]u8{0xFF}, // wrong data
    };

    try std.testing.expect(!verifyProof(tampered, proof, tree.root));
}

test "commitment: wrong root fails verification" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 3, 2);
    defer share_set.deinit(allocator);

    const proof = try generateProof(allocator, share_set.shares, 0);
    defer proof.deinit(allocator);

    const wrong_root = [_]u8{0xAB} ** 32;
    try std.testing.expect(!verifyProof(share_set.shares[0], proof, wrong_root));
}

test "commitment: single share tree" {
    const allocator = std.testing.allocator;
    // Can't split into 1 share (need K>=2, N>=K), so manually build tree
    const share = types.Share{ .x = 1, .y = &[_]u8{42} };
    const shares = [_]types.Share{share};

    const tree = try buildTree(allocator, &shares);
    defer tree.deinit(allocator);

    try std.testing.expectEqual(@as(usize, 1), tree.leaves.len);
}
