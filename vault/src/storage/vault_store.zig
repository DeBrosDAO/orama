/// Multi-secret storage engine (V2).
///
/// Each identity can store up to MAX_SECRETS_PER_IDENTITY named secrets.
/// Secrets are stored with HMAC integrity, anti-rollback version checks,
/// and atomic writes (tmp+rename).
///
/// Directory layout:
///   <data_dir>/vaults/<identity_hex>/
///     <secret_name>/
///       share.bin       - Encrypted share data
///       checksum.bin    - HMAC-SHA256 integrity
///       meta.json       - {"version":1,"created_ns":...,"updated_ns":...,"size":123}
const std = @import("std");
const hmac = @import("../crypto/hmac.zig");

// ── Constants ────────────────────────────────────────────────────────────────

pub const MAX_SECRETS_PER_IDENTITY = 1000;
pub const MAX_SECRET_NAME_LEN = 128;
pub const MAX_SECRET_SIZE = 512 * 1024; // 512 KiB

/// Characters allowed in secret names: alphanumeric, underscore, hyphen.
fn isValidNameChar(c: u8) bool {
    return std.ascii.isAlphanumeric(c) or c == '_' or c == '-';
}

// ── Types ────────────────────────────────────────────────────────────────────

pub const SecretMeta = struct {
    version: u64,
    created_ns: i128,
    updated_ns: i128,
    size: usize,
};

pub const VaultStoreError = error{
    IdentityRequired,
    SecretNameRequired,
    SecretNameTooLong,
    SecretNameInvalid,
    SecretDataRequired,
    SecretDataTooLarge,
    SecretLimitExceeded,
    IntegrityCheckFailed,
    VersionConflict,
    NotFound,
    OutOfMemory,
    IoError,
};

// ── Public API ───────────────────────────────────────────────────────────────

/// Validates a secret name: non-empty, within length limit, alphanumeric + '_' + '-'.
pub fn validateSecretName(name: []const u8) VaultStoreError!void {
    if (name.len == 0) return VaultStoreError.SecretNameRequired;
    if (name.len > MAX_SECRET_NAME_LEN) return VaultStoreError.SecretNameTooLong;
    for (name) |c| {
        if (!isValidNameChar(c)) return VaultStoreError.SecretNameInvalid;
    }
}

/// Writes a named secret atomically with HMAC integrity and anti-rollback protection.
///
/// For new secrets, checks the per-identity secret count limit.
/// For existing secrets, rejects if `version` <= the stored version.
/// Writes share.bin, checksum.bin, and meta.json atomically (tmp+rename).
pub fn writeSecret(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    data: []const u8,
    version: u64,
    integrity_key: []const u8,
    allocator: std.mem.Allocator,
) VaultStoreError!void {
    if (identity.len == 0) return VaultStoreError.IdentityRequired;
    try validateSecretName(name);
    if (data.len == 0) return VaultStoreError.SecretDataRequired;
    if (data.len > MAX_SECRET_SIZE) return VaultStoreError.SecretDataTooLarge;

    // Build secret directory path: <data_dir>/vaults/<identity>/<name>/
    const secret_dir = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}", .{ data_dir, identity, name }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(secret_dir);

    // Check if this is a new secret
    const is_new = !secretExistsInternal(data_dir, identity, name, allocator);

    if (is_new) {
        // Check secret count limit
        const count = countSecrets(data_dir, identity, allocator) catch 0;
        if (count >= MAX_SECRETS_PER_IDENTITY) return VaultStoreError.SecretLimitExceeded;
    } else {
        // Anti-rollback: reject if version <= existing
        const existing_meta = readMeta(data_dir, identity, name, allocator) catch |err| {
            // If we can't read meta but the dir exists, allow overwrite
            if (err == VaultStoreError.IoError) return VaultStoreError.IoError;
            // For NotFound, treat as new
            if (err == VaultStoreError.NotFound) {
                // directory exists but meta doesn't - allow write
                return writeSecretInner(secret_dir, data, version, integrity_key, null, allocator);
            }
            return err;
        };
        if (version <= existing_meta.version) return VaultStoreError.VersionConflict;
    }

    // Determine created_ns: keep existing for updates, new timestamp for new secrets
    const created_ns: ?i128 = if (!is_new) blk: {
        const existing_meta = readMeta(data_dir, identity, name, allocator) catch break :blk null;
        break :blk existing_meta.created_ns;
    } else null;

    return writeSecretInner(secret_dir, data, version, integrity_key, created_ns, allocator);
}

