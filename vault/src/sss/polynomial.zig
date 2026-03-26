/// Polynomial operations over GF(2^8).
///
/// Used by Shamir split to evaluate random polynomials at different points.
/// Horner's method for efficient evaluation: O(K) multiplications per point.
const std = @import("std");
const gf = @import("field.zig");

/// Evaluates a polynomial at point x using Horner's method.
///
/// coeffs[0] is the constant term (the secret byte).
/// coeffs[1] is the x^1 coefficient, etc.
///
/// Horner's: p(x) = coeffs[0] + x*(coeffs[1] + x*(coeffs[2] + ...))
///         = ((coeffs[K-1]*x + coeffs[K-2])*x + ... + coeffs[1])*x + coeffs[0]
pub fn evaluate(coeffs: []const u8, x: u8) u8 {
    if (coeffs.len == 0) return 0;
    if (x == 0) return coeffs[0]; // p(0) = constant term = secret

    // Horner's method: start from highest degree
    var result: u8 = coeffs[coeffs.len - 1];
    var i: usize = coeffs.len - 1;
    while (i > 0) {
        i -= 1;
        result = gf.add(gf.mul(result, x), coeffs[i]);
    }
    return result;
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "constant polynomial: p(x) = 42 for all x" {
    const coeffs = [_]u8{42};
    for (0..256) |x_int| {
        const x: u8 = @truncate(x_int);
        try std.testing.expectEqual(@as(u8, 42), evaluate(&coeffs, x));
    }
}

test "linear polynomial: p(0) returns constant term" {
    const coeffs = [_]u8{ 42, 7 }; // p(x) = 42 + 7x
    try std.testing.expectEqual(@as(u8, 42), evaluate(&coeffs, 0));
}

test "linear polynomial: p(1) = a0 + a1" {
    const coeffs = [_]u8{ 42, 7 }; // p(x) = 42 + 7x
    // p(1) = 42 XOR 7 = 45 (in GF(2^8), add = XOR)
    try std.testing.expectEqual(gf.add(42, 7), evaluate(&coeffs, 1));
}

test "quadratic: p(0) returns secret" {
    const coeffs = [_]u8{ 0xFF, 0x12, 0x34 }; // p(x) = 0xFF + 0x12*x + 0x34*x^2
    try std.testing.expectEqual(@as(u8, 0xFF), evaluate(&coeffs, 0));
}

test "empty coefficients returns 0" {
    const coeffs = [_]u8{};
    try std.testing.expectEqual(@as(u8, 0), evaluate(&coeffs, 42));
}

test "known test vector: verify Horner matches naive evaluation" {
    // p(x) = 5 + 3x + 7x^2
    const coeffs = [_]u8{ 5, 3, 7 };
    const x: u8 = 2;

    // Naive: 5 XOR mul(3,2) XOR mul(7, mul(2,2))
    const naive = gf.add(gf.add(5, gf.mul(3, x)), gf.mul(7, gf.mul(x, x)));
    try std.testing.expectEqual(naive, evaluate(&coeffs, x));
}
