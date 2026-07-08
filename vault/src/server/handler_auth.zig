/// POST /v1/vault/auth/challenge — Request an authentication challenge.
/// POST /v1/vault/auth/session  — Exchange verified challenge for a session token.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");
const log = @import("../log.zig");
const challenge_mod = @import("../auth/challenge.zig");
const session_mod = @import("../auth/session.zig");
const guardian_mod = @import("../guardian.zig");

const IDENTITY_HEX_LEN = 64;

/// POST /v1/vault/auth/challenge
/// Request body: {"identity":"<64 hex chars>"}
/// Response: {"nonce":"<hex>","created_ns":<number>,"tag":"<hex>"}
pub fn handleChallenge(writer: anytype, body: []const u8, ctx: *const router.RouteContext) !void {
    if (body.len == 0) {
        return response.badRequest(writer, "empty body");
    }

    const guardian = ctx.guardian orelse {
        return response.internalError(writer);
    };

    // Parse JSON
    const ChallengeReq = struct {
        identity: []const u8,
    };

    const parsed = std.json.parseFromSlice(ChallengeReq, ctx.allocator, body, .{}) catch {
        return response.badRequest(writer, "invalid JSON");
    };
    defer parsed.deinit();
    const identity = parsed.value.identity;

    // Validate identity
    if (identity.len != IDENTITY_HEX_LEN) {
        return response.badRequest(writer, "identity must be exactly 64 hex characters");
    }
    for (identity) |c| {
        if (!std.ascii.isHex(c)) {
            return response.badRequest(writer, "identity must be hex");
        }
    }

    // Generate challenge
    const ch = challenge_mod.generateChallenge(identity, guardian.server_secret);

    // Format response as JSON with hex strings
    var resp_buf: [1024]u8 = undefined;
    const nonce_hex = std.fmt.bytesToHex(ch.nonce, .lower);
    const tag_hex = std.fmt.bytesToHex(ch.tag, .lower);

    // created_ns is i128, format as integer
    const resp_body = std.fmt.bufPrint(&resp_buf,
        \\{{"nonce":"{s}","created_ns":{d},"tag":"{s}"}}
    , .{ &nonce_hex, ch.created_ns, &tag_hex }) catch {
        return response.internalError(writer);
    };

    try response.jsonOk(writer, resp_body);
}

/// POST /v1/vault/auth/session
/// Request body: {"identity":"<hex>","nonce":"<hex>","created_ns":<number>,"tag":"<hex>"}
/// Response: {"token":"<hex>","expiry_ns":<number>}
pub fn handleSession(writer: anytype, body: []const u8, ctx: *const router.RouteContext) !void {
    if (body.len == 0) {
        return response.badRequest(writer, "empty body");
    }

    const guardian = ctx.guardian orelse {
        return response.internalError(writer);
    };

    // Parse using manual extraction since we need hex-encoded byte arrays
    const identity = extractJsonString(body, "identity") orelse {
        return response.badRequest(writer, "missing identity field");
    };
    const nonce_hex = extractJsonString(body, "nonce") orelse {
        return response.badRequest(writer, "missing nonce field");
    };
    const tag_hex = extractJsonString(body, "tag") orelse {
        return response.badRequest(writer, "missing tag field");
    };
    const created_ns = extractJsonInt(body, "created_ns") orelse {
        return response.badRequest(writer, "missing created_ns field");
    };

    // Validate identity
    if (identity.len != IDENTITY_HEX_LEN) {
        return response.badRequest(writer, "identity must be exactly 64 hex characters");
    }

    // Decode nonce from hex
    if (nonce_hex.len != challenge_mod.CHALLENGE_SIZE * 2) {
        return response.badRequest(writer, "invalid nonce length");
    }
    var nonce: [challenge_mod.CHALLENGE_SIZE]u8 = undefined;
    _ = std.fmt.hexToBytes(&nonce, nonce_hex) catch {
        return response.badRequest(writer, "invalid nonce hex");
    };

    // Decode tag from hex
    const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;
    if (tag_hex.len != HmacSha256.mac_length * 2) {
        return response.badRequest(writer, "invalid tag length");
    }
    var tag: [HmacSha256.mac_length]u8 = undefined;
    _ = std.fmt.hexToBytes(&tag, tag_hex) catch {
        return response.badRequest(writer, "invalid tag hex");
    };

    // Reconstruct challenge
    const ch = challenge_mod.Challenge{
        .nonce = nonce,
        .created_ns = created_ns,
        .tag = tag,
    };

    // Verify challenge
    challenge_mod.verifyChallenge(ch, identity, guardian.server_secret) catch |err| {
        switch (err) {
            challenge_mod.AuthError.ChallengeExpired => return response.jsonError(writer, 401, "Unauthorized", "challenge expired"),
            challenge_mod.AuthError.InvalidChallenge => return response.jsonError(writer, 401, "Unauthorized", "invalid challenge"),
        }
    };

    // Issue session token
    const token = session_mod.issueToken(identity, guardian.server_secret);

    // Encode token fields as hex
    var resp_buf: [1024]u8 = undefined;
    const token_tag_hex = std.fmt.bytesToHex(token.tag, .lower);

    // The identity is already a hex string (the value the client authenticated
    // with); return it verbatim. It must equal the <identity_hex> segment of the
    // session token, which validateSessionToken re-hashes to verify the tag —
    // hex-encoding it again here produced a 128-char value that never validated.
    const id_slice = token.identity[0..token.identity_len];

    const resp_body = std.fmt.bufPrint(&resp_buf,
        \\{{"identity":"{s}","expiry_ns":{d},"tag":"{s}"}}
    , .{ id_slice, token.expiry_ns, &token_tag_hex }) catch {
        return response.internalError(writer);
    };

    try response.jsonOk(writer, resp_body);
}