/// Reads a named secret and verifies HMAC integrity.
/// Caller must free the returned slice.
pub fn readSecret(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    integrity_key: []const u8,
    allocator: std.mem.Allocator,
) VaultStoreError![]u8 {
    // Build paths
    const share_path = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}/share.bin", .{ data_dir, identity, name }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(share_path);

    const checksum_path = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}/checksum.bin", .{ data_dir, identity, name }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(checksum_path);

    // Read share data
    const share_data = std.fs.cwd().readFileAlloc(allocator, share_path, MAX_SECRET_SIZE) catch {
        return VaultStoreError.NotFound;
    };
    errdefer allocator.free(share_data);

    // Read checksum
    const checksum_data = std.fs.cwd().readFileAlloc(allocator, checksum_path, 32) catch {
        return VaultStoreError.IoError;
    };
    defer allocator.free(checksum_data);

    if (checksum_data.len != 32) {
        return VaultStoreError.IntegrityCheckFailed;
    }

    // Verify HMAC
    if (!hmac.verify(integrity_key, share_data, checksum_data[0..32].*)) {
        return VaultStoreError.IntegrityCheckFailed;
    }

    return share_data;
}

/// Reads and parses meta.json for a named secret.
pub fn readMeta(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    allocator: std.mem.Allocator,
) VaultStoreError!SecretMeta {
    const meta_path = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}/meta.json", .{ data_dir, identity, name }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(meta_path);

    const meta_data = std.fs.cwd().readFileAlloc(allocator, meta_path, 4096) catch {
        return VaultStoreError.NotFound;
    };
    defer allocator.free(meta_data);

    const parsed = std.json.parseFromSlice(MetaJson, allocator, meta_data, .{}) catch {
        return VaultStoreError.IoError;
    };
    defer parsed.deinit();

    return SecretMeta{
        .version = parsed.value.version,
        .created_ns = parsed.value.created_ns,
        .updated_ns = parsed.value.updated_ns,
        .size = parsed.value.size,
    };
}

/// Deletes a named secret (removes the entire <name>/ directory).
pub fn deleteSecret(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    allocator: std.mem.Allocator,
) VaultStoreError!void {
    const secret_dir = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}", .{ data_dir, identity, name }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(secret_dir);

    // Check it exists first
    std.fs.cwd().access(secret_dir, .{}) catch {
        return VaultStoreError.NotFound;
    };

    std.fs.cwd().deleteTree(secret_dir) catch {
        return VaultStoreError.IoError;
    };
}

/// Checks if a named secret exists for the given identity.
pub fn secretExists(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    allocator: std.mem.Allocator,
) !bool {
    return secretExistsInternal(data_dir, identity, name, allocator);
}

