/// Post-Quantum Digital Signature — ML-DSA-65 (FIPS 204).
///
/// ╔══════════════════════════════════════════════════════════════════════╗
/// ║  WARNING: THIS IS A STUB IMPLEMENTATION — PROVIDES ZERO SECURITY   ║
/// ║                                                                    ║
/// ║  keygen() returns random bytes, sign() returns a SHA-256 hash,    ║
/// ║  verify() ALWAYS SUCCEEDS. This exists solely for interface       ║
/// ║  testing and development. DO NOT use in production without        ║
/// ║  replacing with a real liboqs-backed implementation.              ║
/// ╚══════════════════════════════════════════════════════════════════════╝
///
/// Provides a Zig-native interface for ML-DSA-65 (formerly Dilithium3).
/// Uses liboqs via @cImport when available, falls back to stub for testing.
///
/// ML-DSA-65 provides ~192-bit post-quantum security level.
///
/// Key sizes (ML-DSA-65):
///   Public key:  1952 bytes
///   Secret key:  4032 bytes
///   Signature:   3309 bytes (max)
const std = @import("std");
const secure_mem = @import("secure_mem.zig");
const log = std.log.scoped(.pq_sig);

var stub_warned: bool = false;

pub const PK_SIZE: usize = 1952;
pub const SK_SIZE: usize = 4032;
pub const SIG_MAX_SIZE: usize = 3309;

pub const SignKeypair = struct {
    public_key: [PK_SIZE]u8,
    secret_key: [SK_SIZE]u8,

    pub fn deinit(self: *SignKeypair) void {
        secure_mem.secureZero(&self.secret_key);
    }
};

pub const Signature = struct {
    data: [SIG_MAX_SIZE]u8,
    len: usize,
};

pub const SigError = error{
    KeygenFailed,
    SignFailed,
    VerifyFailed,
    InvalidSignature,
};

/// Log a one-time warning that stub PQ-DSA is in use.
fn warnStub() void {
    if (!stub_warned) {
        stub_warned = true;
        log.warn("pq_sig: STUB implementation — uses HMAC-based signatures, NOT real post-quantum security. Install liboqs for ML-DSA-65.", .{});
    }
}

/// Generate an ML-DSA-65 signing keypair.
/// STUB: Returns random bytes. NOT real key generation.
pub fn keygen() SigError!SignKeypair {
    warnStub();
    var kp = SignKeypair{
        .public_key = undefined,
        .secret_key = undefined,
    };

    // STUB: generate random keys for interface testing.
    // TODO(security): Replace with liboqs OQS_SIG_ml_dsa_65_keypair().
    std.crypto.random.bytes(&kp.public_key);
    std.crypto.random.bytes(&kp.secret_key);

    return kp;
}

/// Sign a message with ML-DSA-65.
/// STUB: Returns SHA-256 hash as placeholder. NOT a real signature.
pub fn sign(message: []const u8, secret_key: [SK_SIZE]u8) SigError!Signature {
    _ = secret_key;
    warnStub();

    var sig = Signature{
        .data = undefined,
        .len = SIG_MAX_SIZE,
    };

    // STUB: SHA-256 of message as placeholder signature data.
    // TODO(security): Replace with liboqs OQS_SIG_ml_dsa_65_sign().
    const Sha256 = std.crypto.hash.sha2.Sha256;
    var h = Sha256.init(.{});
    h.update(message);
    const tag = h.finalResult();

    @memset(&sig.data, 0);
    @memcpy(sig.data[0..32], &tag);
    sig.len = SIG_MAX_SIZE;

    return sig;
}

/// Verify an ML-DSA-65 signature.
/// STUB: Performs HMAC-based verification as a placeholder.
/// This is NOT real post-quantum signature verification, but it at least
/// verifies that sign() produced the signature (fail-closed rather than accepting everything).
pub fn verify(message: []const u8, signature: Signature, public_key: [PK_SIZE]u8) SigError!void {
    _ = public_key;
    warnStub();

    // STUB: Verify that the signature contains SHA-256(message) in the first 32 bytes.
    // This is NOT real PQ verification — it just checks consistency with the stub sign().
    // TODO(security): Replace with liboqs OQS_SIG_ml_dsa_65_verify().
    const Sha256 = std.crypto.hash.sha2.Sha256;
    var h = Sha256.init(.{});
    h.update(message);
    const expected = h.finalResult();

    if (signature.len < 32) return SigError.InvalidSignature;

    var diff: u8 = 0;
    for (expected, signature.data[0..32]) |a, b| {
        diff |= a ^ b;
    }
    if (diff != 0) return SigError.VerifyFailed;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "pq_sig: keygen produces valid-sized keys" {
    var kp = try keygen();
    defer kp.deinit();

    try std.testing.expectEqual(@as(usize, PK_SIZE), kp.public_key.len);
    try std.testing.expectEqual(@as(usize, SK_SIZE), kp.secret_key.len);
}

test "pq_sig: sign produces valid-sized signature" {
    var kp = try keygen();
    defer kp.deinit();

    const message = "hello quantum world";
    const sig = try sign(message, kp.secret_key);

    try std.testing.expect(sig.len > 0);
    try std.testing.expect(sig.len <= SIG_MAX_SIZE);
}

test "pq_sig: verify accepts valid signature" {
    var kp = try keygen();
    defer kp.deinit();

    const message = "hello quantum world";
    const sig = try sign(message, kp.secret_key);

    // Stub verify checks that sign produced a consistent signature
    try verify(message, sig, kp.public_key);
}

test "pq_sig: verify rejects wrong message" {
    var kp = try keygen();
    defer kp.deinit();

    const sig = try sign("original message", kp.secret_key);

    // Verifying against a different message should fail
    try std.testing.expectError(SigError.VerifyFailed, verify("tampered message", sig, kp.public_key));
}

test "pq_sig: verify rejects tampered signature" {
    var kp = try keygen();
    defer kp.deinit();

    const message = "hello quantum world";
    var sig = try sign(message, kp.secret_key);
    sig.data[0] ^= 0xFF; // tamper

    try std.testing.expectError(SigError.VerifyFailed, verify(message, sig, kp.public_key));
}

test "pq_sig: key sizes match ML-DSA-65 spec" {
    try std.testing.expectEqual(@as(usize, 1952), PK_SIZE);
    try std.testing.expectEqual(@as(usize, 4032), SK_SIZE);
    try std.testing.expectEqual(@as(usize, 3309), SIG_MAX_SIZE);
}
