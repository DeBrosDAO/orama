/// Periodic verification of share integrity across guardians.
///
/// Randomly selects shares and asks peers to confirm they have the same
/// commitment hash. Detects tampering and data corruption.
const std = @import("std");
const log = @import("../log.zig");
const protocol = @import("protocol.zig");
const node_list = @import("../membership/node_list.zig");

pub const VerifyResult = struct {
    identity: []const u8,
    peer_address: []const u8,
    matches: bool,
    peer_has_share: bool,
};

/// Send a verify request to a peer and wait for the response.
/// Returns null if the peer is unreachable or returns invalid data.
pub fn verifyWithPeer(
    peer_address: []const u8,
    peer_port: u16,
    identity: []const u8,
) ?protocol.VerifyResponse {
    const address = std.net.Address.parseIp4(peer_address, peer_port) catch return null;
    const stream = std.net.tcpConnectToAddress(address) catch return null;
    defer stream.close();

    // Build request
    var req = protocol.VerifyRequest{
        .identity = .{0} ** 64,
        .identity_len = @intCast(@min(identity.len, 64)),
    };
    @memcpy(req.identity[0..req.identity_len], identity[0..req.identity_len]);

    const payload = protocol.encodeVerifyRequest(req);
    const header = protocol.encodeHeader(.{
        .version = protocol.PROTOCOL_VERSION,
        .msg_type = .verify_request,
        .payload_len = payload.len,
    });

    stream.writeAll(&header) catch return null;
    stream.writeAll(&payload) catch return null;

    // Read response header
    var resp_header_buf: [protocol.HEADER_SIZE]u8 = undefined;
    stream.readAll(&resp_header_buf) catch return null;
    const resp_header = protocol.decodeHeader(resp_header_buf) orelse return null;

    if (resp_header.msg_type != .verify_response) return null;
    if (resp_header.payload_len < 98) return null;

    // Read response payload
    var resp_buf: [98]u8 = undefined;
    stream.readAll(&resp_buf) catch return null;

    return protocol.decodeVerifyResponse(&resp_buf);
}

/// Compare our local commitment with a peer's commitment.
pub fn compareCommitments(
    local_root: [32]u8,
    peer_response: protocol.VerifyResponse,
) VerifyResult {
    return VerifyResult{
        .identity = peer_response.identity[0..peer_response.identity_len],
        .peer_address = "", // caller fills in
        .peer_has_share = peer_response.has_share,
        .matches = peer_response.has_share and
            std.mem.eql(u8, &local_root, &peer_response.commitment_root),
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "compareCommitments: matching roots" {
    const root = [_]u8{0xAB} ** 32;
    const resp = protocol.VerifyResponse{
        .identity = .{0} ** 64,
        .identity_len = 4,
        .has_share = true,
        .commitment_root = root,
    };

    const result = compareCommitments(root, resp);
    try std.testing.expect(result.matches);
    try std.testing.expect(result.peer_has_share);
}

test "compareCommitments: mismatched roots" {
    const local_root = [_]u8{0xAB} ** 32;
    var peer_root = [_]u8{0xAB} ** 32;
    peer_root[0] = 0xCD; // tamper

    const resp = protocol.VerifyResponse{
        .identity = .{0} ** 64,
        .identity_len = 4,
        .has_share = true,
        .commitment_root = peer_root,
    };

    const result = compareCommitments(local_root, resp);
    try std.testing.expect(!result.matches);
}

test "compareCommitments: peer missing share" {
    const root = [_]u8{0xAB} ** 32;
    const resp = protocol.VerifyResponse{
        .identity = .{0} ** 64,
        .identity_len = 4,
        .has_share = false,
        .commitment_root = .{0} ** 32,
    };

    const result = compareCommitments(root, resp);
    try std.testing.expect(!result.matches);
    try std.testing.expect(!result.peer_has_share);
}
