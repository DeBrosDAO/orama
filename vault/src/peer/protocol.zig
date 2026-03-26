/// Guardian-to-guardian binary protocol messages.
///
/// All messages are prefixed with a 1-byte type tag + 4-byte big-endian length.
/// Messages are sent over TCP on port 7501 (WireGuard-only interface).
const std = @import("std");

pub const PROTOCOL_VERSION: u8 = 1;

pub const MessageType = enum(u8) {
    heartbeat = 0x01,
    heartbeat_ack = 0x02,
    verify_request = 0x03,
    verify_response = 0x04,
    repair_offer = 0x05,
    repair_accept = 0x06,

    pub fn fromByte(b: u8) ?MessageType {
        return std.meta.intToEnum(MessageType, b) catch null;
    }
};

/// Maximum payload size (1 MiB). Prevents memory exhaustion from malicious peers.
pub const MAX_PAYLOAD_SIZE: u32 = 1024 * 1024;

/// Wire header: [version:1][type:1][length:4] = 6 bytes
pub const HEADER_SIZE = 6;

pub const Header = struct {
    version: u8,
    msg_type: MessageType,
    /// Length of the payload (NOT including header)
    payload_len: u32,
};

pub const Heartbeat = struct {
    /// Sender's node ID (WireGuard IP as 4 bytes for IPv4)
    sender_ip: [4]u8,
    sender_port: u16,
    /// Number of shares this guardian stores
    share_count: u32,
    /// Unix timestamp (seconds)
    timestamp: u64,
};

pub const VerifyRequest = struct {
    /// Identity hash to verify (hex)
    identity: [64]u8,
    identity_len: u8,
};

pub const VerifyResponse = struct {
    /// Identity hash being verified
    identity: [64]u8,
    identity_len: u8,
    /// Whether this guardian has the share
    has_share: bool,
    /// Merkle root of the share data (SHA-256)
    commitment_root: [32]u8,
};

/// Encode a header into bytes.
pub fn encodeHeader(header: Header) [HEADER_SIZE]u8 {
    var buf: [HEADER_SIZE]u8 = undefined;
    buf[0] = header.version;
    buf[1] = @intFromEnum(header.msg_type);
    std.mem.writeInt(u32, buf[2..6], header.payload_len, .big);
    return buf;
}

/// Decode a header from bytes.
/// Returns null if version is wrong, message type is invalid, or payload exceeds MAX_PAYLOAD_SIZE.
pub fn decodeHeader(buf: [HEADER_SIZE]u8) ?Header {
    if (buf[0] != PROTOCOL_VERSION) return null;
    const msg_type = MessageType.fromByte(buf[1]) orelse return null;
    const payload_len = std.mem.readInt(u32, buf[2..6], .big);

    // Reject payloads that exceed the maximum allowed size
    if (payload_len > MAX_PAYLOAD_SIZE) return null;

    return Header{
        .version = buf[0],
        .msg_type = msg_type,
        .payload_len = payload_len,
    };
}

/// Encode a heartbeat message into bytes.
pub fn encodeHeartbeat(hb: Heartbeat) [18]u8 {
    var buf: [18]u8 = undefined;
    @memcpy(buf[0..4], &hb.sender_ip);
    std.mem.writeInt(u16, buf[4..6], hb.sender_port, .big);
    std.mem.writeInt(u32, buf[6..10], hb.share_count, .big);
    std.mem.writeInt(u64, buf[10..18], hb.timestamp, .big);
    return buf;
}

/// Decode a heartbeat from bytes.
pub fn decodeHeartbeat(buf: []const u8) ?Heartbeat {
    if (buf.len < 18) return null;
    return Heartbeat{
        .sender_ip = buf[0..4].*,
        .sender_port = std.mem.readInt(u16, buf[4..6], .big),
        .share_count = std.mem.readInt(u32, buf[6..10], .big),
        .timestamp = std.mem.readInt(u64, buf[10..18], .big),
    };
}

/// Encode a verify request.
pub fn encodeVerifyRequest(req: VerifyRequest) [65]u8 {
    var buf: [65]u8 = undefined;
    @memcpy(buf[0..64], &req.identity);
    buf[64] = req.identity_len;
    return buf;
}

