/// AES-256-GCM encryption/decryption.
///
/// Wraps Zig's std.crypto.aead.aes_gcm for a clean API.
/// 12-byte random nonce, 16-byte auth tag appended to ciphertext.
const std = @import("std");
const Aes256Gcm = std.crypto.aead.aes_gcm.Aes256Gcm;

pub const KEY_SIZE = 32;
pub const NONCE_SIZE = 12;
pub const TAG_SIZE = 16;

pub const EncryptedData = struct {
    /// Ciphertext (same length as plaintext)
    ciphertext: []const u8,
    /// 12-byte nonce
    nonce: [NONCE_SIZE]u8,
    /// 16-byte authentication tag
    tag: [TAG_SIZE]u8,
};

/// Encrypts plaintext with AES-256-GCM.
/// Returns EncryptedData with ciphertext, nonce, and tag.
/// Caller must free ciphertext.
pub fn encrypt(
    allocator: std.mem.Allocator,
    plaintext: []const u8,
    key: [KEY_SIZE]u8,
) !EncryptedData {
    var nonce: [NONCE_SIZE]u8 = undefined;
    std.crypto.random.bytes(&nonce);

    const ciphertext = try allocator.alloc(u8, plaintext.len);
    errdefer allocator.free(ciphertext);

    var tag: [TAG_SIZE]u8 = undefined;
    Aes256Gcm.encrypt(ciphertext, &tag, plaintext, &.{}, nonce, key);

    return EncryptedData{
        .ciphertext = ciphertext,
        .nonce = nonce,
        .tag = tag,
    };
}

/// Decrypts AES-256-GCM ciphertext.
/// Returns plaintext. Caller must free and zero.
pub fn decrypt(
    allocator: std.mem.Allocator,
    data: EncryptedData,
    key: [KEY_SIZE]u8,
) ![]u8 {
    const plaintext = try allocator.alloc(u8, data.ciphertext.len);
    errdefer {
        @memset(plaintext, 0);
        allocator.free(plaintext);
    }

    Aes256Gcm.decrypt(plaintext, data.ciphertext, data.tag, &.{}, data.nonce, key) catch {
        return error.AuthenticationFailed;
    };

    return plaintext;
}

/// Generates a random 32-byte AES-256 key.
pub fn generateKey() [KEY_SIZE]u8 {
    var key: [KEY_SIZE]u8 = undefined;
    std.crypto.random.bytes(&key);
    return key;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "encrypt/decrypt round-trip" {
    const allocator = std.testing.allocator;
    const key = generateKey();
    const plaintext = "Hello, vault!";

    const encrypted = try encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    const decrypted = try decrypt(allocator, encrypted, key);
    defer {
        @memset(decrypted, 0);
        allocator.free(decrypted);
    }

    try std.testing.expectEqualSlices(u8, plaintext, decrypted);
}

test "wrong key fails to decrypt" {
    const allocator = std.testing.allocator;
    const key = generateKey();
    const wrong_key = generateKey();
    const plaintext = "secret data";

    const encrypted = try encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    try std.testing.expectError(error.AuthenticationFailed, decrypt(allocator, encrypted, wrong_key));
}

test "tampered ciphertext fails to decrypt" {
    const allocator = std.testing.allocator;
    const key = generateKey();
    const plaintext = "secret data";

    const encrypted = try encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    // Tamper with ciphertext
    const tampered = encrypted;
    const ct_mut: []u8 = @constCast(tampered.ciphertext);
    ct_mut[0] ^= 0xFF;

    try std.testing.expectError(error.AuthenticationFailed, decrypt(allocator, tampered, key));
}

test "different nonces produce different ciphertexts" {
    const allocator = std.testing.allocator;
    const key = generateKey();
    const plaintext = "same plaintext";

    const enc1 = try encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(enc1.ciphertext));
    const enc2 = try encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(enc2.ciphertext));

    // Nonces should differ (random)
    try std.testing.expect(!std.mem.eql(u8, &enc1.nonce, &enc2.nonce));
    // Ciphertexts should differ
    try std.testing.expect(!std.mem.eql(u8, enc1.ciphertext, enc2.ciphertext));
}

test "empty plaintext" {
    const allocator = std.testing.allocator;
    const key = generateKey();

    const encrypted = try encrypt(allocator, &.{}, key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    const decrypted = try decrypt(allocator, encrypted, key);
    defer allocator.free(decrypted);

    try std.testing.expectEqual(@as(usize, 0), decrypted.len);
}
