/// POST /v1/vault/pull — Retrieve a share for a user.
///
/// Expects JSON body: {"identity":"<hex>"}
/// Returns: {"share":"<base64>"} or 404 if not found.
/// Uses file_store.readShare() for HMAC integrity verification.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");
const log = @import("../log.zig");
const file_store = @import("../storage/file_store.zig");
const handler_auth = @import("handler_auth.zig");
const ownership = @import("../auth/ownership.zig");

/// Maximum request body size for pull requests. Only contains {"identity":"<64 hex>"}.
const MAX_BODY_SIZE = 4096;

pub fn handle(writer: anytype, body: []const u8, ctx: *const router.RouteContext, session_token: ?[]const u8) !void {
    if (body.len == 0) {
        return response.badRequest(writer, "empty body");
    }

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
    const PullBody = struct {
        identity: []const u8,
        pubkey: []const u8 = "",
        signature: []const u8 = "",
        timestamp: i64 = 0,
    };

    const parsed = std.json.parseFromSlice(PullBody, ctx.allocator, body, .{}) catch {
        return response.badRequest(writer, "invalid JSON");
    };
    defer parsed.deinit();

    const identity = parsed.value.identity;

    // Validate identity is exactly 64 hex chars (SHA-256 hash)
    if (identity.len != 64) {
        return response.badRequest(writer, "identity must be exactly 64 hex characters");
    }
    for (identity) |c| {
        if (!std.ascii.isHex(c)) {
            return response.badRequest(writer, "identity must be hex");
        }
    }

    // Ownership proof: identity = SHA-256(pubkey) and a fresh, valid Ed25519
    // signature over the pull message. This closes the password-oracle — anyone
    // who only knows the identity cannot read even the encrypted blob.
    if (ctx.guardian != null) {
        const now = std.time.timestamp();
        if (!ownership.verifyPull(identity, parsed.value.timestamp, now, parsed.value.pubkey, parsed.value.signature)) {
            return response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature");
        }
    }

    // Use the guardian's persistent at-rest integrity key (survives restarts),
    // or a fixed fallback when running without a guardian (e.g. tests).
    const integrity_key: []const u8 = if (ctx.guardian) |guardian|
        &guardian.integrity_key
    else
        "vault-default-integrity-key!!!!!";

    // Read share from storage with HMAC integrity verification
    const share_data = file_store.readShare(ctx.data_dir, identity, integrity_key, ctx.allocator) catch |err| {
        if (err == file_store.StoreError.IoError) {
            // Check if share simply doesn't exist
            const exists = file_store.shareExists(ctx.data_dir, identity, ctx.allocator) catch false;
            if (!exists) {
                return response.jsonError(writer, 404, "Not Found", "share not found");
            }
        }
        if (err == file_store.StoreError.IntegrityCheckFailed) {
            log.err("integrity check failed for {s}", .{identity});
            return response.internalError(writer);
        }
        log.err("failed to read share for {s}: {}", .{ identity, err });
        return response.internalError(writer);
    };
    defer ctx.allocator.free(share_data);

    // Read reconstruction metadata (version + threshold) so the gateway can
    // select a version-consistent read set using the ORIGINAL threshold.
    const meta: file_store.V1Meta = file_store.readMeta(ctx.data_dir, identity, ctx.allocator) catch .{ .version = 0, .threshold = 0 };

    // Base64 encode
    const encoded_len = std.base64.standard.Encoder.calcSize(share_data.len);
    const encoded = ctx.allocator.alloc(u8, encoded_len) catch {
        return response.internalError(writer);
    };
    defer ctx.allocator.free(encoded);
    _ = std.base64.standard.Encoder.encode(encoded, share_data);

    // Build response: {"share":"<base64>","version":<n>,"threshold":<k>}
    // Written in parts to avoid a large stack buffer for the share payload.
    const prefix = "{\"share\":\"";
    var suffix_buf: [96]u8 = undefined;
    const suffix = std.fmt.bufPrint(&suffix_buf, "\",\"version\":{d},\"threshold\":{d}}}", .{ meta.version, meta.threshold }) catch {
        return response.internalError(writer);
    };
    const body_len = prefix.len + encoded.len + suffix.len;

    try writer.writeAll("HTTP/1.1 200 OK\r\n");
    try writer.writeAll("Content-Type: application/json\r\n");
    try std.fmt.format(writer, "Content-Length: {d}\r\n", .{body_len});
    try writer.writeAll("Connection: close\r\n");
    try writer.writeAll("\r\n");
    try writer.writeAll(prefix);
    try writer.writeAll(encoded);
    try writer.writeAll(suffix);

    log.info("served share for identity {s} ({d} bytes, v{d})", .{ identity, share_data.len, meta.version });
}
