/// Shamir Secret Sharing — Combine (Lagrange interpolation).
///
/// Reconstructs the secret from K or more shares using Lagrange interpolation
/// at x=0 over GF(2^8).
const std = @import("std");
const gf = @import("field.zig");
const types = @import("types.zig");

pub const CombineError = error{
    NotEnoughShares,
    MismatchedShareLengths,
    ZeroShareIndex,
    DuplicateShareIndices,
    DivisionByZero,
    OutOfMemory,
};

/// Reconstructs the secret from K or more shares.
///
/// shares: Slice of Share structs. Must have at least 2 shares, all with the same y length.
/// Returns the reconstructed secret. Caller must free and zero the result.
pub fn combine(
    allocator: std.mem.Allocator,
    shares: []const types.Share,
) CombineError![]u8 {
    if (shares.len < 2) return CombineError.NotEnoughShares;

    const secret_len = shares[0].y.len;

    // Validate shares
    for (shares) |share| {
        if (share.x == 0) return CombineError.ZeroShareIndex;
        if (share.y.len != secret_len) return CombineError.MismatchedShareLengths;
    }

    // Check for duplicate x values
    for (shares, 0..) |a, i| {
        for (shares[i + 1 ..]) |b| {
            if (a.x == b.x) return CombineError.DuplicateShareIndices;
        }
    }

    // Allocate result buffer
    const result = allocator.alloc(u8, secret_len) catch return CombineError.OutOfMemory;
    @memset(result, 0);

    // Lagrange interpolation at x=0 for each byte position
    for (0..secret_len) |byte_idx| {
        var value: u8 = 0;

        for (shares, 0..) |share_i, i| {
            // Compute Lagrange basis polynomial L_i(0)
            // L_i(0) = Product_{j!=i} (0 - x_j) / (x_i - x_j)
            //         = Product_{j!=i} x_j / (x_i - x_j)
            // In GF(2^8): subtraction = XOR = addition
            var basis: u8 = 1;

            for (shares, 0..) |share_j, j| {
                if (i == j) continue;
                // numerator: x_j (since 0 - x_j = x_j in GF(2^8))
                // denominator: x_i - x_j = x_i XOR x_j
                const num = share_j.x;
                const den = gf.sub(share_i.x, share_j.x);
                basis = gf.mul(basis, try gf.div(num, den));
            }

            // Accumulate: value += share_i.y[byte_idx] * L_i(0)
            value = gf.add(value, gf.mul(share_i.y[byte_idx], basis));
        }

        result[byte_idx] = value;
    }

    return result;
}

// ── Tests ────────────────────────────────────────────────────────────────────

const split_mod = @import("split.zig");

test "round-trip: 2-of-3 single byte" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 3, 2);
    defer share_set.deinit(allocator);

    // Any 2 of 3 shares should reconstruct
    const pairs = [_][2]usize{ .{ 0, 1 }, .{ 0, 2 }, .{ 1, 2 } };
    for (pairs) |pair| {
        const subset = [_]types.Share{ share_set.shares[pair[0]], share_set.shares[pair[1]] };
        const recovered = try combine(allocator, &subset);
        defer {
            @memset(recovered, 0);
            allocator.free(recovered);
        }
        try std.testing.expectEqualSlices(u8, &secret, recovered);
    }
}

test "round-trip: 3-of-5 multi-byte" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 };
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    const subset = [_]types.Share{
        share_set.shares[0],
        share_set.shares[2],
        share_set.shares[4],
    };
    const recovered = try combine(allocator, &subset);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "round-trip: 2-of-2 minimum" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 0xFF, 0x00, 0x55, 0xAA };
    const share_set = try split_mod.split(allocator, &secret, 2, 2);
    defer share_set.deinit(allocator);

    const recovered = try combine(allocator, share_set.shares);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "round-trip: all C(5,3) = 10 subsets" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 42, 137, 255, 0 };
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    var count: usize = 0;
    for (0..5) |i| {
        for (i + 1..5) |j| {
            for (j + 1..5) |l| {
                const subset = [_]types.Share{
                    share_set.shares[i],
                    share_set.shares[j],
                    share_set.shares[l],
                };
                const recovered = try combine(allocator, &subset);
                defer {
                    @memset(recovered, 0);
                    allocator.free(recovered);
                }
                try std.testing.expectEqualSlices(u8, &secret, recovered);
                count += 1;
            }
        }
    }
    try std.testing.expectEqual(@as(usize, 10), count);
}

