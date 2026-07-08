/// POST /v1/vault/push — Store a share for a user.
///
/// Expects JSON body: {"identity":"<hex>","share":"<base64>","index":<n>,"version":<n>}
/// Stores the share to disk with HMAC integrity via file_store.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");
const log = @import("../log.zig");
const file_store = @import("../storage/file_store.zig");
const handler_auth = @import("handler_auth.zig");
const ownership = @import("../auth/ownership.zig");

/// Maximum request body size (1 MiB). Prevents memory exhaustion from oversized payloads.
const MAX_BODY_SIZE = 1024 * 1024;
/// Maximum decoded share size (512 KiB). Encrypted Shamir shares should be small.
const MAX_SHARE_SIZE = 512 * 1024;
/// Identity hash must be exactly 64 hex chars (SHA-256 output).
const IDENTITY_HEX_LEN = 64;

pub fn handle(writer: anytype, body: []const u8, ctx: *const router.RouteContext, session_token: ?[]const u8) !void {
    if (body.len == 0) {
        return response.badRequest(writer, "empty body");
    }

    // Reject oversized request bodies before parsing
    if (body.len > MAX_BODY_SIZE) {
        return response.badRequest(writer, "request body too large");
    }

    // Require session token when guardian is configured
    if (ctx.guardian) |guardian| {
        const tok = session_token orelse {
            return response.jsonError(writer, 401, "Unauthorized", "session token required");
        };
        if (handler_auth.validateSessionToken(tok, guardian.server_secret) == null) {
            return response.jsonError(writer, 401, "Unauthorized", "invalid session token");
        }
    }

    // Parse JSON
    const PushBody = struct {
        identity: []const u8,
        share: []const u8,
        version: u64,
        threshold: u8 = 0,
        pubkey: []const u8 = "",
        signature: []const u8 = "",
    };

    const parsed = std.json.parseFromSlice(PushBody, ctx.allocator, body, .{}) catch {
        return response.badRequest(writer, "invalid JSON");
    };
    defer parsed.deinit();

    const identity = parsed.value.identity;
    const share_b64 = parsed.value.share;
    const version = parsed.value.version;
    const threshold = parsed.value.threshold;

    // Validate identity is exactly 64 hex chars (SHA-256 hash)
    if (identity.len != IDENTITY_HEX_LEN) {
        return response.badRequest(writer, "identity must be exactly 64 hex characters");
    }
    for (identity) |c| {
        if (!std.ascii.isHex(c)) {
            return response.badRequest(writer, "identity must be hex");
        }
    }

    // Ownership proof: identity = SHA-256(pubkey) and a valid Ed25519 signature
    // over the push message. This is what stops anyone who merely knows the
    // identity from overwriting it. Enforced whenever a guardian is configured.
    if (ctx.guardian != null) {
        if (!ownership.verifyPush(identity, version, parsed.value.pubkey, parsed.value.signature)) {
            return response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature");
        }
    }

    // Decode base64 share
    const decoded_len = std.base64.standard.Decoder.calcSizeForSlice(share_b64) catch {
        return response.badRequest(writer, "invalid base64 in share");
    };

    // Reject oversized shares before allocating
    if (decoded_len > MAX_SHARE_SIZE) {
        return response.badRequest(writer, "share data too large");
    }

    const share_data = ctx.allocator.alloc(u8, decoded_len) catch {
        return response.internalError(writer);
    };
    defer ctx.allocator.free(share_data);

    std.base64.standard.Decoder.decode(share_data, share_b64) catch {
        return response.badRequest(writer, "invalid base64 in share");
    };

    if (share_data.len == 0) {
        return response.badRequest(writer, "share data is empty");
    }

    // Anti-rollback: reject shares with version <= current stored version.
    // Returns 409 Conflict (not 400) so clients can distinguish a stale version
    // from a malformed request and re-read + bump before retrying.
    if (file_store.readMeta(ctx.data_dir, identity, ctx.allocator)) |cur_meta| {
        if (version <= cur_meta.version) {
            log.warn("rejected rollback for {s}: version {d} <= current {d}", .{ identity, version, cur_meta.version });
            return response.jsonError(writer, 409, "Conflict", "version must be greater than current stored version");
        }
    } else |_| {}

    // Use the guardian's persistent at-rest integrity key (survives restarts),
    // or a fixed fallback when running without a guardian (e.g. tests).
    const integrity_key: []const u8 = if (ctx.guardian) |guardian|
        &guardian.integrity_key
    else
        "vault-default-integrity-key!!!!!";

    // Write share data to storage with HMAC integrity
    file_store.writeShare(ctx.data_dir, identity, share_data, integrity_key, ctx.allocator) catch |err| {
        log.err("failed to write share for {s}: {}", .{ identity, err });
        return response.internalError(writer);
    };

    // Write metadata (version + threshold) LAST, as the commit marker — the
    // share only becomes "present" once meta.json lands, so a crash mid-write
    // leaves an incomplete (ignored) share rather than a corrupt one.
    file_store.writeMeta(ctx.data_dir, identity, .{ .version = version, .threshold = threshold }, ctx.allocator) catch |err| {
        log.err("failed to write meta for {s}: {}", .{ identity, err });
        return response.internalError(writer);
    };

    log.info("stored share for identity {s} ({d} bytes, version {d})", .{ identity, share_data.len, version });
    try response.jsonOk(writer, "{\"status\":\"stored\"}");
}

/// Read the current stored version for an identity. Returns null if no version file exists.
fn readCurrentVersion(data_dir: []const u8, identity: []const u8, allocator: std.mem.Allocator) ?u64 {
    var path_buf: [4096]u8 = undefined;
    const version_path = std.fmt.bufPrint(&path_buf, "{s}/shares/{s}/version", .{ data_dir, identity }) catch return null;

    const version_data = std.fs.cwd().readFileAlloc(allocator, version_path, 32) catch return null;
    defer allocator.free(version_data);

    return std.fmt.parseInt(u64, version_data, 10) catch null;
}

/// Write version counter atomically to: <data_dir>/shares/<identity>/version
fn writeVersionFile(data_dir: []const u8, identity: []const u8, version: u64) !void {
    var path_buf: [4096]u8 = undefined;
    const tmp_path = std.fmt.bufPrint(&path_buf, "{s}/shares/{s}/version.tmp", .{ data_dir, identity }) catch return error.PathTooLong;

    var path_buf2: [4096]u8 = undefined;
    const final_path = std.fmt.bufPrint(&path_buf2, "{s}/shares/{s}/version", .{ data_dir, identity }) catch return error.PathTooLong;

    var ver_buf: [20]u8 = undefined; // max u64 is 20 digits
    const ver_str = std.fmt.bufPrint(&ver_buf, "{d}", .{version}) catch return error.PathTooLong;

    const file = try std.fs.cwd().createFile(tmp_path, .{});
    defer file.close();
    try file.writeAll(ver_str);

    std.fs.cwd().rename(tmp_path, final_path) catch |rename_err| {
        std.fs.cwd().deleteFile(tmp_path) catch {};
        return rename_err;
    };
}
