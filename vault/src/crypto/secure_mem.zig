/// Secure memory utilities.
///
/// Provides:
/// - secureZero: volatile zero-fill that can't be optimized away
/// - mlock/munlock: prevent memory from being swapped to disk
/// - SecureBuffer: RAII wrapper that zeros and unlocks on deinit
const std = @import("std");
const builtin = @import("builtin");

/// Securely zeros a memory buffer using a volatile write.
/// This prevents the compiler from optimizing away the zero-fill,
/// which is critical for erasing keys and secrets from memory.
pub fn secureZero(buf: []u8) void {
    // Use volatile semantics via std.crypto to prevent optimization
    std.crypto.secureZero(u8, @as([]volatile u8, @volatileCast(buf)));
}

/// Locks memory pages so they won't be swapped to disk.
/// Non-fatal: logs warning on failure but doesn't error.
pub fn mlock(ptr: [*]const u8, len: usize) void {
    if (builtin.os.tag == .linux) {
        const result = std.posix.mlock(ptr[0..len]);
        if (result) |_| {} else |_| {
            // mlock failure is non-fatal — might need CAP_IPC_LOCK or higher RLIMIT_MEMLOCK
        }
    }
    // macOS: mlock exists but not worth the complexity for development
}

/// Unlocks previously locked memory pages.
pub fn munlock(ptr: [*]const u8, len: usize) void {
    if (builtin.os.tag == .linux) {
        std.posix.munlock(ptr[0..len]) catch {};
    }
}

/// A buffer that is automatically zeroed and munlocked on deinit.
/// Use for key material and other secrets.
pub const SecureBuffer = struct {
    data: []u8,
    allocator: std.mem.Allocator,

    pub fn init(allocator: std.mem.Allocator, len: usize) !SecureBuffer {
        const data = try allocator.alloc(u8, len);
        mlock(data.ptr, data.len);
        return SecureBuffer{
            .data = data,
            .allocator = allocator,
        };
    }

    pub fn deinit(self: *SecureBuffer) void {
        secureZero(self.data);
        munlock(self.data.ptr, self.data.len);
        self.allocator.free(self.data);
        self.data = &.{};
    }

    /// Creates a SecureBuffer from existing data (copies and mlocks).
    pub fn fromSlice(allocator: std.mem.Allocator, src: []const u8) !SecureBuffer {
        const buf = try init(allocator, src.len);
        @memcpy(buf.data, src);
        return buf;
    }
};

// ── Tests ────────────────────────────────────────────────────────────────────

test "secureZero: fills buffer with zeros" {
    var buf = [_]u8{ 0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE };
    secureZero(&buf);
    for (buf) |b| {
        try std.testing.expectEqual(@as(u8, 0), b);
    }
}

test "secureZero: handles empty buffer" {
    var buf = [_]u8{};
    secureZero(&buf); // should not crash
}

test "SecureBuffer: init and deinit" {
    const allocator = std.testing.allocator;
    var buf = try SecureBuffer.init(allocator, 32);
    // Fill with data
    @memset(buf.data, 0xAB);
    try std.testing.expectEqual(@as(usize, 32), buf.data.len);
    buf.deinit();
}

test "SecureBuffer: fromSlice copies data" {
    const allocator = std.testing.allocator;
    const src = [_]u8{ 1, 2, 3, 4, 5 };
    var buf = try SecureBuffer.fromSlice(allocator, &src);
    defer buf.deinit();

    try std.testing.expectEqualSlices(u8, &src, buf.data);
}

test "mlock/munlock: doesn't crash (no-op on non-Linux)" {
    var buf = [_]u8{0} ** 64;
    mlock(&buf, buf.len);
    munlock(&buf, buf.len);
}