/// Lists all secret names for an identity.
/// Caller must free each name slice and the returned slice.
pub fn listSecrets(
    data_dir: []const u8,
    identity: []const u8,
    allocator: std.mem.Allocator,
) ![][]const u8 {
    const vault_dir = std.fmt.allocPrint(allocator, "{s}/vaults/{s}", .{ data_dir, identity }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(vault_dir);

    var dir = std.fs.cwd().openDir(vault_dir, .{ .iterate = true }) catch {
        // No vault dir = no secrets
        return allocator.alloc([]const u8, 0) catch return VaultStoreError.OutOfMemory;
    };
    defer dir.close();

    var names: std.ArrayListUnmanaged([]const u8) = .{};
    errdefer {
        for (names.items) |n| allocator.free(n);
        names.deinit(allocator);
    }

    var it = dir.iterate();
    while (try it.next()) |entry| {
        if (entry.kind == .directory) {
            const name_copy = allocator.dupe(u8, entry.name) catch return VaultStoreError.OutOfMemory;
            names.append(allocator, name_copy) catch {
                allocator.free(name_copy);
                return VaultStoreError.OutOfMemory;
            };
        }
    }

    return names.toOwnedSlice(allocator) catch return VaultStoreError.OutOfMemory;
}

/// Counts the number of secrets for an identity.
pub fn countSecrets(
    data_dir: []const u8,
    identity: []const u8,
    allocator: std.mem.Allocator,
) !usize {
    const vault_dir = std.fmt.allocPrint(allocator, "{s}/vaults/{s}", .{ data_dir, identity }) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(vault_dir);

    var dir = std.fs.cwd().openDir(vault_dir, .{ .iterate = true }) catch {
        return 0;
    };
    defer dir.close();

    var count: usize = 0;
    var it = dir.iterate();
    while (try it.next()) |entry| {
        if (entry.kind == .directory) count += 1;
    }
    return count;
}

// ── Internal helpers ─────────────────────────────────────────────────────────

/// JSON shape for meta.json parsing.
const MetaJson = struct {
    version: u64,
    created_ns: i128,
    updated_ns: i128,
    size: usize,
};

fn secretExistsInternal(
    data_dir: []const u8,
    identity: []const u8,
    name: []const u8,
    allocator: std.mem.Allocator,
) bool {
    const share_path = std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}/share.bin", .{ data_dir, identity, name }) catch
        return false;
    defer allocator.free(share_path);

    std.fs.cwd().access(share_path, .{}) catch return false;
    return true;
}

