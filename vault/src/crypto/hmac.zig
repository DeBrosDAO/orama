/// HMAC-SHA256 for integrity verification.
///
/// Used for file integrity checks on stored shares.
const std = @import("std");
const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;

pub const MAC_SIZE = 32;

/// Computes HMAC-SHA256 of data with the given key.
pub fn compute(key: []const u8, data: []const u8) [MAC_SIZE]u8 {
    var mac: [MAC_SIZE]u8 = undefined;
    HmacSha256.create(&mac, data, key);
    return mac;
}

/// Verifies an HMAC-SHA256 tag in constant time.
pub fn verify(key: []const u8, data: []const u8, expected: [MAC_SIZE]u8) bool {
    const computed = compute(key, data);
    return constantTimeEqual(&computed, &expected);
}

/// Constant-time comparison to prevent timing attacks.
fn constantTimeEqual(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    var diff: u8 = 0;
    for (a, b) |x, y| {
        diff |= x ^ y;
    }
    return diff == 0;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "compute: deterministic output" {
    const key = "test-key";
    const data = "hello world";
    const mac1 = compute(key, data);
    const mac2 = compute(key, data);
    try std.testing.expectEqualSlices(u8, &mac1, &mac2);
}

test "compute: different keys produce different MACs" {
    const data = "hello world";
    const mac1 = compute("key1", data);
    const mac2 = compute("key2", data);
    try std.testing.expect(!std.mem.eql(u8, &mac1, &mac2));
}

test "compute: different data produces different MACs" {
    const key = "test-key";
    const mac1 = compute(key, "data1");
    const mac2 = compute(key, "data2");
    try std.testing.expect(!std.mem.eql(u8, &mac1, &mac2));
}

test "verify: correct MAC passes" {
    const key = "test-key";
    const data = "hello world";
    const mac = compute(key, data);
    try std.testing.expect(verify(key, data, mac));
}

test "verify: wrong MAC fails" {
    const key = "test-key";
    const data = "hello world";
    var mac = compute(key, data);
    mac[0] ^= 0xFF;
    try std.testing.expect(!verify(key, data, mac));
}

test "verify: wrong key fails" {
    const key = "test-key";
    const data = "hello world";
    const mac = compute(key, data);
    try std.testing.expect(!verify("wrong-key", data, mac));
}
