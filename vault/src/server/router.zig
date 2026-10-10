/// Minimal HTTP request router.
///
/// Parses the request line (method + path) and dispatches to handlers.
/// No dependencies on std.http.Server — we parse raw HTTP/1.1 ourselves
/// for simplicity and to avoid API churn.
const std = @import("std");
const log = @import("../log.zig");
const response = @import("response.zig");
const handler_health = @import("handler_health.zig");
const handler_status = @import("handler_status.zig");
const handler_guardians = @import("handler_guardians.zig");
const handler_push = @import("handler_push.zig");
const handler_pull = @import("handler_pull.zig");
const handler_auth = @import("handler_auth.zig");
const handler_secrets = @import("handler_secrets.zig");
const guardian_mod = @import("../guardian.zig");

pub const Request = struct {
    method: Method,
    path: []const u8,
    body: []const u8,
    content_length: usize,
    /// Authorization header value (if present)
    authorization: ?[]const u8,
    /// X-Session-Token header value (if present)
    session_token: ?[]const u8,
    /// X-Vault-Pubkey (hex Ed25519 public key) for V2 ownership proofs.
    vault_pubkey: ?[]const u8,
    /// X-Vault-Signature (hex Ed25519 signature) for V2 ownership proofs.
    vault_signature: ?[]const u8,
    /// X-Vault-Timestamp (unix seconds) for V2 GET/DELETE/LIST proofs.
    vault_timestamp: ?[]const u8,
};

pub const Method = enum {
    GET,
    POST,
    PUT,
    DELETE,
    OPTIONS,
    UNKNOWN,

    pub fn fromString(s: []const u8) Method {
        if (std.mem.eql(u8, s, "GET")) return .GET;
        if (std.mem.eql(u8, s, "POST")) return .POST;
        if (std.mem.eql(u8, s, "PUT")) return .PUT;
        if (std.mem.eql(u8, s, "DELETE")) return .DELETE;
        if (std.mem.eql(u8, s, "OPTIONS")) return .OPTIONS;
        return .UNKNOWN;
    }
};

/// Parse HTTP request from raw bytes.
/// Returns null if the request is malformed.
pub fn parseRequest(buf: []const u8) ?Request {
    // Find end of request line
    const request_line_end = std.mem.indexOf(u8, buf, "\r\n") orelse return null;
    const request_line = buf[0..request_line_end];

    // Parse: METHOD /path HTTP/1.x
    var parts = std.mem.splitScalar(u8, request_line, ' ');
    const method_str = parts.next() orelse return null;
    const path = parts.next() orelse return null;
    // Skip HTTP version (parts.next())

    const method = Method.fromString(method_str);

    // Parse headers
    var content_length: usize = 0;
    var authorization: ?[]const u8 = null;
    var session_token: ?[]const u8 = null;
    var vault_pubkey: ?[]const u8 = null;
    var vault_signature: ?[]const u8 = null;
    var vault_timestamp: ?[]const u8 = null;

    const headers_end = std.mem.indexOf(u8, buf, "\r\n\r\n") orelse return null;
    const headers_section = buf[request_line_end + 2 .. headers_end];

    var header_iter = std.mem.splitSequence(u8, headers_section, "\r\n");
    while (header_iter.next()) |header_line| {
        if (header_line.len == 0) continue;
        if (std.ascii.startsWithIgnoreCase(header_line, "content-length:")) {
            const value = std.mem.trimLeft(u8, header_line["content-length:".len..], " ");
            content_length = std.fmt.parseInt(usize, value, 10) catch 0;
        } else if (std.ascii.startsWithIgnoreCase(header_line, "authorization:")) {
            authorization = std.mem.trimLeft(u8, header_line["authorization:".len..], " ");
        } else if (std.ascii.startsWithIgnoreCase(header_line, "x-session-token:")) {
            session_token = std.mem.trimLeft(u8, header_line["x-session-token:".len..], " ");
        } else if (std.ascii.startsWithIgnoreCase(header_line, "x-vault-pubkey:")) {
            vault_pubkey = std.mem.trimLeft(u8, header_line["x-vault-pubkey:".len..], " ");
        } else if (std.ascii.startsWithIgnoreCase(header_line, "x-vault-signature:")) {
            vault_signature = std.mem.trimLeft(u8, header_line["x-vault-signature:".len..], " ");
        } else if (std.ascii.startsWithIgnoreCase(header_line, "x-vault-timestamp:")) {
            vault_timestamp = std.mem.trimLeft(u8, header_line["x-vault-timestamp:".len..], " ");
        }
    }

    const body_start = headers_end + 4;
    const body = if (body_start < buf.len) buf[body_start..] else &[_]u8{};

    return Request{
        .method = method,
        .path = path,
        .body = body,
        .content_length = content_length,
        .authorization = authorization,
        .session_token = session_token,
        .vault_pubkey = vault_pubkey,
        .vault_signature = vault_signature,
        .vault_timestamp = vault_timestamp,
    };
}

