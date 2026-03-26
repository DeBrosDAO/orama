/// Structured logging for vault-guardian.
/// Uses std.log for systemd journal compatibility.
const std = @import("std");

const scope = std.log.scoped(.vault);

pub fn info(comptime fmt: []const u8, args: anytype) void {
    scope.info(fmt, args);
}

pub fn warn(comptime fmt: []const u8, args: anytype) void {
    scope.warn(fmt, args);
}

pub fn err(comptime fmt: []const u8, args: anytype) void {
    scope.err(fmt, args);
}
