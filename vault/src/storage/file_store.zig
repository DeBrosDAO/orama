/// File-per-user share storage with HMAC integrity.
///
/// Each user's data is stored in a directory named by their identity hash (hex).
/// Files are written atomically (write-to-temp + rename) to prevent corruption.
///
/// Directory layout:
///   <data_dir>/shares/<identity_hash_hex>/
///     meta.json          - Share metadata (JSON)
///     share.bin          - Share bytes (HMAC-integrity-protected; this layer does not encrypt)
///     wrapped_dek1.bin   - KEK1-wrapped DEK
///     wrapped_dek2.bin   - KEK2-wrapped DEK
///     checksum.bin       - HMAC-SHA256 of share.bin
const std = @import("std");
const hmac = @import("../crypto/hmac.zig");

pub const StoreError = error{
    IdentityHashRequired,
    ShareDataRequired,
    IntegrityCheckFailed,
    OutOfMemory,
    IoError,
};

/// Metadata for a stored share.
pub const ShareMetadata = struct {
    /// Monotonic version counter (for rollback protection)
    version: u64,
    /// Share index (x-coordinate in Shamir scheme)
    share_index: u8,
    /// Merkle commitment root (hex)
    commitment_root: [64]u8, // 32 bytes as hex
    /// Timestamp of last update (Unix epoch)
    timestamp: i64,
};

/// Writes share data atomically to the store.
/// Creates directory if it doesn't exist. Uses temp+rename for atomicity.
pub fn writeShare(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    share_data: []const u8,
    integrity_key: []const u8,
    allocator: std.mem.Allocator,
) !void {
    if (identity_hash_hex.len == 0) return StoreError.IdentityHashRequired;
    if (share_data.len == 0) return StoreError.ShareDataRequired;

    // Build path: <data_dir>/shares/<identity_hash_hex>/
    const share_dir = try std.fmt.allocPrint(allocator, "{s}/shares/{s}", .{ data_dir, identity_hash_hex });
    defer allocator.free(share_dir);

    // Create directory
    std.fs.cwd().makePath(share_dir) catch {
        return StoreError.IoError;
    };

    // Write share data atomically
    const share_path = try std.fmt.allocPrint(allocator, "{s}/share.bin", .{share_dir});
    defer allocator.free(share_path);
    const tmp_path = try std.fmt.allocPrint(allocator, "{s}/share.bin.tmp", .{share_dir});
    defer allocator.free(tmp_path);

    try atomicWrite(share_path, tmp_path, share_data);

    // Write HMAC checksum
    const checksum = hmac.compute(integrity_key, share_data);
    const checksum_path = try std.fmt.allocPrint(allocator, "{s}/checksum.bin", .{share_dir});
    defer allocator.free(checksum_path);
    const checksum_tmp = try std.fmt.allocPrint(allocator, "{s}/checksum.bin.tmp", .{share_dir});
    defer allocator.free(checksum_tmp);

    try atomicWrite(checksum_path, checksum_tmp, &checksum);
}

/// Reads share data from the store and verifies HMAC integrity.
/// Returns the share data. Caller must free.
pub fn readShare(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    integrity_key: []const u8,
    allocator: std.mem.Allocator,
) ![]u8 {
    // Build paths
    const share_path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/share.bin", .{ data_dir, identity_hash_hex });
    defer allocator.free(share_path);
    const checksum_path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/checksum.bin", .{ data_dir, identity_hash_hex });
    defer allocator.free(checksum_path);

    // Read share data
    const share_data = std.fs.cwd().readFileAlloc(allocator, share_path, 10 * 1024 * 1024) catch {
        return StoreError.IoError;
    };
    errdefer allocator.free(share_data);

    // Read checksum
    const checksum_data = std.fs.cwd().readFileAlloc(allocator, checksum_path, 32) catch {
        return StoreError.IoError;
    };
    defer allocator.free(checksum_data);

    if (checksum_data.len != 32) {
        return StoreError.IntegrityCheckFailed;
    }

    // Verify HMAC
    if (!hmac.verify(integrity_key, share_data, checksum_data[0..32].*)) {
        return StoreError.IntegrityCheckFailed;
    }

    return share_data;
}

/// Checks if a share exists for the given identity.
pub fn shareExists(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    allocator: std.mem.Allocator,
) !bool {
    const share_path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/share.bin", .{ data_dir, identity_hash_hex });
    defer allocator.free(share_path);

    std.fs.cwd().access(share_path, .{}) catch {
        return false;
    };
    return true;
}

