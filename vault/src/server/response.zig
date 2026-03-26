/// HTTP JSON response helpers.
const std = @import("std");

pub const ContentType = enum {
    json,
    text,

    pub fn header(self: ContentType) []const u8 {
        return switch (self) {
            .json => "application/json",
            .text => "text/plain",
        };
    }
};

/// Write a JSON success response.
pub fn jsonOk(writer: anytype, body: []const u8) !void {
    try writer.writeAll("HTTP/1.1 200 OK\r\n");
    try writer.writeAll("Content-Type: application/json\r\n");
    try std.fmt.format(writer, "Content-Length: {d}\r\n", .{body.len});
    try writer.writeAll("Connection: close\r\n");
    try writer.writeAll("\r\n");
    try writer.writeAll(body);
}

/// Write a JSON error response with given status code.
pub fn jsonError(writer: anytype, status_code: u16, status_text: []const u8, message: []const u8) !void {
    // Build JSON error body
    var buf: [1024]u8 = undefined;
    const body = std.fmt.bufPrint(&buf, "{{\"error\":\"{s}\"}}", .{message}) catch message;

    try std.fmt.format(writer, "HTTP/1.1 {d} {s}\r\n", .{ status_code, status_text });
    try writer.writeAll("Content-Type: application/json\r\n");
    try std.fmt.format(writer, "Content-Length: {d}\r\n", .{body.len});
    try writer.writeAll("Connection: close\r\n");
    try writer.writeAll("\r\n");
    try writer.writeAll(body);
}

pub fn notFound(writer: anytype) !void {
    try jsonError(writer, 404, "Not Found", "not found");
}

pub fn methodNotAllowed(writer: anytype) !void {
    try jsonError(writer, 405, "Method Not Allowed", "method not allowed");
}

pub fn badRequest(writer: anytype, message: []const u8) !void {
    try jsonError(writer, 400, "Bad Request", message);
}

pub fn internalError(writer: anytype) !void {
    try jsonError(writer, 500, "Internal Server Error", "internal server error");
}