/// Route a parsed request to the appropriate handler.
pub fn route(req: Request, writer: anytype, ctx: *const RouteContext) !void {
    // Health check (no auth required)
    if (std.mem.eql(u8, req.path, "/v1/vault/health")) {
        if (req.method != .GET) return response.methodNotAllowed(writer);
        return handler_health.handle(writer, ctx);
    }

    // Status
    if (std.mem.eql(u8, req.path, "/v1/vault/status")) {
        if (req.method != .GET) return response.methodNotAllowed(writer);
        return handler_status.handle(writer, ctx);
    }

    // Guardians list
    if (std.mem.eql(u8, req.path, "/v1/vault/guardians")) {
        if (req.method != .GET) return response.methodNotAllowed(writer);
        return handler_guardians.handle(writer, ctx);
    }

    // Auth: challenge
    if (std.mem.eql(u8, req.path, "/v1/vault/auth/challenge")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_auth.handleChallenge(writer, req.body, ctx);
    }

    // Auth: session
    if (std.mem.eql(u8, req.path, "/v1/vault/auth/session")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_auth.handleSession(writer, req.body, ctx);
    }

    // Push share
    if (std.mem.eql(u8, req.path, "/v1/vault/push")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_push.handle(writer, req.body, ctx, req.session_token);
    }

    // Pull share
    if (std.mem.eql(u8, req.path, "/v1/vault/pull")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_pull.handle(writer, req.body, ctx, req.session_token);
    }

    // V2: Auth endpoints (same handlers, new path prefix)
    if (std.mem.eql(u8, req.path, "/v2/vault/auth/challenge")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_auth.handleChallenge(writer, req.body, ctx);
    }

    if (std.mem.eql(u8, req.path, "/v2/vault/auth/session")) {
        if (req.method != .POST) return response.methodNotAllowed(writer);
        return handler_auth.handleSession(writer, req.body, ctx);
    }

    // V2: Named secrets CRUD
    if (std.mem.startsWith(u8, req.path, "/v2/vault/secrets")) {
        const prefix = "/v2/vault/secrets";
        const suffix = req.path[prefix.len..];

        if (suffix.len == 0) {
            // GET /v2/vault/secrets -> list
            if (req.method != .GET) return response.methodNotAllowed(writer);
            return handler_secrets.handleList(writer, ctx, req);
        }

        if (suffix[0] == '/') {
            const name = suffix[1..];
            if (name.len == 0) return response.badRequest(writer, "secret name required");
            return switch (req.method) {
                .PUT => handler_secrets.handlePut(writer, req.body, name, ctx, req),
                .GET => handler_secrets.handleGet(writer, name, ctx, req),
                .DELETE => handler_secrets.handleDelete(writer, name, ctx, req),
                else => response.methodNotAllowed(writer),
            };
        }
    }

    return response.notFound(writer);
}

pub const RouteContext = struct {
    data_dir: []const u8,
    listen_address: []const u8,
    client_port: u16,
    peer_port: u16,
    allocator: std.mem.Allocator,
    guardian: ?*guardian_mod.Guardian = null,
};

// ── Tests ────────────────────────────────────────────────────────────────────

test "parseRequest: GET with no body" {
    const raw = "GET /v1/vault/health HTTP/1.1\r\nHost: localhost\r\n\r\n";
    const req = parseRequest(raw).?;
    try std.testing.expectEqual(Method.GET, req.method);
    try std.testing.expectEqualSlices(u8, "/v1/vault/health", req.path);
    try std.testing.expectEqual(@as(usize, 0), req.content_length);
}

test "parseRequest: POST with content-length" {
    const raw = "POST /v1/vault/push HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello";
    const req = parseRequest(raw).?;
    try std.testing.expectEqual(Method.POST, req.method);
    try std.testing.expectEqualSlices(u8, "/v1/vault/push", req.path);
    try std.testing.expectEqual(@as(usize, 5), req.content_length);
    try std.testing.expectEqualSlices(u8, "hello", req.body);
}

test "parseRequest: authorization header" {
    const raw = "POST /v1/vault/push HTTP/1.1\r\nAuthorization: Bearer abc123\r\n\r\n";
    const req = parseRequest(raw).?;
    try std.testing.expectEqualSlices(u8, "Bearer abc123", req.authorization.?);
}

test "parseRequest: session token header" {
    const raw = "POST /v1/vault/push HTTP/1.1\r\nX-Session-Token: tok123\r\n\r\n";
    const req = parseRequest(raw).?;
    try std.testing.expectEqualSlices(u8, "tok123", req.session_token.?);
}

test "parseRequest: V2 ownership headers" {
    const raw = "GET /v2/vault/secrets/k HTTP/1.1\r\nX-Vault-Pubkey: aa\r\nX-Vault-Signature: bb\r\nX-Vault-Timestamp: 1700000000\r\n\r\n";
    const req = parseRequest(raw).?;
    try std.testing.expectEqualSlices(u8, "aa", req.vault_pubkey.?);
    try std.testing.expectEqualSlices(u8, "bb", req.vault_signature.?);
    try std.testing.expectEqualSlices(u8, "1700000000", req.vault_timestamp.?);
}

test "parseRequest: malformed returns null" {
    try std.testing.expect(parseRequest("garbage") == null);
    try std.testing.expect(parseRequest("GET\r\n\r\n") == null);
}
