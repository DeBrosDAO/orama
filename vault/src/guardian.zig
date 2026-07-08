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
    /// Random server secret for HMAC-based auth (generated at startup).
    /// Ephemeral by design: regenerated each boot and used only to sign
    /// session/challenge tokens, which clients re-acquire on demand.
    server_secret: [32]u8,
    /// Persistent at-rest integrity key, loaded/created in data_dir. Keys the
    /// HMAC over stored share files, so it MUST survive restarts — otherwise
    /// every stored share fails its integrity check after a reboot.
    integrity_key: [32]u8,
    /// Share count cache (refreshed periodically)
    share_count: u32,

    pub fn init(allocator: std.mem.Allocator, cfg: config.Config) !Guardian {
        // Generate ephemeral server secret (auth tokens only).
        var secret: [32]u8 = undefined;
        std.crypto.random.bytes(&secret);

        // Load (or create) the persistent at-rest integrity key.
        const integrity_key = loadOrCreateIntegrityKey(cfg.data_dir, allocator);

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
            .integrity_key = integrity_key,
            .share_count = share_count,
        };
    }

    pub fn deinit(self: *Guardian) void {
        self.nodes.deinit();
        // Zero out secrets
        @memset(&self.server_secret, 0);
        @memset(&self.integrity_key, 0);
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

/// Load the persistent at-rest integrity key from <data_dir>/integrity.key,
/// creating it with fresh randomness on first run.
///
/// This key authenticates share files on disk, so it MUST persist across
/// restarts. The previous implementation keyed at-rest integrity with the
/// ephemeral server_secret, which meant every stored share became unreadable
/// after any restart (deploy, crash, reboot). If the key cannot be persisted we
/// fall back to an in-memory random key and warn loudly (degraded: shares
/// written this boot won't survive a restart).
fn loadOrCreateIntegrityKey(data_dir: []const u8, allocator: std.mem.Allocator) [32]u8 {
    var key: [32]u8 = undefined;

    const path = std.fmt.allocPrint(allocator, "{s}/integrity.key", .{data_dir}) catch {
        std.crypto.random.bytes(&key);
        log.err("integrity key path alloc failed; using ephemeral key (shares will not survive restart)", .{});
        return key;
    };
    defer allocator.free(path);

    // Load an existing, well-formed key if present.
    if (std.fs.cwd().readFileAlloc(allocator, path, 32)) |existing| {
        defer allocator.free(existing);
        if (existing.len == 32) {
            @memcpy(&key, existing[0..32]);
            return key;
        }
        log.warn("integrity.key has unexpected size {d}; regenerating", .{existing.len});
    } else |_| {}

    // Create a fresh key and persist it atomically with 0600 perms.
    std.crypto.random.bytes(&key);
    std.fs.cwd().makePath(data_dir) catch {};

    const tmp_path = std.fmt.allocPrint(allocator, "{s}/integrity.key.tmp", .{data_dir}) catch {
        log.err("integrity key persist failed (tmp path); using ephemeral key", .{});
        return key;
    };
    defer allocator.free(tmp_path);

    persist: {
        const file = std.fs.cwd().createFile(tmp_path, .{ .mode = 0o600 }) catch {
            log.err("could not create integrity key file; using ephemeral key (shares will not survive restart)", .{});
            break :persist;
        };
        {
            defer file.close();
            file.writeAll(&key) catch {
                log.err("could not write integrity key; using ephemeral key", .{});
                break :persist;
            };
        }
        std.fs.cwd().rename(tmp_path, path) catch {
            std.fs.cwd().deleteFile(tmp_path) catch {};
            log.err("could not finalize integrity key; using ephemeral key", .{});
            break :persist;
        };
        log.info("initialized persistent vault integrity key", .{});
    }
    return key;
}

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
