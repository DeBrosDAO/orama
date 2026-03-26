/// Integration tests — tests the full system working together across modules.
///
/// These tests verify end-to-end flows that span multiple subsystems:
/// SSS split/combine, AES-256-GCM encryption, file_store with HMAC integrity,
/// Merkle commitment verification, proactive re-sharing, and quorum logic.
const std = @import("std");

// ── Module imports ──────────────────────────────────────────────────────────

const split_mod = @import("sss/split.zig");
const combine_mod = @import("sss/combine.zig");
const commitment_mod = @import("sss/commitment.zig");
const reshare_mod = @import("sss/reshare.zig");
const types = @import("sss/types.zig");

const aes = @import("crypto/aes.zig");
const hmac = @import("crypto/hmac.zig");
const hkdf = @import("crypto/hkdf.zig");

const file_store = @import("storage/file_store.zig");

const quorum = @import("membership/quorum.zig");

// ── Helpers ─────────────────────────────────────────────────────────────────

/// Creates a temporary directory and returns its real path.
/// Caller must call cleanup() on the returned tmpDir.
fn makeTmpDir(buf: []u8) struct { dir: std.testing.TmpDir, path: []const u8 } {
    var tmp = std.testing.tmpDir(.{});
    const path = tmp.dir.realpath(".", buf) catch @panic("failed to get tmp realpath");
    return .{ .dir = tmp, .path = path };
}

/// Generates a random byte buffer of `len` bytes.
fn randomBytes(allocator: std.mem.Allocator, len: usize) ![]u8 {
    const buf = try allocator.alloc(u8, len);
    std.crypto.random.bytes(buf);
    return buf;
}

/// Hex-encode a byte slice into a stack buffer suitable for identity hashes.
fn hexEncode(bytes: []const u8, out: []u8) []const u8 {
    const charset = "0123456789abcdef";
    var i: usize = 0;
    for (bytes) |b| {
        out[i] = charset[b >> 4];
        out[i + 1] = charset[b & 0x0F];
        i += 2;
    }
    return out[0..i];
}

// ═══════════════════════════════════════════════════════════════════════════
// 1. Full vault lifecycle simulation
// ═══════════════════════════════════════════════════════════════════════════

