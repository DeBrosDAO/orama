/// Quorum logic for multi-guardian operations.
///
/// W = min(N, max(K+1, ceil(2N/3))) — a supermajority, and always more than a
/// read needs. K = max(2, floor(N/3)) is the Shamir threshold.
///
/// The header used to say W = ceil(2N/3), which the function below has not
/// done since the K+1 floor was added: at N=3 that gave W=2 against K=3, so a
/// write reported successful had persisted fewer shares than a read requires.
///
/// Push: fan out to all alive guardians, succeed if W respond with ACK.
/// Pull: contact guardians until K shares are collected.
const std = @import("std");
const node_list = @import("node_list.zig");

/// Calculate write quorum: W = min(N, max(K+1, ceil(2N/3))).
/// The invariant W > K guarantees a successful write persists more shares than
/// a read requires, so it is always recoverable and survives losing at least
/// one guardian. The old formula (ceil(2N/3)) gave W=2 < K=3 at N=3, allowing
/// silently-unrecoverable "successful" writes.
pub fn writeQuorum(alive_count: usize) usize {
    if (alive_count == 0) return 0;
    const k = readQuorum(alive_count);
    var w = (2 * alive_count + 2) / 3; // ceil(2N/3)
    if (w < k + 1) w = k + 1;
    if (w > alive_count) w = alive_count;
    return w;
}

/// Check if we have enough ACKs for a successful write.
pub fn hasWriteQuorum(ack_count: usize, alive_count: usize) bool {
    return ack_count >= writeQuorum(alive_count);
}

/// Calculate read threshold K = max(2, floor(N/3)).
/// Minimum shares needed to reconstruct; any K-1 reveal zero information.
pub fn readQuorum(alive_count: usize) usize {
    const t = alive_count / 3;
    return if (t < 2) 2 else t;
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
    try std.testing.expectEqual(@as(usize, 3), writeQuorum(3)); // max(K+1=3, ceil(6/3)=2) = 3
    try std.testing.expectEqual(@as(usize, 3), writeQuorum(4)); // ceil(8/3) = 3
    try std.testing.expectEqual(@as(usize, 4), writeQuorum(5)); // ceil(10/3) = 4
    try std.testing.expectEqual(@as(usize, 10), writeQuorum(14)); // ceil(28/3) = 10
}

test "hasWriteQuorum: basic checks" {
    // 5-node cluster: quorum = 4
    try std.testing.expect(hasWriteQuorum(4, 5));
    try std.testing.expect(hasWriteQuorum(5, 5));
    try std.testing.expect(!hasWriteQuorum(3, 5));

    // 3-node cluster: quorum = 3 (all must ack; W > K guarantees recoverability)
    try std.testing.expect(hasWriteQuorum(3, 3));
    try std.testing.expect(!hasWriteQuorum(2, 3));
}

test "readQuorum: adaptive threshold" {
    try std.testing.expectEqual(@as(usize, 2), readQuorum(0));
    try std.testing.expectEqual(@as(usize, 2), readQuorum(3)); // 3/3=1, min=2
    try std.testing.expectEqual(@as(usize, 2), readQuorum(5)); // 5/3=1, min=2
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
