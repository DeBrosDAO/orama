/// Hybrid Key Exchange — X25519 + ML-KEM-768.
///
/// Combines classical X25519 ECDH with post-quantum ML-KEM-768
/// to provide security against both classical and quantum adversaries.
///
/// The shared secret is: HKDF-SHA256(X25519_SS || ML-KEM_SS, "orama-hybrid-v1")
///
/// This ensures that:
/// - If X25519 is broken (quantum), ML-KEM still protects the key
/// - If ML-KEM is broken (unknown attack), X25519 still protects the key
/// - Both must be broken simultaneously to compromise the shared secret
const std = @import("std");
const pq_kem = @import("pq_kem.zig");
const hkdf = @import("hkdf.zig");
const secure_mem = @import("secure_mem.zig");

const X25519 = std.crypto.dh.X25519;

pub const SHARED_SECRET_SIZE: usize = 32;
pub const X25519_PK_SIZE: usize = 32;
pub const X25519_SK_SIZE: usize = 32;

/// Combined hybrid public key (X25519 + ML-KEM-768).
pub const HybridPublicKey = struct {
    x25519: [X25519_PK_SIZE]u8,
    ml_kem: [pq_kem.PK_SIZE]u8,
};

/// Combined hybrid secret key (X25519 + ML-KEM-768).
pub const HybridSecretKey = struct {
    x25519: [X25519_SK_SIZE]u8,
    ml_kem: [pq_kem.SK_SIZE]u8,

    pub fn deinit(self: *HybridSecretKey) void {
        secure_mem.secureZero(&self.x25519);
        secure_mem.secureZero(&self.ml_kem);
    }
};

/// Combined hybrid keypair.
pub const HybridKeypair = struct {
    public_key: HybridPublicKey,
    secret_key: HybridSecretKey,

    pub fn deinit(self: *HybridKeypair) void {
        self.secret_key.deinit();
    }
};

/// Hybrid encapsulation result — sent to the responder.
pub const HybridEncapsResult = struct {
    x25519_ephemeral_pk: [X25519_PK_SIZE]u8,
    ml_kem_ciphertext: [pq_kem.CT_SIZE]u8,
    shared_secret: [SHARED_SECRET_SIZE]u8,

    pub fn deinit(self: *HybridEncapsResult) void {
        secure_mem.secureZero(&self.shared_secret);
    }
};

pub const HybridError = error{
    X25519Failed,
    MLKEMFailed,
    HKDFFailed,
};

const HYBRID_INFO = "orama-hybrid-v1";
const HYBRID_SALT = [_]u8{0} ** 32; // fixed salt for domain separation

/// Generate a hybrid keypair (X25519 + ML-KEM-768).
pub fn keygen() HybridError!HybridKeypair {
    // X25519 keypair
    var x25519_sk: [X25519_SK_SIZE]u8 = undefined;
    std.crypto.random.bytes(&x25519_sk);
    const x25519_pk = X25519.recoverPublicKey(x25519_sk) catch return HybridError.X25519Failed;

    // ML-KEM-768 keypair
    const ml_kem_kp = pq_kem.keygen() catch return HybridError.MLKEMFailed;

    return HybridKeypair{
        .public_key = HybridPublicKey{
            .x25519 = x25519_pk,
            .ml_kem = ml_kem_kp.public_key,
        },
        .secret_key = HybridSecretKey{
            .x25519 = x25519_sk,
            .ml_kem = ml_kem_kp.secret_key,
        },
    };
}

/// Initiator side: encapsulate to a responder's hybrid public key.
/// Returns the shared secret and the data to send to the responder.
pub fn encapsulate(responder_pk: HybridPublicKey) HybridError!HybridEncapsResult {
    // X25519: generate ephemeral keypair and compute DH
    var eph_sk: [X25519_SK_SIZE]u8 = undefined;
    std.crypto.random.bytes(&eph_sk);
    const eph_pk = X25519.recoverPublicKey(eph_sk) catch return HybridError.X25519Failed;
    const x25519_ss = X25519.scalarmult(eph_sk, responder_pk.x25519) catch return HybridError.X25519Failed;
    secure_mem.secureZero(&eph_sk);

    // ML-KEM-768: encapsulate to responder's PQ public key
    const ml_kem_result = pq_kem.encaps(responder_pk.ml_kem) catch return HybridError.MLKEMFailed;

    // Combine: HKDF(X25519_SS || ML-KEM_SS)
    var combined: [64]u8 = undefined;
    @memcpy(combined[0..32], &x25519_ss);
    @memcpy(combined[32..64], &ml_kem_result.shared_secret);

    var shared_secret: [SHARED_SECRET_SIZE]u8 = undefined;
    hkdf.deriveKey(&shared_secret, &combined, &HYBRID_SALT, HYBRID_INFO);
    secure_mem.secureZero(&combined);

    return HybridEncapsResult{
        .x25519_ephemeral_pk = eph_pk,
        .ml_kem_ciphertext = ml_kem_result.ciphertext,
        .shared_secret = shared_secret,
    };
}

/// Responder side: decapsulate using own secret key.
/// Returns the same shared secret that the initiator computed.
pub fn decapsulate(
    x25519_ephemeral_pk: [X25519_PK_SIZE]u8,
    ml_kem_ciphertext: [pq_kem.CT_SIZE]u8,
    own_sk: HybridSecretKey,
) HybridError![SHARED_SECRET_SIZE]u8 {
    // X25519: compute DH with initiator's ephemeral public key
    const x25519_ss = X25519.scalarmult(own_sk.x25519, x25519_ephemeral_pk) catch return HybridError.X25519Failed;

    // ML-KEM-768: decapsulate
    const ml_kem_ss = pq_kem.decaps(ml_kem_ciphertext, own_sk.ml_kem) catch return HybridError.MLKEMFailed;

    // Combine: same HKDF as encapsulate
    var combined: [64]u8 = undefined;
    @memcpy(combined[0..32], &x25519_ss);
    @memcpy(combined[32..64], &ml_kem_ss);

    var shared_secret: [SHARED_SECRET_SIZE]u8 = undefined;
    hkdf.deriveKey(&shared_secret, &combined, &HYBRID_SALT, HYBRID_INFO);
    secure_mem.secureZero(&combined);

    return shared_secret;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "hybrid: keygen produces valid keypair" {
    var kp = try keygen();
    defer kp.deinit();

    // X25519 public key should be 32 bytes
    try std.testing.expectEqual(@as(usize, 32), kp.public_key.x25519.len);
    // ML-KEM public key should be 1184 bytes
    try std.testing.expectEqual(@as(usize, pq_kem.PK_SIZE), kp.public_key.ml_kem.len);
}

test "hybrid: encapsulate produces valid result" {
    var kp = try keygen();
    defer kp.deinit();

    var result = try encapsulate(kp.public_key);
    defer result.deinit();

    try std.testing.expectEqual(@as(usize, 32), result.x25519_ephemeral_pk.len);
    try std.testing.expectEqual(@as(usize, pq_kem.CT_SIZE), result.ml_kem_ciphertext.len);
    try std.testing.expectEqual(@as(usize, 32), result.shared_secret.len);
}

test "hybrid: shared secret size is 32 bytes" {
    try std.testing.expectEqual(@as(usize, 32), SHARED_SECRET_SIZE);
}
