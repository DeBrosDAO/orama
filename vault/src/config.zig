/// Configuration loading for vault-guardian.
/// Reads a simple key=value config file. Lines starting with '#' are comments.
const std = @import("std");

pub const Config = struct {
    /// Address to bind client-facing server
    listen_address: []const u8 = "0.0.0.0",
    /// Client-facing port (TLS in production, plain TCP for MVP)
    client_port: u16 = 7500,
    /// Guardian-to-guardian port (WireGuard-only interface)
    peer_port: u16 = 7501,
    /// Data storage directory
    data_dir: []const u8 = "/opt/orama/.orama/data/vault",
    /// RQLite endpoint for node discovery
    rqlite_url: []const u8 = "http://127.0.0.1:4001",

    /// Arena allocator that owns duped strings (null when using defaults)
    _arena: ?std.heap.ArenaAllocator = null,

    /// Free all allocated memory.
    pub fn deinit(self: *Config) void {
        if (self._arena) |*arena| {
            arena.deinit();
        }
        self.* = undefined;
    }
};

pub const ConfigError = error{
    InvalidPort,
    LineTooLong,
};

/// Loads config from file, or returns defaults if file doesn't exist.
pub fn loadOrDefault(allocator: std.mem.Allocator, path: []const u8) !Config {
    // Read whole file into memory (config files are small)
    const contents = std.fs.cwd().readFileAlloc(allocator, path, 64 * 1024) catch |err| {
        if (err == error.FileNotFound) {
            return Config{};
        }
        return err;
    };
    defer allocator.free(contents);

    var cfg = Config{};
    var arena = std.heap.ArenaAllocator.init(allocator);
    errdefer arena.deinit();
    const arena_alloc = arena.allocator();

    var line_iter = std.mem.splitScalar(u8, contents, '\n');
    while (line_iter.next()) |raw_line| {
        // Strip trailing \r for Windows line endings
        const line_trimmed = if (raw_line.len > 0 and raw_line[raw_line.len - 1] == '\r')
            raw_line[0 .. raw_line.len - 1]
        else
            raw_line;

        const line = std.mem.trim(u8, line_trimmed, " \t");

        // Skip empty lines and comments
        if (line.len == 0) continue;
        if (line[0] == '#') continue;

        // Find '='
        const eq_idx = std.mem.indexOfScalar(u8, line, '=') orelse continue;
        const key = std.mem.trim(u8, line[0..eq_idx], " \t");
        const value = std.mem.trim(u8, line[eq_idx + 1 ..], " \t");

        if (std.mem.eql(u8, key, "listen_address")) {
            cfg.listen_address = try arena_alloc.dupe(u8, value);
        } else if (std.mem.eql(u8, key, "client_port")) {
            cfg.client_port = std.fmt.parseInt(u16, value, 10) catch return ConfigError.InvalidPort;
        } else if (std.mem.eql(u8, key, "peer_port")) {
            cfg.peer_port = std.fmt.parseInt(u16, value, 10) catch return ConfigError.InvalidPort;
        } else if (std.mem.eql(u8, key, "data_dir")) {
            cfg.data_dir = try arena_alloc.dupe(u8, value);
        } else if (std.mem.eql(u8, key, "rqlite_url")) {
            cfg.rqlite_url = try arena_alloc.dupe(u8, value);
        }
        // Unknown keys are silently ignored
    }

    cfg._arena = arena;
    return cfg;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "config: defaults when file not found" {
    var cfg = try loadOrDefault(std.testing.allocator, "/tmp/nonexistent-vault-config-file-xyz");
    defer cfg.deinit();

    try std.testing.expectEqualSlices(u8, "0.0.0.0", cfg.listen_address);
    try std.testing.expectEqual(@as(u16, 7500), cfg.client_port);
    try std.testing.expectEqual(@as(u16, 7501), cfg.peer_port);
    try std.testing.expectEqualSlices(u8, "/opt/orama/.orama/data/vault", cfg.data_dir);
    try std.testing.expectEqualSlices(u8, "http://127.0.0.1:4001", cfg.rqlite_url);
}

test "config: parse key=value file" {
    // Write a temp config file
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();

    const file = try tmp_dir.dir.createFile("test.conf", .{});
    try file.writeAll(
        \\# This is a comment
        \\listen_address = 127.0.0.1
        \\client_port = 8080
        \\peer_port = 8081
        \\data_dir = /tmp/vault-test
        \\rqlite_url = http://10.0.0.1:4001
        \\
        \\# Another comment
        \\unknown_key = ignored
        \\
    );
    file.close();

    // Get the full path
    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_path = try tmp_dir.dir.realpath(".", &path_buf);
    var full_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const full_path = try std.fmt.bufPrint(&full_path_buf, "{s}/test.conf", .{dir_path});

    var cfg = try loadOrDefault(std.testing.allocator, full_path);
    defer cfg.deinit();

    try std.testing.expectEqualSlices(u8, "127.0.0.1", cfg.listen_address);
    try std.testing.expectEqual(@as(u16, 8080), cfg.client_port);
    try std.testing.expectEqual(@as(u16, 8081), cfg.peer_port);
    try std.testing.expectEqualSlices(u8, "/tmp/vault-test", cfg.data_dir);
    try std.testing.expectEqualSlices(u8, "http://10.0.0.1:4001", cfg.rqlite_url);
}

test "config: partial config uses defaults for missing keys" {
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();

    const file = try tmp_dir.dir.createFile("partial.conf", .{});
    try file.writeAll("client_port = 9000\n");
    file.close();

    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_path = try tmp_dir.dir.realpath(".", &path_buf);
    var full_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const full_path = try std.fmt.bufPrint(&full_path_buf, "{s}/partial.conf", .{dir_path});

    var cfg = try loadOrDefault(std.testing.allocator, full_path);
    defer cfg.deinit();

    try std.testing.expectEqual(@as(u16, 9000), cfg.client_port);
    // Defaults for everything else
    try std.testing.expectEqualSlices(u8, "0.0.0.0", cfg.listen_address);
    try std.testing.expectEqual(@as(u16, 7501), cfg.peer_port);
}

test "config: invalid port returns error" {
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();

    const file = try tmp_dir.dir.createFile("bad.conf", .{});
    try file.writeAll("client_port = not_a_number\n");
    file.close();

    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_path = try tmp_dir.dir.realpath(".", &path_buf);
    var full_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const full_path = try std.fmt.bufPrint(&full_path_buf, "{s}/bad.conf", .{dir_path});

    const result = loadOrDefault(std.testing.allocator, full_path);
    try std.testing.expectError(ConfigError.InvalidPort, result);
}

test "config: empty file returns defaults" {
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();

    const file = try tmp_dir.dir.createFile("empty.conf", .{});
    file.close();

    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_path = try tmp_dir.dir.realpath(".", &path_buf);
    var full_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const full_path = try std.fmt.bufPrint(&full_path_buf, "{s}/empty.conf", .{dir_path});

    var cfg = try loadOrDefault(std.testing.allocator, full_path);
    defer cfg.deinit();

    try std.testing.expectEqual(@as(u16, 7500), cfg.client_port);
}

test "config: comments and blank lines are skipped" {
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();

    const file = try tmp_dir.dir.createFile("comments.conf", .{});
    try file.writeAll(
        \\# Full line comment
        \\
        \\  # Indented comment
        \\client_port = 1234
        \\
    );
    file.close();

    var path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_path = try tmp_dir.dir.realpath(".", &path_buf);
    var full_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const full_path = try std.fmt.bufPrint(&full_path_buf, "{s}/comments.conf", .{dir_path});

    var cfg = try loadOrDefault(std.testing.allocator, full_path);
    defer cfg.deinit();

    try std.testing.expectEqual(@as(u16, 1234), cfg.client_port);
}