test "integration: full vault lifecycle — split, store, read, combine" {
    const allocator = std.testing.allocator;

    // Generate a random 1KB secret (simulating encrypted vault data)
    const secret = try randomBytes(allocator, 1024);
    defer allocator.free(secret);

    // Split with N=7, K=3 (7 guardian nodes)
    const share_set = try split_mod.split(allocator, secret, 7, 3);
    defer share_set.deinit(allocator);

    // Store each share to a separate temp directory using file_store
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp = makeTmpDir(&tmp_dir_buf);
    var tmp_dir = tmp.dir;
    defer tmp_dir.cleanup();
    const tmp_path = tmp.path;

    const integrity_key = "vault-integration-test-key-32b!!";

    // Write each share to disk with a unique identity
    for (share_set.shares, 0..) |share, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(i)), share.x, 0xAB, 0xCD };
        const identity = hexEncode(&id_byte, &id_buf);
        try file_store.writeShare(tmp_path, identity, share.y, integrity_key, allocator);
    }

    // Read shares back and verify HMAC integrity
    for (share_set.shares, 0..) |share, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(i)), share.x, 0xAB, 0xCD };
        const identity = hexEncode(&id_byte, &id_buf);
        const read_data = try file_store.readShare(tmp_path, identity, integrity_key, allocator);
        defer allocator.free(read_data);
        try std.testing.expectEqualSlices(u8, share.y, read_data);
    }

    // Combine using multiple different K-subsets and verify reconstruction
    // Subset 1: shares 0, 1, 2
    {
        const subset = [_]types.Share{ share_set.shares[0], share_set.shares[1], share_set.shares[2] };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // Subset 2: shares 2, 4, 6
    {
        const subset = [_]types.Share{ share_set.shares[2], share_set.shares[4], share_set.shares[6] };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // Subset 3: shares 1, 3, 5
    {
        const subset = [_]types.Share{ share_set.shares[1], share_set.shares[3], share_set.shares[5] };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 2. Multi-guardian failure tolerance
// ═══════════════════════════════════════════════════════════════════════════

test "integration: multi-guardian failure tolerance — N=10, K=4" {
    const allocator = std.testing.allocator;

    const secret = try randomBytes(allocator, 256);
    defer allocator.free(secret);

    const n: u8 = 10;
    const k: u8 = 4;
    const share_set = try split_mod.split(allocator, secret, n, k);
    defer share_set.deinit(allocator);

    // Recovery with exactly K shares (indices 0,3,5,9)
    {
        const subset = [_]types.Share{
            share_set.shares[0],
            share_set.shares[3],
            share_set.shares[5],
            share_set.shares[9],
        };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // Recovery with K+1 shares
    {
        const recovered = try combine_mod.combine(allocator, share_set.shares[0..5]);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // Recovery with K+2 shares
    {
        const recovered = try combine_mod.combine(allocator, share_set.shares[0..6]);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // Recovery with all N shares
    {
        const recovered = try combine_mod.combine(allocator, share_set.shares);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, secret, recovered);
    }

    // K-1 shares should NOT reconstruct the original secret
    // (with overwhelming probability for a 256-byte secret)
    {
        const subset = [_]types.Share{
            share_set.shares[0],
            share_set.shares[1],
            share_set.shares[2],
        };
        const result = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(result, 0);
            allocator.free(result);
        }
        try std.testing.expect(!std.mem.eql(u8, secret, result));
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 3. Storage integrity under tampering
// ═══════════════════════════════════════════════════════════════════════════

test "integration: storage integrity under tampering" {
    const allocator = std.testing.allocator;

    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp = makeTmpDir(&tmp_dir_buf);
    var tmp_dir = tmp.dir;
    defer tmp_dir.cleanup();
    const tmp_path = tmp.path;

    const identity = "tampertest01";
    const share_data = try randomBytes(allocator, 512);
    defer allocator.free(share_data);
    const integrity_key = "tamper-integrity-key-32-bytes!!!";

    // Write and read back successfully
    try file_store.writeShare(tmp_path, identity, share_data, integrity_key, allocator);
    {
        const read_data = try file_store.readShare(tmp_path, identity, integrity_key, allocator);
        defer allocator.free(read_data);
        try std.testing.expectEqualSlices(u8, share_data, read_data);
    }

    // Tamper with the share.bin file (flip a byte)
    const share_path = try std.fmt.allocPrint(allocator, "{s}/shares/{s}/share.bin", .{ tmp_path, identity });
    defer allocator.free(share_path);
    {
        const file = try std.fs.cwd().openFile(share_path, .{ .mode = .write_only });
        defer file.close();
        // Write a single modified byte at the beginning
        const tampered_byte = [_]u8{share_data[0] ^ 0xFF};
        try file.writeAll(&tampered_byte);
    }

    // readShare should now fail with IntegrityCheckFailed
    try std.testing.expectError(
        file_store.StoreError.IntegrityCheckFailed,
        file_store.readShare(tmp_path, identity, integrity_key, allocator),
    );

    // Re-write the original data and verify it works again
    try file_store.writeShare(tmp_path, identity, share_data, integrity_key, allocator);
    {
        const read_data = try file_store.readShare(tmp_path, identity, integrity_key, allocator);
        defer allocator.free(read_data);
        try std.testing.expectEqualSlices(u8, share_data, read_data);
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 4. Shamir + AES-GCM full encryption round-trip
// ═══════════════════════════════════════════════════════════════════════════

test "integration: Shamir + AES-256-GCM encryption round-trip" {
    const allocator = std.testing.allocator;

    // Generate a random AES key
    const key = aes.generateKey();

    // Encrypt a payload
    const plaintext = "This is sensitive vault data that must survive split/combine via Shamir + AES-GCM.";
    const encrypted = try aes.encrypt(allocator, plaintext, key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    // Use the ciphertext + nonce + tag as the "secret" for Shamir split.
    // Concatenate: nonce (12) || tag (16) || ciphertext (N)
    const secret_len = aes.NONCE_SIZE + aes.TAG_SIZE + encrypted.ciphertext.len;
    const secret = try allocator.alloc(u8, secret_len);
    defer allocator.free(secret);

    @memcpy(secret[0..aes.NONCE_SIZE], &encrypted.nonce);
    @memcpy(secret[aes.NONCE_SIZE .. aes.NONCE_SIZE + aes.TAG_SIZE], &encrypted.tag);
    @memcpy(secret[aes.NONCE_SIZE + aes.TAG_SIZE ..], encrypted.ciphertext);

    // Split into 5 shares with K=3
    const share_set = try split_mod.split(allocator, secret, 5, 3);
    defer share_set.deinit(allocator);

    // Combine K=3 shares back
    const subset = [_]types.Share{ share_set.shares[0], share_set.shares[2], share_set.shares[4] };
    const recovered_secret = try combine_mod.combine(allocator, &subset);
    defer {
        @memset(recovered_secret, 0);
        allocator.free(recovered_secret);
    }

    // Extract nonce, tag, ciphertext from recovered secret
    var recovered_nonce: [aes.NONCE_SIZE]u8 = undefined;
    @memcpy(&recovered_nonce, recovered_secret[0..aes.NONCE_SIZE]);

    var recovered_tag: [aes.TAG_SIZE]u8 = undefined;
    @memcpy(&recovered_tag, recovered_secret[aes.NONCE_SIZE .. aes.NONCE_SIZE + aes.TAG_SIZE]);

    const recovered_ct = recovered_secret[aes.NONCE_SIZE + aes.TAG_SIZE ..];

    const recovered_encrypted = aes.EncryptedData{
        .ciphertext = recovered_ct,
        .nonce = recovered_nonce,
        .tag = recovered_tag,
    };

    // Decrypt with original key
    const decrypted = try aes.decrypt(allocator, recovered_encrypted, key);
    defer {
        @memset(decrypted, 0);
        allocator.free(decrypted);
    }

    try std.testing.expectEqualSlices(u8, plaintext, decrypted);
}

// ═══════════════════════════════════════════════════════════════════════════
// 5. Adaptive threshold + quorum consistency
// ═══════════════════════════════════════════════════════════════════════════

test "integration: adaptive threshold + quorum consistency" {
    // readQuorum is the Shamir threshold K = max(3, floor(N/3))
    // writeQuorum is ceil(2/3 * N)
    //
    // Invariants that always hold:
    //   - threshold >= 3 (minimum security)
    //   - writeQuorum > N/2 (majority) for N >= 3
    //
    // For production-sized clusters (N >= 9), an additional invariant holds:
    //   - threshold <= writeQuorum (enough shares stored for reconstruction)
    // For small clusters (N < 9), the minimum threshold of 3 can exceed the
    // write quorum. This is a known tradeoff: small clusters require ALL nodes
    // for write+read to succeed, which is acceptable for tiny deployments.

    const test_values = [_]usize{ 3, 5, 7, 10, 14, 50, 100 };

    for (test_values) |n| {
        const threshold = quorum.readQuorum(n);
        const wq = quorum.writeQuorum(n);

        // threshold >= 3 (minimum security)
        try std.testing.expect(threshold >= 3);

        // writeQuorum > N/2 (majority) for N >= 3
        try std.testing.expect(wq > n / 2);

        // For production clusters (N >= 9), threshold must fit within write quorum
        if (n >= 9) {
            try std.testing.expect(threshold <= wq);
        }

        // writeQuorum + threshold >= N (read+write overlap guarantees consistency)
        // This is the fundamental quorum intersection property: any write set
        // and any read set must share at least one node.
        try std.testing.expect(wq + threshold >= n);
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 6. Large payload round-trip
// ═══════════════════════════════════════════════════════════════════════════

test "integration: large payload round-trip (100KB)" {
    const allocator = std.testing.allocator;

    // Generate a 100KB random payload
    const payload_size = 100 * 1024;
    const secret = try randomBytes(allocator, payload_size);
    defer allocator.free(secret);

    // Split into 14 shares with K=5
    const share_set = try split_mod.split(allocator, secret, 14, 5);
    defer share_set.deinit(allocator);

    // Store all shares to disk via file_store
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp = makeTmpDir(&tmp_dir_buf);
    var tmp_dir = tmp.dir;
    defer tmp_dir.cleanup();
    const tmp_path = tmp.path;

    const integrity_key = "large-payload-integrity-key-32b!";

    for (share_set.shares, 0..) |share, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(i)), share.x, 0x11, 0x22 };
        const identity = hexEncode(&id_byte, &id_buf);
        try file_store.writeShare(tmp_path, identity, share.y, integrity_key, allocator);
    }

    // Read back any 5 shares (indices 2, 5, 7, 10, 13)
    const read_indices = [_]usize{ 2, 5, 7, 10, 13 };
    var read_shares: [5]types.Share = undefined;
    var read_bufs: [5][]u8 = undefined;

    for (read_indices, 0..) |idx, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(idx)), share_set.shares[idx].x, 0x11, 0x22 };
        const identity = hexEncode(&id_byte, &id_buf);
        const data = try file_store.readShare(tmp_path, identity, integrity_key, allocator);
        read_bufs[i] = data;
        read_shares[i] = types.Share{
            .x = share_set.shares[idx].x,
            .y = data,
        };
    }
    defer {
        for (&read_bufs) |buf| allocator.free(buf);
    }

    // Combine and verify exact match
    const recovered = try combine_mod.combine(allocator, &read_shares);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqual(payload_size, recovered.len);
    try std.testing.expectEqualSlices(u8, secret, recovered);
}

// ═══════════════════════════════════════════════════════════════════════════
// 7. Commitment tree cross-check
// ═══════════════════════════════════════════════════════════════════════════

test "integration: Merkle commitment tree — build, prove, verify, tamper" {
    const allocator = std.testing.allocator;

    // Create a set of shares via Shamir split
    const secret = try randomBytes(allocator, 64);
    defer allocator.free(secret);

    const share_set = try split_mod.split(allocator, secret, 7, 3);
    defer share_set.deinit(allocator);

    // Build the Merkle commitment tree
    const tree = try commitment_mod.buildTree(allocator, share_set.shares);
    defer tree.deinit(allocator);

    try std.testing.expectEqual(@as(usize, 7), tree.leaves.len);

    // Verify that each share's Merkle proof validates against the root
    for (share_set.shares, 0..) |share, i| {
        const proof = try commitment_mod.generateProof(allocator, share_set.shares, i);
        defer proof.deinit(allocator);

        try std.testing.expect(commitment_mod.verifyProof(share, proof, tree.root));
    }

    // Tamper with one share and verify the proof fails
    {
        const proof_0 = try commitment_mod.generateProof(allocator, share_set.shares, 0);
        defer proof_0.deinit(allocator);

        // Create a tampered share with modified y data
        const tampered_y = try allocator.alloc(u8, share_set.shares[0].y.len);
        defer allocator.free(tampered_y);
        @memcpy(tampered_y, share_set.shares[0].y);
        tampered_y[0] ^= 0xFF; // flip a byte

        const tampered_share = types.Share{
            .x = share_set.shares[0].x,
            .y = tampered_y,
        };

        // Proof should fail for the tampered share
        try std.testing.expect(!commitment_mod.verifyProof(tampered_share, proof_0, tree.root));
    }

    // Verify wrong root also fails
    {
        const proof_0 = try commitment_mod.generateProof(allocator, share_set.shares, 0);
        defer proof_0.deinit(allocator);

        const wrong_root = [_]u8{0xDE} ** 32;
        try std.testing.expect(!commitment_mod.verifyProof(share_set.shares[0], proof_0, wrong_root));
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 8. Reshare round-trip
// ═══════════════════════════════════════════════════════════════════════════

test "integration: proactive reshare — new shares reconstruct, old+new mixed fails" {
    const allocator = std.testing.allocator;

    const secret = [_]u8{ 10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160 };
    const n: u8 = 5;
    const k: u8 = 3;

    // Step 1: Split the secret into original shares
    const share_set = try split_mod.split(allocator, &secret, n, k);
    defer share_set.deinit(allocator);

    // Step 2: Each guardian generates reshare deltas
    var all_deltas: [5][]reshare_mod.ReshareDelta = undefined;
    for (0..n) |i| {
        all_deltas[i] = try reshare_mod.generateDeltas(
            allocator,
            @as(u8, @truncate(i)) + 1,
            secret.len,
            n,
            k,
        );
    }
    defer {
        for (0..n) |i| {
            for (all_deltas[i]) |*d| d.deinit(allocator);
            allocator.free(all_deltas[i]);
        }
    }

    // Step 3: Each guardian applies deltas to get new shares
    var new_shares: [5]types.Share = undefined;
    for (0..n) |j| {
        var deltas_for_j: [5]reshare_mod.ReshareDelta = undefined;
        for (0..n) |i| {
            deltas_for_j[i] = all_deltas[i][j];
        }
        new_shares[j] = try reshare_mod.applyDeltas(
            allocator,
            share_set.shares[j],
            &deltas_for_j,
        );
    }
    defer {
        for (&new_shares) |*s| s.deinit(allocator);
    }

    // Step 4: Verify new shares reconstruct the same secret
    // Test multiple subsets of new shares
    {
        const subset = [_]types.Share{ new_shares[0], new_shares[2], new_shares[4] };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, &secret, recovered);
    }
    {
        const subset = [_]types.Share{ new_shares[1], new_shares[3], new_shares[4] };
        const recovered = try combine_mod.combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, &secret, recovered);
    }

    // Step 5: Verify old + new shares mixed does NOT reconstruct the secret
    // Mix: 1 new share + 2 old shares from different epoch
    {
        const mixed = [_]types.Share{ new_shares[0], share_set.shares[2], share_set.shares[4] };
        const result = try combine_mod.combine(allocator, &mixed);
        defer {
            @memset(result, 0);
            allocator.free(result);
        }
        // With a 16-byte secret, the probability of an accidental match is ~1/2^128
        try std.testing.expect(!std.mem.eql(u8, &secret, result));
    }

    // Mix: 2 new shares + 1 old share
    {
        const mixed = [_]types.Share{ new_shares[0], new_shares[1], share_set.shares[3] };
        const result = try combine_mod.combine(allocator, &mixed);
        defer {
            @memset(result, 0);
            allocator.free(result);
        }
        try std.testing.expect(!std.mem.eql(u8, &secret, result));
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// 9. HKDF-derived keys for domain separation in vault operations
// ═══════════════════════════════════════════════════════════════════════════

test "integration: HKDF domain separation — different contexts yield different keys" {
    // Simulate deriving multiple keys from a single seed for different vault operations:
    // one for AES encryption, one for HMAC integrity, one for share commitment.
    const seed = "master-vault-seed-from-wallet-signature-1234567890";

    var aes_key: [32]u8 = undefined;
    var hmac_key: [32]u8 = undefined;
    var commit_key: [32]u8 = undefined;

    hkdf.deriveKey(&aes_key, seed, "orama-vault-v1", "aes-encryption-key");
    hkdf.deriveKey(&hmac_key, seed, "orama-vault-v1", "hmac-integrity-key");
    hkdf.deriveKey(&commit_key, seed, "orama-vault-v1", "commitment-key");

    // All three keys must be different
    try std.testing.expect(!std.mem.eql(u8, &aes_key, &hmac_key));
    try std.testing.expect(!std.mem.eql(u8, &aes_key, &commit_key));
    try std.testing.expect(!std.mem.eql(u8, &hmac_key, &commit_key));

    // Use AES key to encrypt, HMAC key for integrity check on the ciphertext
    const allocator = std.testing.allocator;
    const plaintext = "vault entry: private key material";

    const encrypted = try aes.encrypt(allocator, plaintext, aes_key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    // Compute HMAC of ciphertext with the HMAC-specific key
    const mac = hmac.compute(&hmac_key, encrypted.ciphertext);

    // Verify HMAC passes with correct key
    try std.testing.expect(hmac.verify(&hmac_key, encrypted.ciphertext, mac));

    // Verify HMAC fails with wrong key
    try std.testing.expect(!hmac.verify(&aes_key, encrypted.ciphertext, mac));

    // Decrypt successfully with the right AES key
    const decrypted = try aes.decrypt(allocator, encrypted, aes_key);
    defer {
        @memset(decrypted, 0);
        allocator.free(decrypted);
    }
    try std.testing.expectEqualSlices(u8, plaintext, decrypted);
}

// ═══════════════════════════════════════════════════════════════════════════
// 10. Full pipeline: HKDF -> AES -> Shamir -> Store -> Read -> Combine -> Decrypt
// ═══════════════════════════════════════════════════════════════════════════

test "integration: full pipeline — derive keys, encrypt, split, store, reconstruct, decrypt" {
    const allocator = std.testing.allocator;

    // 1. Derive keys from a wallet seed
    const wallet_seed = "user-wallet-signature-bytes-here-at-least-32-bytes!!";

    var encryption_key: [aes.KEY_SIZE]u8 = undefined;
    hkdf.deriveKey(&encryption_key, wallet_seed, "orama-vault-v1", "encryption");

    var integrity_key_buf: [32]u8 = undefined;
    hkdf.deriveKey(&integrity_key_buf, wallet_seed, "orama-vault-v1", "integrity");

    // 2. Encrypt the vault payload
    const vault_data = "{ \"privateKey\": \"0xDEADBEEF...\", \"mnemonic\": \"abandon abandon ...\" }";
    const encrypted = try aes.encrypt(allocator, vault_data, encryption_key);
    defer allocator.free(@constCast(encrypted.ciphertext));

    // 3. Package ciphertext + nonce + tag as the Shamir secret
    const secret_len = aes.NONCE_SIZE + aes.TAG_SIZE + encrypted.ciphertext.len;
    const shamir_secret = try allocator.alloc(u8, secret_len);
    defer allocator.free(shamir_secret);

    @memcpy(shamir_secret[0..aes.NONCE_SIZE], &encrypted.nonce);
    @memcpy(shamir_secret[aes.NONCE_SIZE .. aes.NONCE_SIZE + aes.TAG_SIZE], &encrypted.tag);
    @memcpy(shamir_secret[aes.NONCE_SIZE + aes.TAG_SIZE ..], encrypted.ciphertext);

    // 4. Split into shares
    const n: u8 = 7;
    const k: u8 = 3;
    const share_set = try split_mod.split(allocator, shamir_secret, n, k);
    defer share_set.deinit(allocator);

    // 5. Build Merkle commitment tree (for cross-guardian verification)
    const tree = try commitment_mod.buildTree(allocator, share_set.shares);
    defer tree.deinit(allocator);

    // 6. Store shares to disk
    var tmp_dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp = makeTmpDir(&tmp_dir_buf);
    var tmp_dir = tmp.dir;
    defer tmp_dir.cleanup();
    const tmp_path = tmp.path;

    for (share_set.shares, 0..) |share, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(i)), share.x, 0xFE, 0xED };
        const identity = hexEncode(&id_byte, &id_buf);
        try file_store.writeShare(tmp_path, identity, share.y, &integrity_key_buf, allocator);
    }

    // 7. Simulate recovery: read K shares from disk, verify commitments, combine
    const recovery_indices = [_]usize{ 1, 4, 6 };
    var recovery_shares: [3]types.Share = undefined;
    var recovery_bufs: [3][]u8 = undefined;

    for (recovery_indices, 0..) |idx, i| {
        var id_buf: [8]u8 = undefined;
        const id_byte = [_]u8{ @as(u8, @truncate(idx)), share_set.shares[idx].x, 0xFE, 0xED };
        const identity = hexEncode(&id_byte, &id_buf);
        const data = try file_store.readShare(tmp_path, identity, &integrity_key_buf, allocator);
        recovery_bufs[i] = data;
        recovery_shares[i] = types.Share{
            .x = share_set.shares[idx].x,
            .y = data,
        };

        // Verify Merkle proof for each recovered share
        const proof = try commitment_mod.generateProof(allocator, share_set.shares, idx);
        defer proof.deinit(allocator);
        try std.testing.expect(commitment_mod.verifyProof(recovery_shares[i], proof, tree.root));
    }
    defer {
        for (&recovery_bufs) |buf| allocator.free(buf);
    }

    // 8. Combine shares to recover the Shamir secret
    const recovered_secret = try combine_mod.combine(allocator, &recovery_shares);
    defer {
        @memset(recovered_secret, 0);
        allocator.free(recovered_secret);
    }
    try std.testing.expectEqualSlices(u8, shamir_secret, recovered_secret);

    // 9. Extract nonce/tag/ciphertext and decrypt
    var rec_nonce: [aes.NONCE_SIZE]u8 = undefined;
    @memcpy(&rec_nonce, recovered_secret[0..aes.NONCE_SIZE]);

    var rec_tag: [aes.TAG_SIZE]u8 = undefined;
    @memcpy(&rec_tag, recovered_secret[aes.NONCE_SIZE .. aes.NONCE_SIZE + aes.TAG_SIZE]);

    const rec_ct = recovered_secret[aes.NONCE_SIZE + aes.TAG_SIZE ..];

    const rec_encrypted = aes.EncryptedData{
        .ciphertext = rec_ct,
        .nonce = rec_nonce,
        .tag = rec_tag,
    };

    const decrypted = try aes.decrypt(allocator, rec_encrypted, encryption_key);
    defer {
        @memset(decrypted, 0);
        allocator.free(decrypted);
    }

    try std.testing.expectEqualSlices(u8, vault_data, decrypted);
}
