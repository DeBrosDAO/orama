/// Node join/leave detection.
///
/// Compares the current node list with a refreshed one to detect:
/// - New nodes (join) → mark as unknown, will be alive after first heartbeat
/// - Missing nodes (leave) → mark as dead after timeout (handled by heartbeat)
///
/// New nodes receive shares on the next client sync push (no active handoff).
const std = @import("std");
const log = @import("../log.zig");
const node_list = @import("node_list.zig");

pub const DiscoveryEvent = struct {
    address: []const u8,
    port: u16,
    event_type: EventType,
};

pub const EventType = enum {
    joined,
    departed,
};

/// Compare two node lists and return events for joins and departures.
pub fn detectChanges(
    allocator: std.mem.Allocator,
    old: *const node_list.NodeList,
    new: *const node_list.NodeList,
) ![]DiscoveryEvent {
    var events = std.ArrayListUnmanaged(DiscoveryEvent){};
    errdefer events.deinit(allocator);

    // Find new nodes (in new but not in old)
    for (new.nodes) |new_node| {
        var found = false;
        for (old.nodes) |old_node| {
            if (std.mem.eql(u8, new_node.address, old_node.address) and new_node.port == old_node.port) {
                found = true;
                break;
            }
        }
        if (!found) {
            try events.append(allocator, DiscoveryEvent{
                .address = new_node.address,
                .port = new_node.port,
                .event_type = .joined,
            });
        }
    }

    // Find departed nodes (in old but not in new)
    for (old.nodes) |old_node| {
        var found = false;
        for (new.nodes) |new_node| {
            if (std.mem.eql(u8, old_node.address, new_node.address) and old_node.port == new_node.port) {
                found = true;
                break;
            }
        }
        if (!found) {
            try events.append(allocator, DiscoveryEvent{
                .address = old_node.address,
                .port = old_node.port,
                .event_type = .departed,
            });
        }
    }

    return events.toOwnedSlice(allocator);
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "detectChanges: no changes" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    var old = try node_list.fromStatic(allocator, &addrs, 7500);
    defer old.deinit();
    var new = try node_list.fromStatic(allocator, &addrs, 7500);
    defer new.deinit();

    const events = try detectChanges(allocator, &old, &new);
    defer allocator.free(events);

    try std.testing.expectEqual(@as(usize, 0), events.len);
}

test "detectChanges: node joined" {
    const allocator = std.testing.allocator;
    const old_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    const new_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2", "10.0.0.3" };
    var old = try node_list.fromStatic(allocator, &old_addrs, 7500);
    defer old.deinit();
    var new = try node_list.fromStatic(allocator, &new_addrs, 7500);
    defer new.deinit();

    const events = try detectChanges(allocator, &old, &new);
    defer allocator.free(events);

    try std.testing.expectEqual(@as(usize, 1), events.len);
    try std.testing.expectEqual(EventType.joined, events[0].event_type);
    try std.testing.expectEqualSlices(u8, "10.0.0.3", events[0].address);
}

test "detectChanges: node departed" {
    const allocator = std.testing.allocator;
    const old_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2", "10.0.0.3" };
    const new_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.3" };
    var old = try node_list.fromStatic(allocator, &old_addrs, 7500);
    defer old.deinit();
    var new = try node_list.fromStatic(allocator, &new_addrs, 7500);
    defer new.deinit();

    const events = try detectChanges(allocator, &old, &new);
    defer allocator.free(events);

    try std.testing.expectEqual(@as(usize, 1), events.len);
    try std.testing.expectEqual(EventType.departed, events[0].event_type);
    try std.testing.expectEqualSlices(u8, "10.0.0.2", events[0].address);
}

test "detectChanges: simultaneous join and depart" {
    const allocator = std.testing.allocator;
    const old_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    const new_addrs = [_][]const u8{ "10.0.0.1", "10.0.0.3" };
    var old = try node_list.fromStatic(allocator, &old_addrs, 7500);
    defer old.deinit();
    var new = try node_list.fromStatic(allocator, &new_addrs, 7500);
    defer new.deinit();

    const events = try detectChanges(allocator, &old, &new);
    defer allocator.free(events);

    try std.testing.expectEqual(@as(usize, 2), events.len);
    // One join and one depart
    var joins: usize = 0;
    var departs: usize = 0;
    for (events) |e| {
        if (e.event_type == .joined) joins += 1;
        if (e.event_type == .departed) departs += 1;
    }
    try std.testing.expectEqual(@as(usize, 1), joins);
    try std.testing.expectEqual(@as(usize, 1), departs);
}
