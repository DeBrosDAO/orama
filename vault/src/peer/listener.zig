/// Guardian-to-guardian TCP listener (port 7501, WireGuard only).
///
/// Handles incoming heartbeats and verification requests from peers.
const std = @import("std");
const log = @import("../log.zig");
const protocol = @import("protocol.zig");
const node_list = @import("../membership/node_list.zig");

pub const PeerContext = struct {
    nodes: *node_list.NodeList,
    data_dir: []const u8,
    allocator: std.mem.Allocator,
};

/// Start the peer protocol listener. Blocks forever.
pub fn serve(listen_address: []const u8, port: u16, ctx: PeerContext) !void {
    const address = std.net.Address.parseIp(listen_address, port) catch |err| {
        log.err("invalid peer listen address {s}:{d}: {}", .{ listen_address, port, err });
        return err;
    };

    var server = address.listen(.{
        .reuse_address = true,
    }) catch |err| {
        log.err("failed to bind peer listener {s}:{d}: {}", .{ listen_address, port, err });
        return err;
    };
    defer server.deinit();

    log.info("peer protocol listening on {s}:{d}", .{ listen_address, port });

    while (true) {
        const conn = server.accept() catch |err| {
            log.warn("peer accept error: {}", .{err});
            continue;
        };

        handlePeerConnection(conn, &ctx) catch |err| {
            log.warn("peer connection error: {}", .{err});
        };
    }
}

fn handlePeerConnection(conn: std.net.Server.Connection, ctx: *const PeerContext) !void {
    defer conn.stream.close();

    // Read header
    var header_buf: [protocol.HEADER_SIZE]u8 = undefined;
    conn.stream.readAll(&header_buf) catch return;

    const header = protocol.decodeHeader(header_buf) orelse return;

    if (header.payload_len > 1024 * 1024) return; // reject >1MB payloads

    // Read payload
    const payload = ctx.allocator.alloc(u8, header.payload_len) catch return;
    defer ctx.allocator.free(payload);
    conn.stream.readAll(payload) catch return;

    switch (header.msg_type) {
        .heartbeat => {
            const hb = protocol.decodeHeartbeat(payload) orelse return;
            handleHeartbeat(hb, ctx);

            // Send ACK
            const ack_header = protocol.encodeHeader(.{
                .version = protocol.PROTOCOL_VERSION,
                .msg_type = .heartbeat_ack,
                .payload_len = 0,
            });
            conn.stream.writeAll(&ack_header) catch {};
        },
        .verify_request => {
            const req = protocol.decodeVerifyRequest(payload) orelse return;
            handleVerifyRequest(req, conn.stream, ctx);
        },
        else => {
            // Unknown/unhandled message type
        },
    }
}

fn handleHeartbeat(hb: protocol.Heartbeat, ctx: *const PeerContext) void {
    // Format sender IP
    var addr_buf: [16]u8 = undefined;
    const addr = std.fmt.bufPrint(&addr_buf, "{d}.{d}.{d}.{d}", .{
        hb.sender_ip[0], hb.sender_ip[1], hb.sender_ip[2], hb.sender_ip[3],
    }) catch return;

    ctx.nodes.updateState(addr, hb.sender_port, .alive);
}

fn handleVerifyRequest(req: protocol.VerifyRequest, stream: std.net.Stream, ctx: *const PeerContext) void {
    const identity = req.identity[0..req.identity_len];

    // Check if we have this share
    var path_buf: [4096]u8 = undefined;
    const share_path = std.fmt.bufPrint(&path_buf, "{s}/shares/{s}/share.bin", .{
        ctx.data_dir, identity,
    }) catch return;

    var resp = protocol.VerifyResponse{
        .identity = req.identity,
        .identity_len = req.identity_len,
        .has_share = false,
        .commitment_root = .{0} ** 32,
    };

    // Try to read the share and compute its hash
    if (std.fs.cwd().readFileAlloc(ctx.allocator, share_path, 1024 * 1024)) |data| {
        defer ctx.allocator.free(data);
        resp.has_share = true;
        // Compute SHA-256 as commitment
        std.crypto.hash.sha2.Sha256.hash(data, &resp.commitment_root, .{});
    } else |_| {}

    const payload = protocol.encodeVerifyResponse(resp);
    const header = protocol.encodeHeader(.{
        .version = protocol.PROTOCOL_VERSION,
        .msg_type = .verify_response,
        .payload_len = payload.len,
    });

    stream.writeAll(&header) catch {};
    stream.writeAll(&payload) catch {};
}
