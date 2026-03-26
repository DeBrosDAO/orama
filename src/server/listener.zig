/// TCP listener for client-facing HTTP server (port 7500).
///
/// Single-threaded accept loop. Each connection is handled synchronously.
/// Supports graceful shutdown via atomic flag and per-IP rate limiting.
const std = @import("std");
const log = @import("../log.zig");
const router = @import("router.zig");
const response = @import("response.zig");
const posix = std.posix;

const MAX_REQUEST_SIZE = 1024 * 1024; // 1 MB max request
const READ_BUF_SIZE = 64 * 1024; // 64 KB read buffer

/// Rate limit: max requests per IP per window
const RATE_LIMIT_MAX = 120;
/// Rate limit window in seconds
const RATE_LIMIT_WINDOW_S: i64 = 60;

/// Per-IP rate limit entry
const RateEntry = struct {
    count: u32,
    window_start: i64,
};

/// Start the HTTP server. Blocks until shutdown flag is set.
pub fn serve(ctx: router.RouteContext, running: *std.atomic.Value(bool)) !void {
    const address = std.net.Address.parseIp(ctx.listen_address, ctx.client_port) catch |err| {
        log.err("invalid listen address {s}:{d}: {}", .{ ctx.listen_address, ctx.client_port, err });
        return err;
    };

    var server = address.listen(.{
        .reuse_address = true,
    }) catch |err| {
        log.err("failed to bind {s}:{d}: {}", .{ ctx.listen_address, ctx.client_port, err });
        return err;
    };
    defer server.deinit();

    // Set a receive timeout so accept() doesn't block forever.
    // This allows us to check the shutdown flag periodically.
    const timeout = posix.timeval{ .sec = 1, .usec = 0 };
    posix.setsockopt(server.stream.handle, posix.SOL.SOCKET, posix.SO.RCVTIMEO, std.mem.asBytes(&timeout)) catch {
        log.warn("failed to set SO_RCVTIMEO, shutdown may be delayed", .{});
    };

    log.info("HTTP server listening on {s}:{d}", .{ ctx.listen_address, ctx.client_port });

    // Rate limiter: IP string -> RateEntry
    var rate_map = std.StringHashMap(RateEntry).init(ctx.allocator);
    defer {
        var it = rate_map.iterator();
        while (it.next()) |entry| {
            ctx.allocator.free(entry.key_ptr.*);
        }
        rate_map.deinit();
    }

    var last_rate_cleanup: i64 = std.time.timestamp();

    while (running.load(.acquire)) {
        const conn = server.accept() catch |err| {
            if (err == error.WouldBlock) continue;
            log.warn("accept error: {}", .{err});
            continue;
        };

        // Periodically clean up stale rate limit entries
        const now = std.time.timestamp();
        if (now - last_rate_cleanup > RATE_LIMIT_WINDOW_S) {
            cleanupRateMap(&rate_map, now, ctx.allocator);
            last_rate_cleanup = now;
        }

        // Extract peer IP for rate limiting
        var ip_buf: [45]u8 = undefined;
        const peer_ip = formatIp(conn.address, &ip_buf);

        // Rate limit check
        if (isRateLimited(&rate_map, peer_ip, now, ctx.allocator)) {
            var resp_buf: [512]u8 = undefined;
            var fbs = std.io.fixedBufferStream(&resp_buf);
            const w = fbs.writer();
            response.jsonError(w, 429, "Too Many Requests", "rate limit exceeded") catch {};
            conn.stream.writeAll(fbs.getWritten()) catch {};
            conn.stream.close();
            continue;
        }

        handleConnection(conn, &ctx) catch |err| {
            log.warn("connection error: {}", .{err});
        };
    }

    log.info("HTTP server shutting down", .{});
}

/// Format an address to a string for use as rate-limit key.
/// Returns a slice into the provided buffer.
fn formatIp(addr: std.net.Address, buf: []u8) []const u8 {
    // Extract IP bytes from the sockaddr
    const ip_bytes: [4]u8 = @bitCast(addr.in.sa.addr);
    const result = std.fmt.bufPrint(buf, "{d}.{d}.{d}.{d}", .{
        ip_bytes[0], ip_bytes[1], ip_bytes[2], ip_bytes[3],
    }) catch return "unknown";
    return result;
}

fn isRateLimited(
    rate_map: *std.StringHashMap(RateEntry),
    key_slice: []const u8,
    now: i64,
    allocator: std.mem.Allocator,
) bool {
    if (key_slice.len == 0) return false;

    if (rate_map.getPtr(key_slice)) |entry| {
        if (now - entry.window_start >= RATE_LIMIT_WINDOW_S) {
            entry.count = 1;
            entry.window_start = now;
            return false;
        }
        entry.count += 1;
        return entry.count > RATE_LIMIT_MAX;
    } else {
        const owned_key = allocator.dupe(u8, key_slice) catch return false;
        rate_map.put(owned_key, RateEntry{ .count = 1, .window_start = now }) catch {
            allocator.free(owned_key);
        };
        return false;
    }
}

fn cleanupRateMap(rate_map: *std.StringHashMap(RateEntry), now: i64, allocator: std.mem.Allocator) void {
    // Collect keys to remove into a bounded stack buffer.
    // If there are more stale entries than fit, they'll be cleaned next cycle.
    var to_remove: [64][]const u8 = undefined;
    var remove_count: usize = 0;

    var it = rate_map.iterator();
    while (it.next()) |entry| {
        if (now - entry.value_ptr.window_start >= RATE_LIMIT_WINDOW_S * 2) {
            if (remove_count < to_remove.len) {
                to_remove[remove_count] = entry.key_ptr.*;
                remove_count += 1;
            }
        }
    }

    for (to_remove[0..remove_count]) |key| {
        _ = rate_map.remove(key);
        allocator.free(key);
    }
}

fn handleConnection(conn: std.net.Server.Connection, ctx: *const router.RouteContext) !void {
    defer conn.stream.close();

    // Read the full request into a buffer
    var buf: [READ_BUF_SIZE]u8 = undefined;
    var total: usize = 0;

    while (total < buf.len) {
        const n = conn.stream.read(buf[total..]) catch |err| {
            if (err == error.ConnectionResetByPeer) return;
            return err;
        };
        if (n == 0) break;
        total += n;

        if (std.mem.indexOf(u8, buf[0..total], "\r\n\r\n")) |headers_end| {
            const req = router.parseRequest(buf[0..total]) orelse break;
            const body_start = headers_end + 4;
            const body_received = total - body_start;
            if (body_received >= req.content_length) break;
        }
    }

    if (total == 0) return;

    const req = router.parseRequest(buf[0..total]) orelse {
        var resp_stream = conn.stream;
        var write_buf: [4096]u8 = undefined;
        var fbs = std.io.fixedBufferStream(&write_buf);
        const writer = fbs.writer();
        try response.badRequest(writer, "malformed request");
        const written = fbs.getWritten();
        resp_stream.writeAll(written) catch {};
        return;
    };

    var resp_buf: [READ_BUF_SIZE]u8 = undefined;
    var fbs = std.io.fixedBufferStream(&resp_buf);
    const writer = fbs.writer();

    router.route(req, writer, ctx) catch |err| {
        log.warn("handler error for {s}: {}", .{ req.path, err });
        var err_fbs = std.io.fixedBufferStream(&resp_buf);
        const err_writer = err_fbs.writer();
        response.internalError(err_writer) catch return;
        conn.stream.writeAll(err_fbs.getWritten()) catch {};
        return;
    };

    conn.stream.writeAll(fbs.getWritten()) catch {};
}