/// Decode a verify request.
/// Returns null if buffer is too short or identity_len exceeds the identity buffer.
pub fn decodeVerifyRequest(buf: []const u8) ?VerifyRequest {
    if (buf.len < 65) return null;
    const identity_len = buf[64];
    // identity_len must not exceed the 64-byte identity buffer
    if (identity_len > 64) return null;
    return VerifyRequest{
        .identity = buf[0..64].*,
        .identity_len = identity_len,
    };
}

/// Encode a verify response.
pub fn encodeVerifyResponse(resp: VerifyResponse) [98]u8 {
    var buf: [98]u8 = undefined;
    @memcpy(buf[0..64], &resp.identity);
    buf[64] = resp.identity_len;
    buf[65] = if (resp.has_share) 1 else 0;
    @memcpy(buf[66..98], &resp.commitment_root);
    return buf;
}

/// Decode a verify response.
/// Returns null if buffer is too short, identity_len exceeds the identity buffer,
/// or has_share contains an invalid value.
pub fn decodeVerifyResponse(buf: []const u8) ?VerifyResponse {
    if (buf.len < 98) return null;
    const identity_len = buf[64];
    // identity_len must not exceed the 64-byte identity buffer
    if (identity_len > 64) return null;
    // has_share must be 0 or 1
    if (buf[65] > 1) return null;
    return VerifyResponse{
        .identity = buf[0..64].*,
        .identity_len = identity_len,
        .has_share = buf[65] != 0,
        .commitment_root = buf[66..98].*,
    };
}

// ── Tests ────────────────────────────────────────────────────────────────────

test "header: encode/decode round-trip" {
    const header = Header{
        .version = PROTOCOL_VERSION,
        .msg_type = .heartbeat,
        .payload_len = 1234,
    };
    const encoded = encodeHeader(header);
    const decoded = decodeHeader(encoded).?;

    try std.testing.expectEqual(header.version, decoded.version);
    try std.testing.expectEqual(header.msg_type, decoded.msg_type);
    try std.testing.expectEqual(header.payload_len, decoded.payload_len);
}

test "header: wrong version returns null" {
    var encoded = encodeHeader(.{
        .version = PROTOCOL_VERSION,
        .msg_type = .heartbeat,
        .payload_len = 0,
    });
    encoded[0] = 99; // wrong version
    try std.testing.expect(decodeHeader(encoded) == null);
}

test "header: invalid message type returns null" {
    var encoded = encodeHeader(.{
        .version = PROTOCOL_VERSION,
        .msg_type = .heartbeat,
        .payload_len = 0,
    });
    encoded[1] = 0xFF; // invalid type
    try std.testing.expect(decodeHeader(encoded) == null);
}

test "heartbeat: encode/decode round-trip" {
    const hb = Heartbeat{
        .sender_ip = .{ 10, 0, 0, 1 },
        .sender_port = 7501,
        .share_count = 42,
        .timestamp = 1700000000,
    };
    const encoded = encodeHeartbeat(hb);
    const decoded = decodeHeartbeat(&encoded).?;

    try std.testing.expectEqualSlices(u8, &hb.sender_ip, &decoded.sender_ip);
    try std.testing.expectEqual(hb.sender_port, decoded.sender_port);
    try std.testing.expectEqual(hb.share_count, decoded.share_count);
    try std.testing.expectEqual(hb.timestamp, decoded.timestamp);
}

test "verify_request: encode/decode round-trip" {
    var req = VerifyRequest{
        .identity = .{0} ** 64,
        .identity_len = 10,
    };
    @memcpy(req.identity[0..10], "abcdef1234");

    const encoded = encodeVerifyRequest(req);
    const decoded = decodeVerifyRequest(&encoded).?;

    try std.testing.expectEqualSlices(u8, &req.identity, &decoded.identity);
    try std.testing.expectEqual(req.identity_len, decoded.identity_len);
}

test "verify_response: encode/decode round-trip" {
    var resp = VerifyResponse{
        .identity = .{0} ** 64,
        .identity_len = 8,
        .has_share = true,
        .commitment_root = .{0xAB} ** 32,
    };
    @memcpy(resp.identity[0..8], "deadbeef");

    const encoded = encodeVerifyResponse(resp);
    const decoded = decodeVerifyResponse(&encoded).?;

    try std.testing.expectEqualSlices(u8, &resp.identity, &decoded.identity);
    try std.testing.expectEqual(resp.identity_len, decoded.identity_len);
    try std.testing.expectEqual(resp.has_share, decoded.has_share);
    try std.testing.expectEqualSlices(u8, &resp.commitment_root, &decoded.commitment_root);
}
