/// Proactive repair protocol for guardian share refresh.
///
/// When the cluster detects that shares should be refreshed (periodic timer,
/// guardian join/leave, or manual trigger), the repair protocol coordinates
/// the Herzberg et al. re-sharing across all active guardians.
///
/// Flow:
/// 1. Leader initiates repair round (broadcasts REPAIR_OFFER)
/// 2. Each guardian generates re-sharing deltas and sends to peers
/// 3. Each guardian applies received deltas to update their share
/// 4. Guardians exchange new Merkle commitments to verify consistency
const std = @import("std");
const log = @import("../log.zig");
const node_list = @import("../membership/node_list.zig");
const protocol = @import("protocol.zig");

/// Status of a repair round.
pub const RepairStatus = enum {
    idle,
    initiated,
    deltas_sent,
    deltas_received,
    applied,
    verified,
    failed,
};

/// A repair round tracks the state of one re-sharing operation.
pub const RepairRound = struct {
    /// Unique round ID (timestamp-based)
    round_id: u64,
    /// Current status
    status: RepairStatus,
    /// Number of guardians participating
    participant_count: usize,
    /// Number of delta sets received
    deltas_received: usize,
    /// Number of verifications completed
    verifications_done: usize,
    /// Whether this round succeeded
    success: bool,
    /// Timestamp when round started
    started_ns: i128,

    pub fn init() RepairRound {
        return RepairRound{
            .round_id = @intCast(@as(u64, @truncate(@as(u128, @bitCast(std.time.nanoTimestamp()))))),
            .status = .idle,
            .participant_count = 0,
            .deltas_received = 0,
            .verifications_done = 0,
            .success = false,
            .started_ns = std.time.nanoTimestamp(),
        };
    }

    /// Check if the round has timed out (60 seconds).
    pub fn isTimedOut(self: *const RepairRound) bool {
        const elapsed_ns = std.time.nanoTimestamp() - self.started_ns;
        return elapsed_ns > 60 * std.time.ns_per_s;
    }

    /// Check if enough deltas have been received to apply.
    pub fn canApply(self: *const RepairRound) bool {
        return self.deltas_received >= self.participant_count;
    }
};

/// Determines if a repair round should be triggered.
///
/// Conditions:
/// - Periodic timer (every 24 hours)
/// - Guardian join/leave detected
/// - Manual trigger via admin API
pub fn shouldRepair(
    alive_count: usize,
    last_repair_ns: i128,
    node_change_detected: bool,
) bool {
    // Always repair on node topology change
    if (node_change_detected) return true;

    // Periodic repair: every 24 hours
    const elapsed_ns = std.time.nanoTimestamp() - last_repair_ns;
    const twenty_four_hours_ns: i128 = 24 * 60 * 60 * std.time.ns_per_s;
    if (elapsed_ns > twenty_four_hours_ns) return true;

    // Don't repair if too few guardians (need at least 3)
    if (alive_count < 3) return false;

    return false;
}

/// Determines the safety threshold — the minimum number of alive guardians
/// needed before we should consider repair unnecessary.
/// If alive_count drops below this, repair becomes critical.
pub fn safetyThreshold(total_count: usize) usize {
    // Safety threshold = K + 1 (one more than read quorum)
    const k = if (total_count / 3 < 3) @as(usize, 3) else total_count / 3;
    return k + 1;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "repair: round init" {
    const round = RepairRound.init();
    try std.testing.expectEqual(RepairStatus.idle, round.status);
    try std.testing.expect(!round.success);
    try std.testing.expectEqual(@as(usize, 0), round.deltas_received);
}

test "repair: round timeout check" {
    var round = RepairRound.init();
    // Fresh round should not be timed out
    try std.testing.expect(!round.isTimedOut());

    // Fake an old start time (61 seconds ago)
    round.started_ns = std.time.nanoTimestamp() - (61 * std.time.ns_per_s);
    try std.testing.expect(round.isTimedOut());
}

test "repair: canApply when enough deltas" {
    var round = RepairRound.init();
    round.participant_count = 5;
    round.deltas_received = 3;
    try std.testing.expect(!round.canApply());

    round.deltas_received = 5;
    try std.testing.expect(round.canApply());
}

test "repair: shouldRepair on node change" {
    try std.testing.expect(shouldRepair(5, std.time.nanoTimestamp(), true));
}

test "repair: shouldRepair periodic" {
    // Last repair was 25 hours ago
    const old_time = std.time.nanoTimestamp() - (25 * 60 * 60 * std.time.ns_per_s);
    try std.testing.expect(shouldRepair(5, old_time, false));
}

test "repair: shouldRepair recent" {
    // Last repair was 1 hour ago, no node changes
    const recent_time = std.time.nanoTimestamp() - (1 * 60 * 60 * std.time.ns_per_s);
    try std.testing.expect(!shouldRepair(5, recent_time, false));
}

test "repair: safetyThreshold" {
    try std.testing.expectEqual(@as(usize, 4), safetyThreshold(5));  // K=3, safety=4
    try std.testing.expectEqual(@as(usize, 4), safetyThreshold(9));  // K=3, safety=4
    try std.testing.expectEqual(@as(usize, 5), safetyThreshold(14)); // K=4, safety=5
    try std.testing.expectEqual(@as(usize, 34), safetyThreshold(100)); // K=33, safety=34
}
