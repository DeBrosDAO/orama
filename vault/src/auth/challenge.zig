/// Challenge-response authentication.
///
/// Flow:
/// 1. Client sends identity hash
/// 2. Server returns a random challenge (32 bytes) + expiry timestamp
/// 3. Client signs challenge with their private key
/// 4. Server verifies signature against identity's public key
///
/// For MVP: HMAC-based challenge tokens (no public key crypto yet).
/// The challenge is an HMAC over (identity || timestamp || nonce) using a server secret.
/// Client must return the challenge within the expiry window.
///
/// Phase 3 adds Ed25519 signature verification.
const std = @import("std");
const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;

pub const CHALLENGE_SIZE = 32;
pub const CHALLENGE_EXPIRY_NS: i128 = 60 * std.time.ns_per_s; // 60 seconds

pub const Challenge = struct {
    /// Random challenge bytes
    nonce: [CHALLENGE_SIZE]u8,
    /// Timestamp when challenge was created (nanos since epoch)
    created_ns: i128,
    /// HMAC tag binding challenge to identity + timestamp
    tag: [HmacSha256.mac_length]u8,
};

pub const AuthError = error{
    ChallengeExpired,
    InvalidChallenge,
};

/// Generate a new challenge for the given identity.
pub fn generateChallenge(identity: []const u8, server_secret: [32]u8) Challenge {
    var nonce: [CHALLENGE_SIZE]u8 = undefined;
    std.crypto.random.bytes(&nonce);

    const now = std.time.nanoTimestamp();

    // HMAC(server_secret, identity || nonce || timestamp)
    var mac = HmacSha256.init(&server_secret);
    mac.update(identity);
    mac.update(&nonce);

    // Encode timestamp as bytes
    var ts_bytes: [16]u8 = undefined;
    std.mem.writeInt(i128, &ts_bytes, now, .little);
    mac.update(&ts_bytes);

    var tag: [HmacSha256.mac_length]u8 = undefined;
    mac.final(&tag);

    return Challenge{
        .nonce = nonce,
        .created_ns = now,
        .tag = tag,
    };
}

/// Verify a challenge response.
/// The client must return the exact nonce + timestamp + tag within the expiry window.
pub fn verifyChallenge(
    challenge: Challenge,
    identity: []const u8,
    server_secret: [32]u8,
) AuthError!void {
    // Check expiry
    const now = std.time.nanoTimestamp();
    const age = now - challenge.created_ns;
    if (age > CHALLENGE_EXPIRY_NS or age < 0) {
        return AuthError.ChallengeExpired;
    }

    // Recompute HMAC and verify
    var mac = HmacSha256.init(&server_secret);
    mac.update(identity);
    mac.update(&challenge.nonce);

    var ts_bytes: [16]u8 = undefined;
    std.mem.writeInt(i128, &ts_bytes, challenge.created_ns, .little);
    mac.update(&ts_bytes);

    var expected: [HmacSha256.mac_length]u8 = undefined;
    mac.final(&expected);

    // Constant-time comparison to prevent timing attacks
    if (!timingSafeEqual(&expected, &challenge.tag)) {
        return AuthError.InvalidChallenge;
    }
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

test "challenge: generate and verify" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    const identity = "abcdef1234";
    const challenge = generateChallenge(identity, secret);

    // Should verify successfully
    try verifyChallenge(challenge, identity, secret);
}

test "challenge: wrong identity fails" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    const challenge = generateChallenge("alice", secret);

    // Different identity should fail
    try std.testing.expectError(AuthError.InvalidChallenge, verifyChallenge(challenge, "bob", secret));
}

test "challenge: wrong secret fails" {
    var secret1: [32]u8 = undefined;
    var secret2: [32]u8 = undefined;
    std.crypto.random.bytes(&secret1);
    std.crypto.random.bytes(&secret2);

    const challenge = generateChallenge("alice", secret1);

    // Different server secret should fail
    try std.testing.expectError(AuthError.InvalidChallenge, verifyChallenge(challenge, "alice", secret2));
}

test "challenge: tampered nonce fails" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    var challenge = generateChallenge("alice", secret);
    challenge.nonce[0] ^= 0xFF; // tamper

    try std.testing.expectError(AuthError.InvalidChallenge, verifyChallenge(challenge, "alice", secret));
}

test "challenge: tampered timestamp fails" {
    var secret: [32]u8 = undefined;
    std.crypto.random.bytes(&secret);

    var challenge = generateChallenge("alice", secret);
    challenge.created_ns -= 1; // tamper

    try std.testing.expectError(AuthError.InvalidChallenge, verifyChallenge(challenge, "alice", secret));
}
