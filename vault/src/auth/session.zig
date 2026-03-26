/// Session token management.
///
/// After successful challenge-response auth, the server issues an HMAC-based
/// session token. Clients include this token in subsequent requests.
///
/// Token format: base64(identity_hash || expiry_timestamp || hmac_tag)
/// The HMAC binds the identity and expiry to the server secret.
const std = @import("std");
const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;

pub const SESSION_EXPIRY_NS: i128 = 3600 * std.time.ns_per_s; // 1 hour

pub const SessionToken = struct {
    identity: [64]u8, // hex-encoded identity hash (padded)
    identity_len: u8,
    expiry_ns: i128,
    tag: [HmacSha256.mac_length]u8,
};

pub const SessionError = error{
    SessionExpired,
    InvalidSession,
};

/// Issue a session token for the given identity.
pub fn issueToken(identity: []const u8, server_secret: [32]u8) SessionToken {
    const now = std.time.nanoTimestamp();
    const expiry = now + SESSION_EXPIRY_NS;

    var id_buf: [64]u8 = .{0} ** 64;
    const copy_len = @min(identity.len, 64);
    @memcpy(id_buf[0..copy_len], identity[0..copy_len]);

    var mac = HmacSha256.init(&server_secret);
    mac.update(&id_buf);
    var expiry_bytes: [16]u8 = undefined;
    std.mem.writeInt(i128, &expiry_bytes, expiry, .little);
    mac.update(&expiry_bytes);

    var tag: [HmacSha256.mac_length]u8 = undefined;
    mac.final(&tag);

    return SessionToken{
        .identity = id_buf,
        .identity_len = @intCast(copy_len),
        .expiry_ns = expiry,
        .tag = tag,
    };
}

/// Verify a session token.
pub fn verifyToken(token: SessionToken, server_secret: [32]u8) SessionError![]const u8 {
    // Check expiry
    const now = std.time.nanoTimestamp();
    if (now > token.expiry_ns) {
        return SessionError.SessionExpired;
    }

    // Recompute HMAC
    var mac = HmacSha256.init(&server_secret);
    mac.update(&token.identity);
    var expiry_bytes: [16]u8 = undefined;
    std.mem.writeInt(i128, &expiry_bytes, token.expiry_ns, .little);
    mac.update(&expiry_bytes);

    var expected: [HmacSha256.mac_length]u8 = undefined;
    mac.final(&expected);

    // Constant-time comparison to prevent timing attacks
    if (!timingSafeEqual(&expected, &token.tag)) {
        return SessionError.InvalidSession;
    }

    return token.identity[0..token.identity_len];
}

/// Constant-time byte comparison to prevent timing side-channel attacks.
fn timingSafeEqual(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    var diff: u8 = 0;
    for (a, b) |x, y| {
        diff |= x ^ y;
    }
    return diff == 0;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "session: issue and verify" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    const token = issueToken("abcdef1234", secret);
    const identity = try verifyToken(token, secret);
    try std.testing.expectEqualSlices(u8, "abcdef1234", identity);
}

test "session: wrong secret fails" {
    var secret1: [32]u8 = undefined;
    var secret2: [32]u8 = undefined;
    std.crypto.random.bytes(&secret1);
    std.crypto.random.bytes(&secret2);

    const token = issueToken("alice", secret1);
    try std.testing.expectError(SessionError.InvalidSession, verifyToken(token, secret2));
}

test "session: tampered identity fails" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    var token = issueToken("alice", secret);
    token.identity[0] = 'X'; // tamper

    try std.testing.expectError(SessionError.InvalidSession, verifyToken(token, secret));
}

test "session: tampered expiry fails" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    var token = issueToken("alice", secret);
    token.expiry_ns += 1; // tamper

    try std.testing.expectError(SessionError.InvalidSession, verifyToken(token, secret));
}