test "round-trip: large secret (256 bytes)" {
    const allocator = std.testing.allocator;
    var secret: [256]u8 = undefined;
    for (&secret, 0..) |*b, i| b.* = @truncate(i);

    const share_set = try split_mod.split(allocator, &secret, 10, 5);
    defer share_set.deinit(allocator);

    // Use first 5 shares
    const recovered = try combine(allocator, share_set.shares[0..5]);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "round-trip: all-zeros secret" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{0} ** 32;
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    const subset = [_]types.Share{
        share_set.shares[0],
        share_set.shares[2],
        share_set.shares[4],
    };
    const recovered = try combine(allocator, &subset);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "round-trip: all-0xFF secret" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{0xFF} ** 32;
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    const subset = [_]types.Share{
        share_set.shares[1],
        share_set.shares[3],
        share_set.shares[4],
    };
    const recovered = try combine(allocator, &subset);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "more than K shares also reconstructs" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{ 1, 2, 3 };
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    // Using 4 shares (more than K=3)
    const recovered = try combine(allocator, share_set.shares[0..4]);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }
    try std.testing.expectEqualSlices(u8, &secret, recovered);
}

test "K-1 shares produce wrong result" {
    const allocator = std.testing.allocator;
    const secret = [_]u8{42};
    const share_set = try split_mod.split(allocator, &secret, 5, 3);
    defer share_set.deinit(allocator);

    // With only 2 shares (K-1), should NOT consistently give back 42
    var match_count: usize = 0;
    for (0..5) |i| {
        for (i + 1..5) |j| {
            const subset = [_]types.Share{ share_set.shares[i], share_set.shares[j] };
            const result = try combine(allocator, &subset);
            defer {
                @memset(result, 0);
                allocator.free(result);
            }
            if (result[0] == 42) match_count += 1;
        }
    }
    // All 10 pairs matching would be astronomically unlikely
    try std.testing.expect(match_count < 10);
}

test "deterministic: known polynomial, manual shares" {
    // p(x) = 42 + 5x + 7x^2  (K=3, secret=42)
    // Manually evaluate at x=1,2,3 using the polynomial module
    const allocator = std.testing.allocator;
    const poly_mod = @import("polynomial.zig");

    const coeffs = [_]u8{ 42, 5, 7 };
    const y1 = poly_mod.evaluate(&coeffs, 1); // p(1)
    const y2 = poly_mod.evaluate(&coeffs, 2); // p(2)
    const y3 = poly_mod.evaluate(&coeffs, 3); // p(3)

    const shares = [_]types.Share{
        .{ .x = 1, .y = &[_]u8{y1} },
        .{ .x = 2, .y = &[_]u8{y2} },
        .{ .x = 3, .y = &[_]u8{y3} },
    };
    const recovered = try combine(allocator, &shares);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }

    try std.testing.expectEqual(@as(u8, 42), recovered[0]);
}

test "deterministic: secret=0, known polynomial" {
    // p(x) = 0 + 0xAB*x + 0xCD*x^2  (secret=0)
    const allocator = std.testing.allocator;
    const poly_mod = @import("polynomial.zig");

    const coeffs = [_]u8{ 0, 0xAB, 0xCD };
    const y1 = poly_mod.evaluate(&coeffs, 1);
    const y3 = poly_mod.evaluate(&coeffs, 3);
    const y5 = poly_mod.evaluate(&coeffs, 5);

    const shares = [_]types.Share{
        .{ .x = 1, .y = &[_]u8{y1} },
        .{ .x = 3, .y = &[_]u8{y3} },
        .{ .x = 5, .y = &[_]u8{y5} },
    };
    const recovered = try combine(allocator, &shares);
    defer {
        @memset(recovered, 0);
        allocator.free(recovered);
    }

    try std.testing.expectEqual(@as(u8, 0), recovered[0]);
}

test "combine: rejects fewer than 2 shares" {
    const allocator = std.testing.allocator;
    const empty: []const types.Share = &.{};
    try std.testing.expectError(CombineError.NotEnoughShares, combine(allocator, empty));

    const single = [_]types.Share{.{ .x = 1, .y = &[_]u8{1} }};
    try std.testing.expectError(CombineError.NotEnoughShares, combine(allocator, &single));
}

test "combine: rejects mismatched share lengths" {
    const allocator = std.testing.allocator;
    const shares = [_]types.Share{
        .{ .x = 1, .y = &[_]u8{ 1, 2 } },
        .{ .x = 2, .y = &[_]u8{3} },
    };
    try std.testing.expectError(CombineError.MismatchedShareLengths, combine(allocator, &shares));
}

test "combine: rejects x=0" {
    const allocator = std.testing.allocator;
    const shares = [_]types.Share{
        .{ .x = 0, .y = &[_]u8{1} },
        .{ .x = 1, .y = &[_]u8{2} },
    };
    try std.testing.expectError(CombineError.ZeroShareIndex, combine(allocator, &shares));
}

test "combine: rejects duplicate x values" {
    const allocator = std.testing.allocator;
    const shares = [_]types.Share{
        .{ .x = 1, .y = &[_]u8{1} },
        .{ .x = 1, .y = &[_]u8{2} },
    };
    try std.testing.expectError(CombineError.DuplicateShareIndices, combine(allocator, &shares));
}
