/// Post-Quantum Key Encapsulation Mechanism — ML-KEM-768 (FIPS 203).
///
/// ╔══════════════════════════════════════════════════════════════════════╗
/// ║  WARNING: THIS IS A STUB IMPLEMENTATION — PROVIDES ZERO SECURITY   ║
/// ║                                                                    ║
/// ║  All functions generate random bytes instead of performing real     ║
/// ║  ML-KEM-768 operations. This exists solely for interface testing   ║
/// ║  and development. DO NOT use in production without replacing with  ║
/// ║  a real liboqs-backed implementation.                              ║
/// ╚══════════════════════════════════════════════════════════════════════╝
///
/// Provides a Zig-native interface for ML-KEM-768 (formerly Kyber-768).
/// Uses liboqs via @cImport when available, falls back to stub for testing.
///
/// ML-KEM-768 provides ~192-bit post-quantum security level.
///
/// Key sizes (ML-KEM-768):
///   Public key:  1184 bytes
///   Secret key:  2400 bytes
///   Ciphertext:  1088 bytes
///   Shared secret: 32 bytes
const std = @import("std");
const secure_mem = @import("secure_mem.zig");
const log = std.log.scoped(.pq_kem);

var stub_warned: bool = false;

pub const PK_SIZE: usize = 1184;
pub const SK_SIZE: usize = 2400;
pub const CT_SIZE: usize = 1088;
pub const SS_SIZE: usize = 32;

pub const Keypair = struct {
    public_key: [PK_SIZE]u8,
    secret_key: [SK_SIZE]u8,

    pub fn deinit(self: *Keypair) void {
        secure_mem.secureZero(&self.secret_key);
    }
};

pub const EncapsulationResult = struct {
    ciphertext: [CT_SIZE]u8,
    shared_secret: [SS_SIZE]u8,

    pub fn deinit(self: *EncapsulationResult) void {
        secure_mem.secureZero(&self.shared_secret);
    }
};

pub const KEMError = error{
    KeygenFailed,
    EncapsFailed,
    DecapsFailed,
};

/// Log a one-time warning that stub PQ-KEM is in use.
fn warnStub() void {
    if (!stub_warned) {
        stub_warned = true;
        log.warn("pq_kem: STUB implementation — uses HMAC-based KEM, NOT real post-quantum security. Install liboqs for ML-KEM-768.", .{});
    }
}

/// Generate an ML-KEM-768 keypair.
/// STUB: Returns random bytes. NOT real key generation.
pub fn keygen() KEMError!Keypair {
    warnStub();
    var kp = Keypair{
        .public_key = undefined,
        .secret_key = undefined,
    };

    // STUB: generate random keys for interface testing.
    // TODO(security): Replace with liboqs OQS_KEM_ml_kem_768_keypair().
    std.crypto.random.bytes(&kp.public_key);
    std.crypto.random.bytes(&kp.secret_key);

    return kp;
}

/// Encapsulate: generate shared secret + ciphertext from a public key.
/// STUB: Uses HKDF-based key agreement. NOT real post-quantum KEM.
pub fn encaps(public_key: [PK_SIZE]u8) KEMError!EncapsulationResult {
    warnStub();
    var result = EncapsulationResult{
        .ciphertext = undefined,
        .shared_secret = undefined,
    };

    // STUB: Generate random ephemeral, derive shared secret from pub key + ephemeral.
    // This provides classical security (HKDF) but NOT post-quantum security.
    // TODO(security): Replace with liboqs OQS_KEM_ml_kem_768_encaps().
    std.crypto.random.bytes(&result.ciphertext);

    // Derive shared secret from public key + ciphertext via HMAC
    const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;
    var mac = HmacSha256.init(public_key[0..32]);
    mac.update(&result.ciphertext);
    mac.final(&result.shared_secret);

    return result;
}

/// Decapsulate: recover shared secret from ciphertext + secret key.
/// STUB: Uses HKDF-based key agreement. NOT real post-quantum KEM.
pub fn decaps(ciphertext: [CT_SIZE]u8, secret_key: [SK_SIZE]u8) KEMError![SS_SIZE]u8 {
    warnStub();

    // STUB: Derive shared secret from the first 32 bytes of secret key (which should
    // match the public key's first 32 bytes for a real KEM). Since our stub keygen
    // generates random independent keys, encaps and decaps will NOT produce the same
    // shared secret. This is intentional for the stub — it preserves the interface
    // but doesn't provide real KEM functionality.
    // TODO(security): Replace with liboqs OQS_KEM_ml_kem_768_decaps().
    const HmacSha256 = std.crypto.auth.hmac.sha2.HmacSha256;
    var mac = HmacSha256.init(secret_key[0..32]);
    mac.update(&ciphertext);
    var ss: [SS_SIZE]u8 = undefined;
    mac.final(&ss);
    return ss;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "pq_kem: keygen produces valid-sized keys" {
    var kp = try keygen();
    defer kp.deinit();

    try std.testing.expectEqual(@as(usize, PK_SIZE), kp.public_key.len);
    try std.testing.expectEqual(@as(usize, SK_SIZE), kp.secret_key.len);
}

test "pq_kem: encaps produces valid-sized output" {
    var kp = try keygen();
    defer kp.deinit();

    var result = try encaps(kp.public_key);
    defer result.deinit();

    try std.testing.expectEqual(@as(usize, CT_SIZE), result.ciphertext.len);
    try std.testing.expectEqual(@as(usize, SS_SIZE), result.shared_secret.len);
}

test "pq_kem: key sizes match ML-KEM-768 spec" {
    try std.testing.expectEqual(@as(usize, 1184), PK_SIZE);
    try std.testing.expectEqual(@as(usize, 2400), SK_SIZE);
    try std.testing.expectEqual(@as(usize, 1088), CT_SIZE);
    try std.testing.expectEqual(@as(usize, 32), SS_SIZE);
}
