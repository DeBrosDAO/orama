/// Write quorum logic for multi-guardian operations.
///
/// W = ceil(2/3 * alive_nodes) — requires supermajority for writes.
/// R = 1 (any single guardian can serve a read).
///
/// Push: fan out to all alive guardians, succeed if W respond with ACK.
/// Pull: contact guardians until K shares are collected.
const std = @import("std");
const node_list = @import("node_list.zig");

/// Calculate write quorum: ceil(2/3 * N).
pub fn writeQuorum(alive_count: usize) usize {
    if (alive_count == 0) return 0;
    if (alive_count <= 2) return alive_count; // require all for tiny clusters
    // ceil(2*N/3) = (2*N + 2) / 3 using integer arithmetic
    return (2 * alive_count + 2) / 3;
}

/// Check if we have enough ACKs for a successful write.
pub fn hasWriteQuorum(ack_count: usize, alive_count: usize) bool {
    return ack_count >= writeQuorum(alive_count);
}

/// Calculate read quorum: how many shares needed to reconstruct.
/// This is the Shamir threshold K = max(3, floor(N/3)).
pub fn readQuorum(alive_count: usize) usize {
    if (alive_count == 0) return 3;
    const t = alive_count / 3;
    return if (t < 3) 3 else t;
}

/// Result of a multi-guardian push operation.
pub const PushResult = struct {
    /// Number of guardians that accepted the share
    ack_count: usize,
    /// Number of guardians that were contacted
    total_contacted: usize,
    /// Number of guardians that failed
    fail_count: usize,
    /// Whether write quorum was achieved
    quorum_met: bool,

    pub fn isSuccess(self: PushResult) bool {
        return self.quorum_met;
    }
};

/// Result of a multi-guardian pull operation.
pub const PullResult = struct {
    /// Number of shares collected
    share_count: usize,
    /// Number of guardians contacted
    total_contacted: usize,
    /// Whether enough shares were collected for reconstruction
    threshold_met: bool,

    pub fn isSuccess(self: PullResult) bool {
        return self.threshold_met;
    }
};

// ── Tests ────────────────────────────────────────────────────────────────────

test "writeQuorum: various cluster sizes" {
    try std.testing.expectEqual(@as(usize, 0), writeQuorum(0));
    try std.testing.expectEqual(@as(usize, 1), writeQuorum(1));
    try std.testing.expectEqual(@as(usize, 2), writeQuorum(2));
    try std.testing.expectEqual(@as(usize, 2), writeQuorum(3)); // ceil(6/3) = 2
    try std.testing.expectEqual(@as(usize, 3), writeQuorum(4)); // ceil(8/3) = 3
    try std.testing.expectEqual(@as(usize, 4), writeQuorum(5)); // ceil(10/3) = 4
    try std.testing.expectEqual(@as(usize, 10), writeQuorum(14)); // ceil(28/3) = 10
}

test "hasWriteQuorum: basic checks" {
    // 5-node cluster: quorum = 4
    try std.testing.expect(hasWriteQuorum(4, 5));
    try std.testing.expect(hasWriteQuorum(5, 5));
    try std.testing.expect(!hasWriteQuorum(3, 5));

    // 3-node cluster: quorum = 2
    try std.testing.expect(hasWriteQuorum(2, 3));
    try std.testing.expect(!hasWriteQuorum(1, 3));
}

test "readQuorum: adaptive threshold" {
    try std.testing.expectEqual(@as(usize, 3), readQuorum(0));
    try std.testing.expectEqual(@as(usize, 3), readQuorum(3));
    try std.testing.expectEqual(@as(usize, 3), readQuorum(5)); // 5/3=1, min=3
    try std.testing.expectEqual(@as(usize, 3), readQuorum(9)); // 9/3=3
    try std.testing.expectEqual(@as(usize, 4), readQuorum(14)); // 14/3=4
    try std.testing.expectEqual(@as(usize, 33), readQuorum(100)); // 100/3=33
}

test "PushResult: success when quorum met" {
    const result = PushResult{
        .ack_count = 4,
        .total_contacted = 5,
        .fail_count = 1,
        .quorum_met = true,
    };
    try std.testing.expect(result.isSuccess());
}

test "PushResult: failure when quorum not met" {
    const result = PushResult{
        .ack_count = 2,
        .total_contacted = 5,
        .fail_count = 3,
        .quorum_met = false,
    };
    try std.testing.expect(!result.isSuccess());
}
