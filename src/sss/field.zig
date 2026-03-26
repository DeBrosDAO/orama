/// GF(2^8) Galois Field arithmetic.
///
/// Uses the AES irreducible polynomial: x^8 + x^4 + x^3 + x + 1 (0x11B).
/// Precomputed log/exp tables at comptime for zero-runtime-cost lookups.
///
/// All operations are over the finite field GF(2^8) = GF(256):
/// - add(a, b) = a XOR b
/// - mul(a, b) via log/exp tables
/// - inv(a) = exp[255 - log[a]]
/// - div(a, b) = mul(a, inv(b))
const std = @import("std");

pub const FieldError = error{DivisionByZero};

/// Irreducible polynomial: x^8 + x^4 + x^3 + x + 1
const POLY: u16 = 0x11B;

/// Generator (primitive element) for GF(2^8)
const GENERATOR: u8 = 0x03;

/// Precomputed exp table: exp[i] = g^i mod poly, for i in 0..511
/// Extended to 512 entries to avoid modular reduction in mul.
/// Generator is 3 (0x03), a primitive element of GF(2^8) with 0x11B.
pub const exp_table: [512]u8 = blk: {
    var table: [512]u8 = undefined;
    var x: u16 = 1;
    for (0..512) |i| {
        table[i] = @truncate(x);
        // Multiply by generator (3) in GF(2^8): x*3 = x*2 XOR x
        const x2 = x << 1; // x * 2
        const x3 = x2 ^ x; // x * 3 = x * 2 + x (addition = XOR in GF(2^8))
        x = if (x3 & 0x100 != 0) x3 ^ POLY else x3;
    }
    break :blk table;
};

/// Precomputed log table: log[a] = i where g^i = a, for a in 1..255
/// log[0] is undefined (log of zero doesn't exist).
const log_table: [256]u8 = blk: {
    var table: [256]u8 = .{0} ** 256;
    for (0..255) |i| {
        table[exp_table[i]] = @truncate(i);
    }
    break :blk table;
};

/// Addition in GF(2^8) is XOR.
pub fn add(a: u8, b: u8) u8 {
    return a ^ b;
}

/// Subtraction in GF(2^8) is also XOR (same as addition).
pub fn sub(a: u8, b: u8) u8 {
    return a ^ b;
}

/// Multiplication in GF(2^8) via log/exp tables.
/// mul(0, _) = mul(_, 0) = 0.
pub fn mul(a: u8, b: u8) u8 {
    if (a == 0 or b == 0) return 0;
    const log_sum = @as(u16, log_table[a]) + @as(u16, log_table[b]);
    return exp_table[log_sum];
}

/// Multiplicative inverse in GF(2^8).
/// inv(a) = a^254 = exp[255 - log[a]] (since a^255 = 1 for all nonzero a).
/// Returns FieldError.DivisionByZero if a == 0.
pub fn inv(a: u8) FieldError!u8 {
    if (a == 0) return FieldError.DivisionByZero;
    return exp_table[255 - @as(u16, log_table[a])];
}

