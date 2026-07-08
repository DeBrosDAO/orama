/// Ed25519 ownership proof for vault identities.
///
/// A vault identity is defined as `identity = SHA-256(ed25519_public_key)`.
/// To push or pull an identity a caller must present its public key and a valid
/// Ed25519 signature over a canonical, domain-separated message. This is what
/// prevents anyone who merely *knows* a 64-hex identity from reading or
/// overwriting it — the previous HMAC challenge proved nothing about ownership.
///
/// Signed messages (ASCII, must match the Go gateway and client exactly):
///   push: "vault-push-v1:" ++ identity_hex ++ ":" ++ decimal(version)
///   pull: "vault-pull-v1:" ++ identity_hex ++ ":" ++ decimal(unix_seconds)
///
/// The push message binds the monotonic version (so a captured signature cannot
/// be reused for a different version), and the pull message binds a timestamp
/// checked against a small skew window (so a captured pull cannot be replayed
/// beyond it — and a replay only re-fetches ciphertext the attacker can't read).
const std = @import("std");
const Ed25519 = std.crypto.sign.Ed25519;
const Sha256 = std.crypto.hash.sha2.Sha256;

/// Maximum accepted clock skew for pull request timestamps, in seconds.
pub const PULL_MAX_SKEW_S: i64 = 120;

/// True if identity_hex (64 lowercase hex) equals SHA-256(pubkey).
pub fn identityMatchesPubkey(identity_hex: []const u8, pubkey: [32]u8) bool {
    if (identity_hex.len != 64) return false;
    var digest: [32]u8 = undefined;
    Sha256.hash(&pubkey, &digest, .{});
    const want = std.fmt.bytesToHex(digest, .lower);
    return std.ascii.eqlIgnoreCase(identity_hex, &want);
}

/// Verify an Ed25519 signature over `message` by `pubkey`.
fn verifySig(message: []const u8, pubkey: [32]u8, sig: [64]u8) bool {
    const pk = Ed25519.PublicKey.fromBytes(pubkey) catch return false;
    const signature = Ed25519.Signature.fromBytes(sig);
    signature.verify(message, pk) catch return false;
    return true;
}

fn decodeHex(comptime n: usize, hex: []const u8) ?[n]u8 {
    if (hex.len != n * 2) return null;
    var out: [n]u8 = undefined;
    _ = std.fmt.hexToBytes(&out, hex) catch return null;
    return out;
}

/// Verify ownership for a push: identity == SHA-256(pubkey) and the Ed25519
/// signature over "vault-push-v1:<identity>:<version>" is valid.
pub fn verifyPush(identity_hex: []const u8, version: u64, pubkey_hex: []const u8, sig_hex: []const u8) bool {
    const pubkey = decodeHex(32, pubkey_hex) orelse return false;
    const sig = decodeHex(64, sig_hex) orelse return false;
    if (!identityMatchesPubkey(identity_hex, pubkey)) return false;

    var msg_buf: [160]u8 = undefined;
    const msg = std.fmt.bufPrint(&msg_buf, "vault-push-v1:{s}:{d}", .{ identity_hex, version }) catch return false;
    return verifySig(msg, pubkey, sig);
}

/// Verify ownership for a pull: identity == SHA-256(pubkey), the timestamp is
/// within the skew window, and the Ed25519 signature over
/// "vault-pull-v1:<identity>:<timestamp>" is valid.
pub fn verifyPull(identity_hex: []const u8, timestamp: i64, now: i64, pubkey_hex: []const u8, sig_hex: []const u8) bool {
    const diff = now - timestamp;
    if (diff > PULL_MAX_SKEW_S or diff < -PULL_MAX_SKEW_S) return false;

    const pubkey = decodeHex(32, pubkey_hex) orelse return false;
    const sig = decodeHex(64, sig_hex) orelse return false;
    if (!identityMatchesPubkey(identity_hex, pubkey)) return false;

    var msg_buf: [160]u8 = undefined;
    const msg = std.fmt.bufPrint(&msg_buf, "vault-pull-v1:{s}:{d}", .{ identity_hex, timestamp }) catch return false;
    return verifySig(msg, pubkey, sig);
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "verifyPush/verifyPull round-trip with a real keypair" {
    const kp = Ed25519.KeyPair.generate();
    const pubkey = kp.public_key.bytes;

    var digest: [32]u8 = undefined;
    Sha256.hash(&pubkey, &digest, .{});
    const identity_hex = std.fmt.bytesToHex(digest, .lower);
    const pubkey_hex = std.fmt.bytesToHex(pubkey, .lower);

    // push
    var pmsg: [160]u8 = undefined;
    const push_msg = try std.fmt.bufPrint(&pmsg, "vault-push-v1:{s}:{d}", .{ &identity_hex, @as(u64, 7) });
    const push_sig = try kp.sign(push_msg, null);
    const push_sig_hex = std.fmt.bytesToHex(push_sig.toBytes(), .lower);
    try std.testing.expect(verifyPush(&identity_hex, 7, &pubkey_hex, &push_sig_hex));
    try std.testing.expect(!verifyPush(&identity_hex, 8, &pubkey_hex, &push_sig_hex)); // wrong version

    // pull
    const ts: i64 = 1_700_000_000;
    var qmsg: [160]u8 = undefined;
    const pull_msg = try std.fmt.bufPrint(&qmsg, "vault-pull-v1:{s}:{d}", .{ &identity_hex, ts });
    const pull_sig = try kp.sign(pull_msg, null);
    const pull_sig_hex = std.fmt.bytesToHex(pull_sig.toBytes(), .lower);
    try std.testing.expect(verifyPull(&identity_hex, ts, ts + 10, &pubkey_hex, &pull_sig_hex));
    try std.testing.expect(!verifyPull(&identity_hex, ts, ts + 10_000, &pubkey_hex, &pull_sig_hex)); // stale
}

test "identity must match pubkey" {
    const kp = Ed25519.KeyPair.generate();
    const other = Ed25519.KeyPair.generate();
    const wrong_identity = std.fmt.bytesToHex([_]u8{0} ** 32, .lower);
    _ = other;
    try std.testing.expect(!identityMatchesPubkey(&wrong_identity, kp.public_key.bytes));
}