/// Validate a session token from the X-Session-Token header.
/// Returns the identity slice on success, or null if no token / invalid token.
/// If a token is present but invalid, returns error to signal rejection.
pub fn validateSessionToken(
    header_value: ?[]const u8,
    server_secret: [32]u8,
) ?[]const u8 {
    const token_str = header_value orelse return null;
    if (token_str.len == 0) return null;

    // Token format: <identity_hex>:<expiry_ns>:<tag_hex>
    var parts = std.mem.splitScalar(u8, token_str, ':');
    const id_hex = parts.next() orelse return null;
    const expiry_str = parts.next() orelse return null;
    const tag_hex_str = parts.next() orelse return null;

    if (id_hex.len == 0 or id_hex.len > 64) return null;

    const expiry_ns: i128 = std.fmt.parseInt(i128, expiry_str, 10) catch return null;

    const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;
    if (tag_hex_str.len != HmacSha256.mac_length * 2) return null;
    var tag: [HmacSha256.mac_length]u8 = undefined;
    _ = std.fmt.hexToBytes(&tag, tag_hex_str) catch return null;

    // Reconstruct token struct
    var id_buf: [64]u8 = .{0} ** 64;
    const copy_len = @min(id_hex.len, 64);
    @memcpy(id_buf[0..copy_len], id_hex[0..copy_len]);

    const token = session_mod.SessionToken{
        .identity = id_buf,
        .identity_len = @intCast(copy_len),
        .expiry_ns = expiry_ns,
        .tag = tag,
    };

    _ = session_mod.verifyToken(token, server_secret) catch return null;
    return id_hex;
}

// ── Manual JSON extraction (needed for hex-encoded byte arrays) ──────────────

fn extractJsonString(json: []const u8, key: []const u8) ?[]const u8 {
    var search_buf: [256]u8 = undefined;
    const search = std.fmt.bufPrint(&search_buf, "\"{s}\":\"", .{key}) catch return null;

    // Also try with space after colon
    var search_buf2: [256]u8 = undefined;
    const search2 = std.fmt.bufPrint(&search_buf2, "\"{s}\": \"", .{key}) catch return null;

    const start_idx = std.mem.indexOf(u8, json, search) orelse
        (std.mem.indexOf(u8, json, search2) orelse return null);

    const search_len = if (std.mem.indexOf(u8, json, search) != null) search.len else search2.len;
    const value_start = start_idx + search_len;
    if (value_start >= json.len) return null;

    var i = value_start;
    while (i < json.len) : (i += 1) {
        if (json[i] == '"' and (i == value_start or json[i - 1] != '\\')) {
            return json[value_start..i];
        }
    }
    return null;
}

fn extractJsonInt(json: []const u8, key: []const u8) ?i128 {
    var search_buf: [256]u8 = undefined;
    const search = std.fmt.bufPrint(&search_buf, "\"{s}\":", .{key}) catch return null;

    const start_idx = std.mem.indexOf(u8, json, search) orelse return null;
    var pos = start_idx + search.len;

    // Skip whitespace
    while (pos < json.len and (json[pos] == ' ' or json[pos] == '\t')) : (pos += 1) {}
    if (pos >= json.len) return null;

    // Handle negative numbers
    var negative = false;
    if (json[pos] == '-') {
        negative = true;
        pos += 1;
    }

    // Collect digits
    const digit_start = pos;
    while (pos < json.len and json[pos] >= '0' and json[pos] <= '9') : (pos += 1) {}
    if (pos == digit_start) return null;

    const value = std.fmt.parseInt(i128, json[digit_start..pos], 10) catch return null;
    return if (negative) -value else value;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "extractJsonString: basic" {
    const json = "{\"identity\":\"abcd1234\"}";
    const val = extractJsonString(json, "identity");
    try std.testing.expect(val != null);
    try std.testing.expectEqualSlices(u8, "abcd1234", val.?);
}

test "extractJsonString: missing key" {
    const json = "{\"identity\":\"abcd1234\"}";
    try std.testing.expect(extractJsonString(json, "nonce") == null);
}

test "extractJsonInt: basic" {
    const json = "{\"created_ns\":12345}";
    const val = extractJsonInt(json, "created_ns");
    try std.testing.expect(val != null);
    try std.testing.expectEqual(@as(i128, 12345), val.?);
}

test "extractJsonInt: negative" {
    const json = "{\"value\":-42}";
    const val = extractJsonInt(json, "value");
    try std.testing.expect(val != null);
    try std.testing.expectEqual(@as(i128, -42), val.?);
}