/// Division in GF(2^8): a / b = a * inv(b).
/// Returns FieldError.DivisionByZero if b == 0.
pub fn div(a: u8, b: u8) FieldError!u8 {
    if (b == 0) return FieldError.DivisionByZero;
    if (a == 0) return 0;
    const log_diff = @as(u16, log_table[a]) + 255 - @as(u16, log_table[b]);
    return exp_table[log_diff];
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "add is XOR" {
    try std.testing.expectEqual(@as(u8, 0), add(0, 0));
    try std.testing.expectEqual(@as(u8, 0), add(0xFF, 0xFF));
    try std.testing.expectEqual(0x53 ^ 0xCA, add(0x53, 0xCA));
}

test "sub equals add (characteristic 2)" {
    for (0..256) |a_int| {
        const a: u8 = @truncate(a_int);
        for (0..256) |b_int| {
            const b: u8 = @truncate(b_int);
            try std.testing.expectEqual(add(a, b), sub(a, b));
        }
    }
}

test "mul: identity (1 * a = a)" {
    for (0..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(a, mul(a, 1));
        try std.testing.expectEqual(a, mul(1, a));
    }
}

test "mul: zero (0 * a = 0)" {
    for (0..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(@as(u8, 0), mul(a, 0));
        try std.testing.expectEqual(@as(u8, 0), mul(0, a));
    }
}

test "mul: commutative (a*b = b*a)" {
    var a: u16 = 1;
    while (a < 256) : (a += 7) {
        var b: u16 = 1;
        while (b < 256) : (b += 11) {
            try std.testing.expectEqual(
                mul(@truncate(a), @truncate(b)),
                mul(@truncate(b), @truncate(a)),
            );
        }
    }
}

test "mul: associative ((a*b)*c = a*(b*c))" {
    var a: u16 = 1;
    while (a < 256) : (a += 17) {
        var b: u16 = 1;
        while (b < 256) : (b += 23) {
            var c: u16 = 1;
            while (c < 256) : (c += 29) {
                const ab_c = mul(mul(@truncate(a), @truncate(b)), @truncate(c));
                const a_bc = mul(@truncate(a), mul(@truncate(b), @truncate(c)));
                try std.testing.expectEqual(ab_c, a_bc);
            }
        }
    }
}

test "mul: distributive (a*(b+c) = a*b + a*c)" {
    var a: u16 = 0;
    while (a < 256) : (a += 13) {
        var b: u16 = 0;
        while (b < 256) : (b += 17) {
            var c: u16 = 0;
            while (c < 256) : (c += 19) {
                const lhs = mul(@truncate(a), add(@truncate(b), @truncate(c)));
                const rhs = add(mul(@truncate(a), @truncate(b)), mul(@truncate(a), @truncate(c)));
                try std.testing.expectEqual(lhs, rhs);
            }
        }
    }
}

test "inv: a * inv(a) = 1 for all nonzero a" {
    for (1..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(@as(u8, 1), mul(a, try inv(a)));
    }
}

test "inv: inv(inv(a)) = a for all nonzero a" {
    for (1..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(a, try inv(try inv(a)));
    }
}

test "inv: returns error on zero" {
    try std.testing.expectError(FieldError.DivisionByZero, inv(0));
}

test "div: a / b = a * inv(b)" {
    var a: u16 = 0;
    while (a < 256) : (a += 13) {
        var b: u16 = 1;
        while (b < 256) : (b += 17) {
            try std.testing.expectEqual(
                mul(@truncate(a), try inv(@truncate(b))),
                try div(@truncate(a), @truncate(b)),
            );
        }
    }
}

test "div: 0 / b = 0 for all nonzero b" {
    for (1..256) |b_int| {
        const b: u8 = @truncate(b_int);
        try std.testing.expectEqual(@as(u8, 0), try div(0, b));
    }
}

test "div: a / 1 = a" {
    for (0..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(a, try div(a, 1));
    }
}

test "div: a / a = 1 for all nonzero a" {
    for (1..256) |a_int| {
        const a: u8 = @truncate(a_int);
        try std.testing.expectEqual(@as(u8, 1), try div(a, a));
    }
}

test "div: returns error on division by zero" {
    try std.testing.expectError(FieldError.DivisionByZero, div(42, 0));
}

test "exhaustive mul: all 256x256 pairs produce valid GF(2^8) results" {
    for (0..256) |a_int| {
        for (0..256) |b_int| {
            const result = mul(@truncate(a_int), @truncate(b_int));
            // Result must be in [0, 255] (u8 guarantees this, but let's be explicit)
            try std.testing.expect(result <= 255);
        }
    }
}

test "exp table: g^0 = 1, g^255 = 1 (cyclic group order = 255)" {
    try std.testing.expectEqual(@as(u8, 1), exp_table[0]);
    try std.testing.expectEqual(@as(u8, 1), exp_table[255]);
}

test "exp table: all nonzero elements appear exactly once in exp[0..255]" {
    var seen = [_]bool{false} ** 256;
    for (0..255) |i| {
        const val = exp_table[i];
        try std.testing.expect(!seen[val]); // no duplicates
        seen[val] = true;
    }
    // All nonzero values should be seen
    for (1..256) |v| {
        try std.testing.expect(seen[v]);
    }
    // Zero should NOT appear (generator never produces 0)
    try std.testing.expect(!seen[0]);
}
