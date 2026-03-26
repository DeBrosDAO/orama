/// Heartbeat management for guardian-to-guardian protocol.
///
/// Timing: 5s heartbeat interval, 15s suspect, 60s dead.
/// Each guardian periodically sends heartbeats to all peers.
/// Peers that stop responding transition: alive → suspect → dead.
const std = @import("std");
const log = @import("../log.zig");
const node_list = @import("../membership/node_list.zig");
const protocol = @import("protocol.zig");

pub const HEARTBEAT_INTERVAL_NS: i128 = 5 * std.time.ns_per_s;
pub const SUSPECT_TIMEOUT_NS: i128 = 15 * std.time.ns_per_s;
pub const DEAD_TIMEOUT_NS: i128 = 60 * std.time.ns_per_s;

/// Check all nodes and update their states based on last_seen time.
pub fn evaluateNodeStates(nodes: *node_list.NodeList) void {
    const now = std.time.nanoTimestamp();

    for (nodes.nodes) |*node| {
        if (nodes.self_index) |si| {
            const idx = (@intFromPtr(node) - @intFromPtr(nodes.nodes.ptr)) / @sizeOf(node_list.Node);
            if (idx == si) continue; // skip self
        }

        if (node.last_seen_ns == 0) continue; // never seen
        const age = now - node.last_seen_ns;

        if (age > DEAD_TIMEOUT_NS) {
            if (node.state != .dead) {
                log.warn("node {s}:{d} marked dead (no heartbeat for >60s)", .{ node.address, node.port });
                node.state = .dead;
            }
        } else if (age > SUSPECT_TIMEOUT_NS) {
            if (node.state != .suspect and node.state != .dead) {
                log.warn("node {s}:{d} suspect (no heartbeat for >15s)", .{ node.address, node.port });
                node.state = .suspect;
            }
        }
    }
}

/// Send a heartbeat to a specific peer.
/// Returns true if the heartbeat was sent successfully.
pub fn sendHeartbeat(
    peer_address: []const u8,
    peer_port: u16,
    self_ip: [4]u8,
    self_port: u16,
    share_count: u32,
) bool {
    const address = std.net.Address.parseIp4(peer_address, peer_port) catch {
        return false;
    };

    const stream = std.net.tcpConnectToAddress(address) catch {
        return false;
    };
    defer stream.close();

    const hb = protocol.Heartbeat{
        .sender_ip = self_ip,
        .sender_port = self_port,
        .share_count = share_count,
        .timestamp = @intCast(@divFloor(std.time.nanoTimestamp(), std.time.ns_per_s)),
    };

    const payload = protocol.encodeHeartbeat(hb);
    const header = protocol.encodeHeader(.{
        .version = protocol.PROTOCOL_VERSION,
        .msg_type = .heartbeat,
        .payload_len = payload.len,
    });

    stream.writeAll(&header) catch return false;
    stream.writeAll(&payload) catch return false;

    return true;
}

/// Count shares in the data directory (V1 + V2).
/// V1: counts directories under shares/
/// V2: counts directories under vaults/ (each identity with at least one secret)
pub fn countShares(data_dir: []const u8) u32 {
    var count: u32 = 0;

    // Count V1 shares
    var path_buf: [4096]u8 = undefined;
    const shares_path = std.fmt.bufPrint(&path_buf, "{s}/shares", .{data_dir}) catch return 0;

    if (std.fs.cwd().openDir(shares_path, .{ .iterate = true })) |d| {
        var dir = d;
        defer dir.close();
        var it = dir.iterate();
        while (it.next() catch null) |entry| {
            if (entry.kind == .directory) count += 1;
        }
    } else |_| {}

    // Count V2 vaults
    var path_buf2: [4096]u8 = undefined;
    const vaults_path = std.fmt.bufPrint(&path_buf2, "{s}/vaults", .{data_dir}) catch return count;

    if (std.fs.cwd().openDir(vaults_path, .{ .iterate = true })) |d| {
        var dir = d;
        defer dir.close();
        var it = dir.iterate();
        while (it.next() catch null) |entry| {
            if (entry.kind == .directory) count += 1;
        }
    } else |_| {}

    return count;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "evaluateNodeStates: marks dead after timeout" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    var nl = try node_list.fromStatic(allocator, &addrs, 7501);
    defer nl.deinit();

    // Set one node as alive but with old timestamp
    nl.nodes[0].state = .alive;
    nl.nodes[0].last_seen_ns = std.time.nanoTimestamp() - DEAD_TIMEOUT_NS - 1;

    // Set other as alive and recent
    nl.nodes[1].state = .alive;
    nl.nodes[1].last_seen_ns = std.time.nanoTimestamp();

    evaluateNodeStates(&nl);

    try std.testing.expectEqual(node_list.NodeState.dead, nl.nodes[0].state);
    try std.testing.expectEqual(node_list.NodeState.alive, nl.nodes[1].state);
}

test "evaluateNodeStates: marks suspect" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{"10.0.0.1"};
    var nl = try node_list.fromStatic(allocator, &addrs, 7501);
    defer nl.deinit();

    nl.nodes[0].state = .alive;
    nl.nodes[0].last_seen_ns = std.time.nanoTimestamp() - SUSPECT_TIMEOUT_NS - 1;

    evaluateNodeStates(&nl);

    try std.testing.expectEqual(node_list.NodeState.suspect, nl.nodes[0].state);
}

test "evaluateNodeStates: skips self" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    var nl = try node_list.fromStatic(allocator, &addrs, 7501);
    defer nl.deinit();

    nl.self_index = 0;
    nl.nodes[0].state = .alive;
    nl.nodes[0].last_seen_ns = std.time.nanoTimestamp() - DEAD_TIMEOUT_NS - 1;

    evaluateNodeStates(&nl);

    // Self should NOT be marked dead
    try std.testing.expectEqual(node_list.NodeState.alive, nl.nodes[0].state);
}

test "countShares: returns 0 for nonexistent dir" {
    try std.testing.expectEqual(@as(u32, 0), countShares("/tmp/nonexistent-vault-dir-12345"));
}