/// Deletes all data for the given identity.
pub fn deleteShare(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    allocator: std.mem.Allocator,
) !void {
    const share_dir = try std.fmt.allocPrint(allocator, "{s}/shares/{s}", .{ data_dir, identity_hash_hex });
    defer allocator.free(share_dir);

    std.fs.cwd().deleteTree(share_dir) catch {};
}

/// V1 share metadata: the reconstruction parameters the share was split with.
/// Persisting these means a pull uses the ORIGINAL threshold, not one recomputed
/// from the live cluster size — otherwise growing/shrinking the fleet silently
/// bricks every existing backup.
pub const V1Meta = struct {
    version: u64,
    threshold: u8,
};

/// Write V1 metadata atomically. Written LAST (after share.bin + checksum.bin)
/// so meta.json acts as the commit marker: the share is only considered present
/// once meta exists, giving crash-consistency across the multi-file write.
pub fn writeMeta(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    meta: V1Meta,
    allocator: std.mem.Allocator,
) !void {
    if (identity_hash_hex.len == 0) return StoreError.IdentityHashRequired;

    const share_dir = try std.fmt.allocPrint(allocator, "{s}/shares/{s}", .{ data_dir, identity_hash_hex });
    defer allocator.free(share_dir);
    std.fs.cwd().makePath(share_dir) catch return StoreError.IoError;

    const json = try std.fmt.allocPrint(allocator, "{{\"version\":{d},\"threshold\":{d}}}", .{ meta.version, meta.threshold });
    defer allocator.free(json);

    const path = try std.fmt.allocPrint(allocator, "{s}/meta.json", .{share_dir});
    defer allocator.free(path);
    const tmp = try std.fmt.allocPrint(allocator, "{s}/meta.json.tmp", .{share_dir});
    defer allocator.free(tmp);

    try atomicWrite(path, tmp, json);
}

/// Read V1 metadata. Returns StoreError.IoError if absent or malformed.
pub fn readMeta(
    data_dir: []const u8,
    identity_hash_hex: []const u8,
    allocator: std.mem.Allocator,
) !V1Meta {
    const path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/meta.json", .{ data_dir, identity_hash_hex });
    defer allocator.free(path);

    const data = std.fs.cwd().readFileAlloc(allocator, path, 256) catch return StoreError.IoError;
    defer allocator.free(data);

    const parsed = std.json.parseFromSlice(V1Meta, allocator, data, .{ .ignore_unknown_fields = true }) catch return StoreError.IoError;
    defer parsed.deinit();
    return parsed.value;
}

// ── Internal helpers ─────────────────────────────────────────────────────────

fn atomicWrite(final_path: []const u8, tmp_path: []const u8, data: []const u8) !void {
    // Write to temp file
    const tmp_file = std.fs.cwd().createFile(tmp_path, .{}) catch {
        return StoreError.IoError;
    };
    defer tmp_file.close();
    tmp_file.writeAll(data) catch {
        return StoreError.IoError;
    };

    // Rename atomically
    std.fs.cwd().rename(tmp_path, final_path) catch {
        return StoreError.IoError;
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "write and read share round-trip" {
    const allocator = std.testing.allocator;

    // Use a temp directory
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "abcdef0123456789";
    const share_data = "test share data bytes";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeShare(tmp_path, identity, share_data, key, allocator);

    const read_data = try readShare(tmp_path, identity, key, allocator);
    defer allocator.free(read_data);

    try std.testing.expectEqualSlices(u8, share_data, read_data);
}

test "integrity check detects tampering" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "deadbeef";
    const share_data = "original data";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeShare(tmp_path, identity, share_data, key, allocator);

    // Tamper with the share file directly
    const share_path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/share.bin", .{ tmp_path, identity });
    defer allocator.free(share_path);

    const file = try std.fs.cwd().openFile(share_path, .{ .mode = .write_only });
    defer file.close();
    try file.writeAll("tampered data!");

    // Read should fail integrity check
    try std.testing.expectError(StoreError.IntegrityCheckFailed, readShare(tmp_path, identity, key, allocator));
}

test "shareExists: returns false for missing share" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const exists = try shareExists(tmp_path, "nonexistent", allocator);
    try std.testing.expect(!exists);
}

test "shareExists: returns true for existing share" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try writeShare(tmp_path, "exists", "data", "key-32-bytes-exactly-right!!!!!", allocator);

    const exists = try shareExists(tmp_path, "exists", allocator);
    try std.testing.expect(exists);
}

test "deleteShare: removes all files" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try writeShare(tmp_path, "todelete", "data", "key-32-bytes-exactly-right!!!!!", allocator);
    try deleteShare(tmp_path, "todelete", allocator);

    const exists = try shareExists(tmp_path, "todelete", allocator);
    try std.testing.expect(!exists);
}
