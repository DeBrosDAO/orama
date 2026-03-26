/// Node list management — discovers guardians via RQLite HTTP API.
///
/// Every node in the Orama network runs a vault guardian. The node list
/// is fetched from RQLite (source of truth for cluster membership).
///
/// RQLite status endpoint: GET /status → includes node list in raft info.
/// For MVP: configurable static list + RQLite discovery.
const std = @import("std");
const log = @import("../log.zig");

pub const Node = struct {
    /// WireGuard IP address (e.g., "10.0.0.1")
    address: []const u8,
    /// Vault guardian client port
    port: u16,
    /// Whether this node is reachable
    state: NodeState,
    /// Last successful heartbeat (nanos since epoch)
    last_seen_ns: i128,
};

pub const NodeState = enum {
    alive,
    suspect,
    dead,
    unknown,
};

pub const NodeList = struct {
    nodes: []Node,
    allocator: std.mem.Allocator,
    /// Self node index (-1 if not identified)
    self_index: ?usize,

    pub fn deinit(self: *NodeList) void {
        for (self.nodes) |node| {
            self.allocator.free(node.address);
        }
        self.allocator.free(self.nodes);
    }

    /// Number of alive nodes.
    pub fn aliveCount(self: *const NodeList) usize {
        var count: usize = 0;
        for (self.nodes) |node| {
            if (node.state == .alive) count += 1;
        }
        return count;
    }

    /// Compute adaptive threshold: max(3, floor(N/3))
    pub fn threshold(self: *const NodeList) usize {
        const alive = self.aliveCount();
        const t = alive / 3;
        return if (t < 3) 3 else t;
    }

    /// Update a node's state.
    pub fn updateState(self: *NodeList, address: []const u8, port: u16, state: NodeState) void {
        for (self.nodes) |*node| {
            if (std.mem.eql(u8, node.address, address) and node.port == port) {
                node.state = state;
                if (state == .alive) {
                    node.last_seen_ns = std.time.nanoTimestamp();
                }
                return;
            }
        }
    }

    /// Get all alive nodes except self.
    pub fn peers(self: *const NodeList, allocator: std.mem.Allocator) ![]Node {
        var list = std.ArrayListUnmanaged(Node){};
        errdefer list.deinit(allocator);

        for (self.nodes, 0..) |node, i| {
            if (self.self_index != null and i == self.self_index.?) continue;
            if (node.state == .alive) {
                try list.append(allocator, node);
            }
        }
        return list.toOwnedSlice(allocator);
    }
};

/// Fetch node list from RQLite status endpoint.
/// RQLite GET /status returns JSON with raft node info.
///
/// For MVP: parses a simple JSON response. In production, this would use
/// a proper HTTP client with retries and TLS.
pub fn fetchFromRqlite(allocator: std.mem.Allocator, rqlite_url: []const u8, vault_port: u16) !NodeList {
    _ = rqlite_url; // TODO: actual HTTP fetch
    _ = vault_port;

    // MVP: return empty list (single-node mode)
    // The caller should fall back to static config or self-only mode.
    const nodes = try allocator.alloc(Node, 0);
    return NodeList{
        .nodes = nodes,
        .allocator = allocator,
        .self_index = null,
    };
}

/// Create a node list from a static list of addresses.
/// Used for testing and as fallback when RQLite is unavailable.
pub fn fromStatic(allocator: std.mem.Allocator, addresses: []const []const u8, port: u16) !NodeList {
    const nodes = try allocator.alloc(Node, addresses.len);
    errdefer allocator.free(nodes);

    for (addresses, 0..) |addr, i| {
        nodes[i] = Node{
            .address = try allocator.dupe(u8, addr),
            .port = port,
            .state = .unknown,
            .last_seen_ns = 0,
        };
    }

    return NodeList{
        .nodes = nodes,
        .allocator = allocator,
        .self_index = null,
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "node_list: static creation and threshold" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5" };
    var nl = try fromStatic(allocator, &addrs, 7500);
    defer nl.deinit();

    try std.testing.expectEqual(@as(usize, 5), nl.nodes.len);
    try std.testing.expectEqual(@as(usize, 0), nl.aliveCount());
    try std.testing.expectEqual(@as(usize, 3), nl.threshold()); // 0 alive → max(3, 0/3) = 3
}

test "node_list: alive count and threshold" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{
        "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5",
        "10.0.0.6", "10.0.0.7", "10.0.0.8", "10.0.0.9", "10.0.0.10",
        "10.0.0.11", "10.0.0.12", "10.0.0.13", "10.0.0.14",
    };
    var nl = try fromStatic(allocator, &addrs, 7500);
    defer nl.deinit();

    // Mark all alive
    for (nl.nodes) |*node| {
        node.state = .alive;
    }

    try std.testing.expectEqual(@as(usize, 14), nl.aliveCount());
    try std.testing.expectEqual(@as(usize, 4), nl.threshold()); // 14/3 = 4
}

test "node_list: updateState" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2" };
    var nl = try fromStatic(allocator, &addrs, 7500);
    defer nl.deinit();

    nl.updateState("10.0.0.1", 7500, .alive);
    try std.testing.expectEqual(NodeState.alive, nl.nodes[0].state);
    try std.testing.expectEqual(NodeState.unknown, nl.nodes[1].state);
    try std.testing.expectEqual(@as(usize, 1), nl.aliveCount());

    nl.updateState("10.0.0.1", 7500, .dead);
    try std.testing.expectEqual(NodeState.dead, nl.nodes[0].state);
    try std.testing.expectEqual(@as(usize, 0), nl.aliveCount());
}

test "node_list: peers excludes self" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2", "10.0.0.3" };
    var nl = try fromStatic(allocator, &addrs, 7500);
    defer nl.deinit();

    nl.self_index = 1; // we are 10.0.0.2
    for (nl.nodes) |*node| node.state = .alive;

    const peer_list = try nl.peers(allocator);
    defer allocator.free(peer_list);

    try std.testing.expectEqual(@as(usize, 2), peer_list.len);
}

test "node_list: threshold minimum is 3" {
    const allocator = std.testing.allocator;
    const addrs = [_][]const u8{ "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5" };
    var nl = try fromStatic(allocator, &addrs, 7500);
    defer nl.deinit();

    // Only 5 alive → 5/3 = 1, but minimum is 3
    for (nl.nodes) |*node| node.state = .alive;
    try std.testing.expectEqual(@as(usize, 3), nl.threshold());
}

test "node_list: fetchFromRqlite returns empty (MVP)" {
    const allocator = std.testing.allocator;
    var nl = try fetchFromRqlite(allocator, "http://127.0.0.1:4001", 7500);
    defer nl.deinit();

    try std.testing.expectEqual(@as(usize, 0), nl.nodes.len);
}