fn writeSecretInner(
    secret_dir: []const u8,
    data: []const u8,
    version: u64,
    integrity_key: []const u8,
    existing_created_ns: ?i128,
    allocator: std.mem.Allocator,
) VaultStoreError!void {
    // Create directory
    std.fs.cwd().makePath(secret_dir) catch {
        return VaultStoreError.IoError;
    };

    // Write share.bin atomically
    const share_path = std.fmt.allocPrint(allocator, "{s}/share.bin", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(share_path);
    const share_tmp = std.fmt.allocPrint(allocator, "{s}/share.bin.tmp", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(share_tmp);

    try atomicWrite(share_path, share_tmp, data);

    // Write checksum.bin atomically
    const checksum = hmac.compute(integrity_key, data);
    const checksum_path = std.fmt.allocPrint(allocator, "{s}/checksum.bin", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(checksum_path);
    const checksum_tmp = std.fmt.allocPrint(allocator, "{s}/checksum.bin.tmp", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(checksum_tmp);

    try atomicWrite(checksum_path, checksum_tmp, &checksum);

    // Write meta.json atomically
    const now = std.time.nanoTimestamp();
    const created_ns = existing_created_ns orelse now;

    var meta_buf: [512]u8 = undefined;
    const meta_json = std.fmt.bufPrint(&meta_buf,
        \\{{"version":{d},"created_ns":{d},"updated_ns":{d},"size":{d}}}
    , .{ version, created_ns, now, data.len }) catch
        return VaultStoreError.IoError;

    const meta_path = std.fmt.allocPrint(allocator, "{s}/meta.json", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(meta_path);
    const meta_tmp = std.fmt.allocPrint(allocator, "{s}/meta.json.tmp", .{secret_dir}) catch
        return VaultStoreError.OutOfMemory;
    defer allocator.free(meta_tmp);

    try atomicWrite(meta_path, meta_tmp, meta_json);
}

fn atomicWrite(final_path: []const u8, tmp_path: []const u8, data: []const u8) VaultStoreError!void {
    // Write to temp file
    const tmp_file = std.fs.cwd().createFile(tmp_path, .{}) catch {
        return VaultStoreError.IoError;
    };
    defer tmp_file.close();
    tmp_file.writeAll(data) catch {
        return VaultStoreError.IoError;
    };

    // Rename atomically
    std.fs.cwd().rename(tmp_path, final_path) catch {
        return VaultStoreError.IoError;
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "validateSecretName: valid names" {
    try validateSecretName("default");
    try validateSecretName("my-secret");
    try validateSecretName("secret_123");
    try validateSecretName("a");
    try validateSecretName("ABC-xyz_09");
}

test "validateSecretName: empty name" {
    try std.testing.expectError(VaultStoreError.SecretNameRequired, validateSecretName(""));
}

test "validateSecretName: too long" {
    const long_name = "a" ** (MAX_SECRET_NAME_LEN + 1);
    try std.testing.expectError(VaultStoreError.SecretNameTooLong, validateSecretName(long_name));
}

test "validateSecretName: invalid characters" {
    try std.testing.expectError(VaultStoreError.SecretNameInvalid, validateSecretName("has space"));
    try std.testing.expectError(VaultStoreError.SecretNameInvalid, validateSecretName("has/slash"));
    try std.testing.expectError(VaultStoreError.SecretNameInvalid, validateSecretName("has.dot"));
    try std.testing.expectError(VaultStoreError.SecretNameInvalid, validateSecretName("has@at"));
}

test "writeSecret and readSecret round-trip" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789";
    const name = "my-secret";
    const data = "encrypted share data here";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, name, data, 1, key, allocator);

    const read_data = try readSecret(tmp_path, identity, name, key, allocator);
    defer allocator.free(read_data);

    try std.testing.expectEqualSlices(u8, data, read_data);
}

test "readSecret: integrity check detects tampering" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef";
    const name = "tamper-test";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, name, "original data", 1, key, allocator);

    // Tamper with the share file
    const share_path = try std.fmt.allocPrint(allocator, "{s}/vaults/{s}/{s}/share.bin", .{ tmp_path, identity, name });
    defer allocator.free(share_path);

    const file = try std.fs.cwd().openFile(share_path, .{ .mode = .write_only });
    defer file.close();
    try file.writeAll("tampered data!!");

    try std.testing.expectError(VaultStoreError.IntegrityCheckFailed, readSecret(tmp_path, identity, name, key, allocator));
}

test "readSecret: not found" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try std.testing.expectError(VaultStoreError.NotFound, readSecret(tmp_path, "nonexistent", "nope", "key!", allocator));
}

test "writeSecret: rejects empty identity" {
    const allocator = std.testing.allocator;
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try std.testing.expectError(VaultStoreError.IdentityRequired, writeSecret(tmp_path, "", "name", "data", 1, "key!", allocator));
}

test "writeSecret: rejects empty data" {
    const allocator = std.testing.allocator;
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try std.testing.expectError(VaultStoreError.SecretDataRequired, writeSecret(tmp_path, "id123", "name", "", 1, "key!", allocator));
}

test "writeSecret: anti-rollback rejects lower version" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "rollback0123456789abcdef";
    const name = "versioned";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, name, "v1 data", 5, key, allocator);

    // Same version should fail
    try std.testing.expectError(VaultStoreError.VersionConflict, writeSecret(tmp_path, identity, name, "v1 again", 5, key, allocator));

    // Lower version should fail
    try std.testing.expectError(VaultStoreError.VersionConflict, writeSecret(tmp_path, identity, name, "rollback", 3, key, allocator));

    // Higher version should succeed
    try writeSecret(tmp_path, identity, name, "v2 data", 6, key, allocator);

    const read_data = try readSecret(tmp_path, identity, name, key, allocator);
    defer allocator.free(read_data);
    try std.testing.expectEqualSlices(u8, "v2 data", read_data);
}

test "readMeta: returns correct metadata" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "meta0123456789abcdef";
    const name = "with-meta";
    const key = "integrity-key-32-bytes-long!!!!";
    const data = "some secret data";

    try writeSecret(tmp_path, identity, name, data, 42, key, allocator);

    const meta = try readMeta(tmp_path, identity, name, allocator);
    try std.testing.expectEqual(@as(u64, 42), meta.version);
    try std.testing.expectEqual(data.len, meta.size);
    try std.testing.expect(meta.created_ns > 0);
    try std.testing.expect(meta.updated_ns >= meta.created_ns);
}

