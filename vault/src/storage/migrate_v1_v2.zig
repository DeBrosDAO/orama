/// One-time migration from V1 (shares/<id>/) to V2 (vaults/<id>/default/).
///
/// V1 layout:
///   <data_dir>/shares/<identity>/
///     share.bin
///     checksum.bin
///     version       (plain text u64)
///
/// V2 layout:
///   <data_dir>/vaults/<identity>/default/
///     share.bin
///     checksum.bin
///     meta.json
///
/// After migration, V1 directories are renamed to <identity>.migrated/ as backup.
const std = @import("std");

/// Checks if V1 data exists that needs migration.
/// Returns true if <data_dir>/shares/ exists and contains at least one subdirectory.
pub fn needsMigration(data_dir: []const u8, allocator: std.mem.Allocator) bool {
    const shares_path = std.fmt.allocPrint(allocator, "{s}/shares", .{data_dir}) catch return false;
    defer allocator.free(shares_path);

    var dir = std.fs.cwd().openDir(shares_path, .{ .iterate = true }) catch return false;
    defer dir.close();

    var it = dir.iterate();
    while (it.next() catch null) |entry| {
        if (entry.kind == .directory) {
            // Skip already-migrated directories
            if (std.mem.endsWith(u8, entry.name, ".migrated")) continue;
            return true;
        }
    }
    return false;
}

/// Migrates all V1 share directories to V2 vault format.
/// Returns the number of identities migrated.
pub fn migrateV1toV2(data_dir: []const u8, allocator: std.mem.Allocator) !u32 {
    const shares_path = std.fmt.allocPrint(allocator, "{s}/shares", .{data_dir}) catch return error.OutOfMemory;
    defer allocator.free(shares_path);

    var dir = std.fs.cwd().openDir(shares_path, .{ .iterate = true }) catch {
        return 0; // No shares dir = nothing to migrate
    };
    defer dir.close();

    var count: u32 = 0;
    var it = dir.iterate();
    while (try it.next()) |entry| {
        if (entry.kind != .directory) continue;
        // Skip already-migrated directories
        if (std.mem.endsWith(u8, entry.name, ".migrated")) continue;

        const migrated = migrateOneIdentity(data_dir, entry.name, allocator);
        if (migrated) {
            count += 1;
        }
    }

    return count;
}

/// Migrate a single identity from V1 to V2.
fn migrateOneIdentity(data_dir: []const u8, identity: []const u8, allocator: std.mem.Allocator) bool {
    // Source paths
    const src_share = std.fmt.allocPrint(allocator, "{s}/shares/{s}/share.bin", .{ data_dir, identity }) catch return false;
    defer allocator.free(src_share);
    const src_checksum = std.fmt.allocPrint(allocator, "{s}/shares/{s}/checksum.bin", .{ data_dir, identity }) catch return false;
    defer allocator.free(src_checksum);
    const src_version = std.fmt.allocPrint(allocator, "{s}/shares/{s}/version", .{ data_dir, identity }) catch return false;
    defer allocator.free(src_version);

    // Destination directory: vaults/<identity>/default/
    const dst_dir = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/default", .{ data_dir, identity }) catch return false;
    defer allocator.free(dst_dir);

    // Create destination directory
    std.fs.cwd().makePath(dst_dir) catch return false;

    // Copy share.bin
    const dst_share = std.fmt.allocPrint(allocator, "{s}/share.bin", .{dst_dir}) catch return false;
    defer allocator.free(dst_share);
    copyFile(src_share, dst_share) catch return false;

    // Copy checksum.bin
    const dst_checksum = std.fmt.allocPrint(allocator, "{s}/checksum.bin", .{dst_dir}) catch return false;
    defer allocator.free(dst_checksum);
    copyFile(src_checksum, dst_checksum) catch return false;

    // Read version file and create meta.json
    const version = readVersionFile(src_version, allocator);
    const now = std.time.nanoTimestamp();

    // Read share.bin size for meta
    const share_size = getFileSize(src_share);

    var meta_buf: [512]u8 = undefined;
    const meta_json = std.fmt.bufPrint(&meta_buf,
        \\{{"version":{d},"created_ns":{d},"updated_ns":{d},"size":{d}}}
    , .{ version, now, now, share_size }) catch return false;

    const dst_meta = std.fmt.allocPrint(allocator, "{s}/meta.json", .{dst_dir}) catch return false;
    defer allocator.free(dst_meta);

    writeFile(dst_meta, meta_json) catch return false;

    // Rename source to .migrated backup
    const src_dir = std.fmt.allocPrint(allocator, "{s}/shares/{s}", .{ data_dir, identity }) catch return false;
    defer allocator.free(src_dir);
    const migrated_dir = std.fmt.allocPrint(allocator, "{s}/shares/{s}.migrated", .{ data_dir, identity }) catch return false;
    defer allocator.free(migrated_dir);

    std.fs.cwd().rename(src_dir, migrated_dir) catch {
        // Migration succeeded but rename failed — not fatal
        return true;
    };

    return true;
}

