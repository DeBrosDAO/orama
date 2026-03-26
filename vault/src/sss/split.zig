/// Shamir Secret Sharing — Split operation.
///
/// Splits a secret byte array into N shares with threshold K.
/// For each byte of the secret, generates a random polynomial of degree K-1
/// with the secret byte as the constant term, then evaluates at points x=1..N.
const std = @import("std");
const poly = @import("polynomial.zig");
const types = @import("types.zig");

pub const SplitError = error{
    ThresholdTooSmall,
    ShareCountTooSmall,
    TooManyShares,
    EmptySecret,
    OutOfMemory,
};

/// Splits a secret into N shares with threshold K.
///
/// - secret: The data to split (1+ bytes)
/// - n: Total number of shares to generate (2..255)
/// - k: Threshold — minimum shares needed to reconstruct (2..N)
///
/// Returns a ShareSet that the caller must deinit.
pub fn split(
    allocator: std.mem.Allocator,
    secret: []const u8,
    n: u8,
    k: u8,
) SplitError!types.ShareSet {
    if (k < 2) return SplitError.ThresholdTooSmall;
    if (n < k) return SplitError.ShareCountTooSmall;
    if (secret.len == 0) return SplitError.EmptySecret;

    const secret_len = secret.len;

    // Allocate shares
    const shares = allocator.alloc(types.Share, n) catch return SplitError.OutOfMemory;
    errdefer {
        for (shares) |*s| {
            if (s.y.len > 0) {
                const m: []u8 = @constCast(s.y);
                @memset(m, 0);
                allocator.free(m);
            }
        }
        allocator.free(shares);
    }

    for (shares, 0..) |*share, i| {
        const y_buf = allocator.alloc(u8, secret_len) catch return SplitError.OutOfMemory;
        share.* = .{
            .x = @as(u8, @truncate(i)) + 1, // x = 1..N
            .y = y_buf,
        };
    }

    // Allocate temporary coefficient buffer (reused per byte)
    const coeffs = allocator.alloc(u8, k) catch return SplitError.OutOfMemory;
    defer {
        @memset(coeffs, 0);
        allocator.free(coeffs);
    }

    // For each byte of the secret
    for (0..secret_len) |byte_idx| {
        // coeffs[0] = secret byte (constant term)
        coeffs[0] = secret[byte_idx];

        // coeffs[1..K-1] = random (CSPRNG)
        std.crypto.random.bytes(coeffs[1..]);

        // Evaluate polynomial at each share's x coordinate
        for (shares) |*share| {
            const y_mut: []u8 = @constCast(share.y);
            y_mut[byte_idx] = poly.evaluate(coeffs, share.x);
        }
    }

    return types.ShareSet{
        .threshold = k,
        .total = n,
        .shares = shares,
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "split: basic 2-of-3" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split(allocator, &secret, 3, 2);
    defer share_set.deinit(allocator);

    try std.testing.expectEqual(@as(u8, 2), share_set.threshold);
    try std.testing.expectEqual(@as(u8, 3), share_set.total);
    try std.testing.expectEqual(@as(usize, 3), share_set.shares.len);

    // Check x coordinates are 1, 2, 3
    try std.testing.expectEqual(@as(u8, 1), share_set.shares[0].x);
    try std.testing.expectEqual(@as(u8, 2), share_set.shares[1].x);
    try std.testing.expectEqual(@as(u8, 3), share_set.shares[2].x);

    // Each share's y should be 1 byte
    for (share_set.shares) |share| {
        try std.testing.expectEqual(@as(usize, 1), share.y.len);
    }
}

test "split: multi-byte secret" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 };
    const share_set = try split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    try std.testing.expectEqual(@as(usize, 5), share_set.shares.len);
    for (share_set.shares) |share| {
        try std.testing.expectEqual(@as(usize, 10), share.y.len);
    }
}

test "split: rejects K < 2" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{1};
    try std.testing.expectError(SplitError.ThresholdTooSmall, split(allocator, &secret, 3, 1));
}

test "split: rejects N < K" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{1};
    try std.testing.expectError(SplitError.ShareCountTooSmall, split(allocator, &secret, 2, 3));
}

test "split: rejects empty secret" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{};
    try std.testing.expectError(SplitError.EmptySecret, split(allocator, &secret, 3, 2));
}

test "split: x coordinates are sequential 1..N" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split(allocator, &secret, 10, 3);
    defer share_set.deinit(allocator);

    for (share_set.shares, 0..) |share, i| {
        try std.testing.expectEqual(@as(u8, @truncate(i)) + 1, share.x);
    }
}
