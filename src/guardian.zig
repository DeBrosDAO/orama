/// Top-level Guardian struct — orchestrates all subsystems.
///
/// The Guardian is the main runtime object that ties together:
/// - HTTP server (client-facing, port 7500)
/// - Peer protocol (guardian-to-guardian, port 7501)
/// - Node membership (via RQLite or static config)
/// - Heartbeat/health management
/// - Storage operations
const std = @import("std");
const log = @import("log.zig");
const config = @import("config.zig");
const node_list = @import("membership/node_list.zig");
const quorum = @import("membership/quorum.zig");
const heartbeat = @import("peer/heartbeat.zig");

pub const Guardian = struct {
    cfg: config.Config,
    nodes: node_list.NodeList,
    allocator: std.mem.Allocator,
    /// Random server secret for HMAC-based auth (generated at startup)
    server_secret: [32]u8,
    /// Share count cache (refreshed periodically)
    share_count: u32,

    pub fn init(allocator: std.mem.Allocator, cfg: config.Config) !Guardian {
        // Generate server secret
        var secret: [32]u8 = undefined;
        std.crypto.random.bytes(&secret);

        // Try to load node list from RQLite, fall back to self-only
        var nodes = node_list.fetchFromRqlite(allocator, cfg.rqlite_url, cfg.client_port) catch blk: {
            log.warn("failed to fetch node list from RQLite, running in single-node mode", .{});
            const self_addr = [_][]const u8{cfg.listen_address};
            break :blk try node_list.fromStatic(allocator, &self_addr, cfg.client_port);
        };

        // Mark self as alive
        if (nodes.nodes.len > 0) {
            nodes.self_index = 0;
            nodes.nodes[0].state = .alive;
            nodes.nodes[0].last_seen_ns = std.time.nanoTimestamp();
        }

        const share_count = heartbeat.countShares(cfg.data_dir);

        return Guardian{
            .cfg = cfg,
            .nodes = nodes,
            .allocator = allocator,
            .server_secret = secret,
            .share_count = share_count,
        };
    }

    pub fn deinit(self: *Guardian) void {
        self.nodes.deinit();
        // Zero out server secret
        @memset(&self.server_secret, 0);
    }

    /// Get current write quorum requirement.
    pub fn writeQuorum(self: *const Guardian) usize {
        return quorum.writeQuorum(self.nodes.aliveCount());
    }

    /// Get current Shamir threshold (read quorum).
    pub fn readThreshold(self: *const Guardian) usize {
        return self.nodes.threshold();
    }

    /// Refresh share count from disk.
    pub fn refreshShareCount(self: *Guardian) void {
        self.share_count = heartbeat.countShares(self.cfg.data_dir);
    }
};

// ── Tests ────────────────────────────────────────────────────────────────────

test "guardian: init and deinit" {
    const allocator = std.testing.allocator;
    const cfg = config.Config{
        .data_dir = "/tmp/nonexistent-vault-test",
    };

    var g = try Guardian.init(allocator, cfg);
    defer g.deinit();

    try std.testing.expectEqual(@as(u32, 0), g.share_count);
}