test "deleteSecret: removes secret" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "delete0123456789abcdef";
    const name = "to-delete";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, name, "doomed", 1, key, allocator);

    const exists_before = try secretExists(tmp_path, identity, name, allocator);
    try std.testing.expect(exists_before);

    try deleteSecret(tmp_path, identity, name, allocator);

    const exists_after = try secretExists(tmp_path, identity, name, allocator);
    try std.testing.expect(!exists_after);
}

test "deleteSecret: not found" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    try std.testing.expectError(VaultStoreError.NotFound, deleteSecret(tmp_path, "ghost", "nope", allocator));
}

test "listSecrets: empty for new identity" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const names = try listSecrets(tmp_path, "nobody", allocator);
    defer allocator.free(names);

    try std.testing.expectEqual(@as(usize, 0), names.len);
}

test "listSecrets: returns all secret names" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "list0123456789abcdef";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, "alpha", "data-a", 1, key, allocator);
    try writeSecret(tmp_path, identity, "beta", "data-b", 1, key, allocator);
    try writeSecret(tmp_path, identity, "gamma", "data-c", 1, key, allocator);

    const names = try listSecrets(tmp_path, identity, allocator);
    defer {
        for (names) |n| allocator.free(n);
        allocator.free(names);
    }

    try std.testing.expectEqual(@as(usize, 3), names.len);

    // Check all three names are present (order is not guaranteed)
    var found_alpha = false;
    var found_beta = false;
    var found_gamma = false;
    for (names) |n| {
        if (std.mem.eql(u8, n, "alpha")) found_alpha = true;
        if (std.mem.eql(u8, n, "beta")) found_beta = true;
        if (std.mem.eql(u8, n, "gamma")) found_gamma = true;
    }
    try std.testing.expect(found_alpha);
    try std.testing.expect(found_beta);
    try std.testing.expect(found_gamma);
}

test "countSecrets: counts correctly" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "count0123456789abcdef";
    const key = "integrity-key-32-bytes-long!!!!";

    try std.testing.expectEqual(@as(usize, 0), try countSecrets(tmp_path, identity, allocator));

    try writeSecret(tmp_path, identity, "first", "data-1", 1, key, allocator);
    try std.testing.expectEqual(@as(usize, 1), try countSecrets(tmp_path, identity, allocator));

    try writeSecret(tmp_path, identity, "second", "data-2", 1, key, allocator);
    try std.testing.expectEqual(@as(usize, 2), try countSecrets(tmp_path, identity, allocator));
}

test "writeSecret: update preserves created_ns" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "preserve0123456789abcdef";
    const name = "preserve-ts";
    const key = "integrity-key-32-bytes-long!!!!";

    try writeSecret(tmp_path, identity, name, "v1", 1, key, allocator);
    const meta1 = try readMeta(tmp_path, identity, name, allocator);

    // Update with higher version
    try writeSecret(tmp_path, identity, name, "v2", 2, key, allocator);
    const meta2 = try readMeta(tmp_path, identity, name, allocator);

    // created_ns should be preserved
    try std.testing.expectEqual(meta1.created_ns, meta2.created_ns);
    // updated_ns should be >= the first
    try std.testing.expect(meta2.updated_ns >= meta1.updated_ns);
    // version should be updated
    try std.testing.expectEqual(@as(u64, 2), meta2.version);
}

test "secretExists: false for missing, true for existing" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    var tmp_dir = std.testing.tmpDir(.{});
    defer tmp_dir.cleanup();
    const tmp_path = try tmp_dir.dir.realpath(".", &tmp_dir_buf);

    const identity = "exists0123456789abcdef";
    const key = "integrity-key-32-bytes-long!!!!";

    try std.testing.expect(!try secretExists(tmp_path, identity, "nope", allocator));

    try writeSecret(tmp_path, identity, "yes", "data", 1, key, allocator);
    try std.testing.expect(try secretExists(tmp_path, identity, "yes", allocator));
}