fn copyFile(src: []const u8, dst: []const u8) !void {
    const src_file = try std.fs.cwd().openFile(src, .{});
    defer src_file.close();

    const dst_file = try std.fs.cwd().createFile(dst, .{});
    defer dst_file.close();

    var buf: [8192]u8 = undefined;
    while (true) {
        const bytes_read = try src_file.read(&buf);
        if (bytes_read == 0) break;
        try dst_file.writeAll(buf[0..bytes_read]);
    }
}

fn readVersionFile(path: []const u8, allocator: std.mem.Allocator) u64 {
    const data = std.fs.cwd().readFileAlloc(allocator, path, 32) catch return 1;
    defer allocator.free(data);
    return std.fmt.parseInt(u64, std.mem.trim(u8, data, &std.ascii.whitespace), 10) catch 1;
}

fn getFileSize(path: []const u8) usize {
    const file = std.fs.cwd().openFile(path, .{}) catch return 0;
    defer file.close();
    const stat = file.stat() catch return 0;
    return stat.size;
}

fn writeFile(path: []const u8, data: []const u8) !void {
    const file = try std.fs.cwd().createFile(path, .{});
    defer file.close();
    try file.writeAll(data);
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "needsMigration: false when no shares dir" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try std.testing.expect(!needsMigration(tmp_path, allocator));
}

test "needsMigration: false when shares dir is empty" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const shares_path = try std.fmt.allocPrint(allocator, "{s}/shares", .{tmp_path});
    defer allocator.free(shares_path);
    try std.fs.cwd().makePath(shares_path);

    try std.testing.expect(!needsMigration(tmp_path, allocator));
}

test "needsMigration: true when shares has subdirectories" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const id_dir = try std.fmt.allocPrint(allocator, "{s}/shares/abcdef1234", .{tmp_path});
    defer allocator.free(id_dir);
    try std.fs.cwd().makePath(id_dir);

    try std.testing.expect(needsMigration(tmp_path, allocator));
}

test "migrateV1toV2: full migration" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    // Create V1 structure
    const identity = "aabbccdd11223344";
    const id_dir = try std.fmt.allocPrint(allocator, "{s}/shares/{s}", .{ tmp_path, identity });
    defer allocator.free(id_dir);
    try std.fs.cwd().makePath(id_dir);

    // Write V1 files
    const share_path = try std.fmt.allocPrint(allocator, "{s}/share.bin", .{id_dir});
    defer allocator.free(share_path);
    try writeFile(share_path, "share data here");

    const checksum_path = try std.fmt.allocPrint(allocator, "{s}/checksum.bin", .{id_dir});
    defer allocator.free(checksum_path);
    try writeFile(checksum_path, "checksum bytes here");

    const version_path = try std.fmt.allocPrint(allocator, "{s}/version", .{id_dir});
    defer allocator.free(version_path);
    try writeFile(version_path, "7");

    // Run migration
    const count = try migrateV1toV2(tmp_path, allocator);
    try std.testing.expectEqual(@as(u32, 1), count);

    // Verify V2 files exist
    const v2_share = try std.fmt.allocPrint(allocator, "{s}/vaults/{s}/default/share.bin", .{ tmp_path, identity });
    defer allocator.free(v2_share);
    std.fs.cwd().access(v2_share, .{}) catch {
        return error.TestUnexpectedResult;
    };

    const v2_meta = try std.fmt.allocPrint(allocator, "{s}/vaults/{s}/default/meta.json", .{ tmp_path, identity });
    defer allocator.free(v2_meta);
    std.fs.cwd().access(v2_meta, .{}) catch {
        return error.TestUnexpectedResult;
    };

    // Verify V1 dir was renamed to .migrated
    const migrated_dir = try std.fmt.allocPrint(allocator, "{s}/shares/{s}.migrated", .{ tmp_path, identity });
    defer allocator.free(migrated_dir);
    std.fs.cwd().access(migrated_dir, .{}) catch {
        return error.TestUnexpectedResult;
    };

    // Verify meta.json has correct version
    const meta_data = try std.fs.cwd().readFileAlloc(allocator, v2_meta, 4096);
    defer allocator.free(meta_data);

    const MetaJson = struct {
        version: u64,
        created_ns: i128,
        updated_ns: i128,
        size: usize,
    };
    const parsed = try std.json.parseFromSlice(MetaJson, allocator, meta_data, .{});
    defer parsed.deinit();
    try std.testing.expectEqual(@as(u64, 7), parsed.value.version);
    try std.testing.expectEqual(@as(usize, 15), parsed.value.size); // "share data here" = 15 bytes

    // needsMigration should now be false (original dir was renamed)
    try std.testing.expect(!needsMigration(tmp_path, allocator));
}

test "migrateV1toV2: skips already migrated" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    // Create a .migrated directory (already migrated)
    const migrated_dir = try std.fmt.allocPrint(allocator, "{s}/shares/already.migrated", .{tmp_path});
    defer allocator.free(migrated_dir);
    try std.fs.cwd().makePath(migrated_dir);

    const count = try migrateV1toV2(tmp_path, allocator);
    try std.testing.expectEqual(@as(u32, 0), count);
}
