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

    // Derive integrity key from guardian server_secret (or use fallback)
    const integrity_key: []const u8 = if (ctx.guardian) |guardian|
        &guardian.server_secret
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

    // Base64 encode
    const encoded_len = std.base64.standard.Encoder.calcSize(share_data.len);
    const encoded = ctx.allocator.alloc(u8, encoded_len) catch {
        return response.internalError(writer);
    };
    defer ctx.allocator.free(encoded);
    _ = std.base64.standard.Encoder.encode(encoded, share_data);

    // Build response: {"share":"<base64>"}
    // We need to write it in parts to avoid a huge stack buffer
    try writer.writeAll("HTTP/1.1 200 OK\r\n");
    try writer.writeAll("Content-Type: application/json\r\n");
    const body_len = 11 + encoded.len + 2; // {"share":".."}
    try std.fmt.format(writer, "Content-Length: {d}\r\n", .{body_len});
    try writer.writeAll("Connection: close\r\n");
    try writer.writeAll("\r\n");
    try writer.writeAll("{\"share\":\"");
    try writer.writeAll(encoded);
    try writer.writeAll("\"}");

    log.info("served share for identity {s} ({d} bytes)", .{ identity, share_data.len });
}
