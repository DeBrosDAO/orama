/// V2 CRUD handlers for named secrets.
///
/// PUT    /v2/vault/secrets/{name}  — Store a named secret
/// GET    /v2/vault/secrets/{name}  — Retrieve a named secret
/// DELETE /v2/vault/secrets/{name}  — Delete a named secret
/// GET    /v2/vault/secrets         — List all secrets for the identity
///
/// All endpoints require session auth (X-Session-Token header) AND an
/// Ed25519 ownership proof (X-Vault-Pubkey + X-Vault-Signature, plus
/// X-Vault-Timestamp on GET/DELETE/LIST). The HMAC session proved the
/// caller could echo a server tag; it did not prove they hold the identity's key.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");
const log = @import("../log.zig");
const vault_store = @import("../storage/vault_store.zig");
const handler_auth = @import("handler_auth.zig");
const ownership = @import("../auth/ownership.zig");

/// Maximum request body size for PUT (1 MiB).
const MAX_BODY_SIZE = 1024 * 1024;

/// PUT /v2/vault/secrets/{name}
/// Body: {"share":"<base64>","version":<u64>}
/// Auth: X-Session-Token + Ed25519 ownership over identity/name/version.
pub fn handlePut(
    writer: anytype,
    body: []const u8,
    name: []const u8,
    ctx: *const router.RouteContext,
    req: router.Request,
) !void {
    const identity = requireAuth(writer, ctx, req.session_token) orelse return;

    if (body.len == 0) {
        return response.badRequest(writer, "empty body");
    }
    if (body.len > MAX_BODY_SIZE) {
        return response.badRequest(writer, "request body too large");
    }

    // Validate secret name
    vault_store.validateSecretName(name) catch |err| {
        return switch (err) {
            vault_store.VaultStoreError.SecretNameRequired => response.badRequest(writer, "secret name required"),
            vault_store.VaultStoreError.SecretNameTooLong => response.badRequest(writer, "secret name too long"),
            vault_store.VaultStoreError.SecretNameInvalid => response.badRequest(writer, "secret name invalid: only alphanumeric, underscore, hyphen allowed"),
            else => response.badRequest(writer, "invalid secret name"),
        };
    };

    // Parse JSON body
    const PutBody = struct {
        share: []const u8,
        version: u64,
    };

    const parsed = std.json.parseFromSlice(PutBody, ctx.allocator, body, .{}) catch {
        return response.badRequest(writer, "invalid JSON: expected {\"share\":\"<base64>\",\"version\":<u64>}");
    };
    defer parsed.deinit();

    const share_b64 = parsed.value.share;
    const version = parsed.value.version;

    if (!requireOwnership(writer, ctx, identity, req, .put, name, version)) return;

    // Decode base64
    const decoded_len = std.base64.standard.Decoder.calcSizeForSlice(share_b64) catch {
        return response.badRequest(writer, "invalid base64 in share");
    };

    if (decoded_len > vault_store.MAX_SECRET_SIZE) {
        return response.badRequest(writer, "share data too large");
    }

    const share_data = ctx.allocator.alloc(u8, decoded_len) catch {
        return response.internalError(writer);
    };
    defer ctx.allocator.free(share_data);

    std.base64.standard.Decoder.decode(share_data, share_b64) catch {
        return response.badRequest(writer, "invalid base64 in share");
    };

    if (share_data.len == 0) {
        return response.badRequest(writer, "share data is empty");
    }

    // Derive integrity key
    const integrity_key: []const u8 = if (ctx.guardian) |guardian|
        &guardian.integrity_key
    else
        "vault-default-integrity-key!!!!!";

    // Write to vault store
    vault_store.writeSecret(ctx.data_dir, identity, name, share_data, version, integrity_key, ctx.allocator) catch |err| {
        return switch (err) {
            vault_store.VaultStoreError.VersionConflict => response.badRequest(writer, "version must be greater than current stored version"),
            vault_store.VaultStoreError.SecretLimitExceeded => response.badRequest(writer, "secret limit exceeded"),
            vault_store.VaultStoreError.SecretDataTooLarge => response.badRequest(writer, "share data too large"),
            else => {
                log.err("failed to write secret '{s}' for {s}: {}", .{ name, identity, err });
                return response.internalError(writer);
            },
        };
    };

    log.info("stored secret '{s}' for identity {s} ({d} bytes, version {d})", .{ name, identity, share_data.len, version });

    // Build response
    var resp_buf: [512]u8 = undefined;
    const resp_body = std.fmt.bufPrint(&resp_buf,
        \\{{"status":"stored","name":"{s}","version":{d}}}
    , .{ name, version }) catch {
        return response.internalError(writer);
    };

    try response.jsonOk(writer, resp_body);
}

/// GET /v2/vault/secrets/{name}
/// Auth: X-Session-Token + Ed25519 ownership over identity/name/timestamp.
pub fn handleGet(
    writer: anytype,
    name: []const u8,
    ctx: *const router.RouteContext,
    req: router.Request,
) !void {
    const identity = requireAuth(writer, ctx, req.session_token) orelse return;
    if (!requireOwnership(writer, ctx, identity, req, .get, name, 0)) return;

    // Derive integrity key
    const integrity_key: []const u8 = if (ctx.guardian) |guardian|
        &guardian.integrity_key
    else
        "vault-default-integrity-key!!!!!";

    // Read secret
    const share_data = vault_store.readSecret(ctx.data_dir, identity, name, integrity_key, ctx.allocator) catch |err| {
        return switch (err) {
            vault_store.VaultStoreError.NotFound => response.jsonError(writer, 404, "Not Found", "secret not found"),
            vault_store.VaultStoreError.IntegrityCheckFailed => {
                log.err("integrity check failed for secret '{s}' identity {s}", .{ name, identity });
                return response.internalError(writer);
            },
            else => {
                log.err("failed to read secret '{s}' for {s}: {}", .{ name, identity, err });
                return response.internalError(writer);
            },
        };
    };
    defer ctx.allocator.free(share_data);

    // Read metadata
    const meta = vault_store.readMeta(ctx.data_dir, identity, name, ctx.allocator) catch {
        // If we can read the share but not meta, still return what we have
        return writeGetResponse(writer, share_data, name, 0, 0, 0, ctx.allocator);
    };

    return writeGetResponse(writer, share_data, name, meta.version, meta.created_ns, meta.updated_ns, ctx.allocator);
}

/// DELETE /v2/vault/secrets/{name}
/// Auth: X-Session-Token + Ed25519 ownership over identity/name/timestamp.
pub fn handleDelete(
    writer: anytype,
    name: []const u8,
    ctx: *const router.RouteContext,
    req: router.Request,
) !void {
    const identity = requireAuth(writer, ctx, req.session_token) orelse return;
    if (!requireOwnership(writer, ctx, identity, req, .delete, name, 0)) return;

    vault_store.deleteSecret(ctx.data_dir, identity, name, ctx.allocator) catch |err| {
        return switch (err) {
            vault_store.VaultStoreError.NotFound => response.jsonError(writer, 404, "Not Found", "secret not found"),
            else => {
                log.err("failed to delete secret '{s}' for {s}: {}", .{ name, identity, err });
                return response.internalError(writer);
            },
        };
    };

    log.info("deleted secret '{s}' for identity {s}", .{ name, identity });

    var resp_buf: [256]u8 = undefined;
    const resp_body = std.fmt.bufPrint(&resp_buf,
        \\{{"status":"deleted","name":"{s}"}}
    , .{name}) catch {
        return response.internalError(writer);
    };

    try response.jsonOk(writer, resp_body);
}

/// GET /v2/vault/secrets
/// Auth: X-Session-Token + Ed25519 ownership over identity/timestamp.
pub fn handleList(
    writer: anytype,
    ctx: *const router.RouteContext,
    req: router.Request,
) !void {
    const identity = requireAuth(writer, ctx, req.session_token) orelse return;
    if (!requireOwnership(writer, ctx, identity, req, .list, "", 0)) return;

    const names = vault_store.listSecrets(ctx.data_dir, identity, ctx.allocator) catch {
        return response.internalError(writer);
    };
    defer {
        for (names) |n| ctx.allocator.free(n);
        ctx.allocator.free(names);
    }

    // Build JSON response by writing parts to the writer directly.
    // First, gather all metadata to calculate content length.
    const SecretInfo = struct {
        name: []const u8,
        version: u64,
        size: usize,
        created_ns: i128,
        updated_ns: i128,
    };

    var infos = ctx.allocator.alloc(SecretInfo, names.len) catch {
        return response.internalError(writer);
    };
    defer ctx.allocator.free(infos);

    for (names, 0..) |n, i| {
        const meta = vault_store.readMeta(ctx.data_dir, identity, n, ctx.allocator) catch {
            infos[i] = .{ .name = n, .version = 0, .size = 0, .created_ns = 0, .updated_ns = 0 };
            continue;
        };
        infos[i] = .{
            .name = n,
            .version = meta.version,
            .size = meta.size,
            .created_ns = meta.created_ns,
            .updated_ns = meta.updated_ns,
        };
    }

    // Build JSON body in a dynamic buffer
    var body_buf: std.ArrayListUnmanaged(u8) = .{};
    defer body_buf.deinit(ctx.allocator);

    body_buf.appendSlice(ctx.allocator, "{\"secrets\":[") catch return response.internalError(writer);

    for (infos, 0..) |info, i| {
        if (i > 0) {
            body_buf.append(ctx.allocator, ',') catch return response.internalError(writer);
        }
        var item_buf: [512]u8 = undefined;
        const item = std.fmt.bufPrint(&item_buf,
            \\{{"name":"{s}","version":{d},"size":{d},"created_ns":{d},"updated_ns":{d}}}
        , .{ info.name, info.version, info.size, info.created_ns, info.updated_ns }) catch {
            return response.internalError(writer);
        };
        body_buf.appendSlice(ctx.allocator, item) catch return response.internalError(writer);
    }

    body_buf.appendSlice(ctx.allocator, "]}") catch return response.internalError(writer);

    try response.jsonOk(writer, body_buf.items);
}

// ── Internal helpers ─────────────────────────────────────────────────────────

/// Validates session token and extracts identity. Returns null (and sends 401) if auth fails.
fn requireAuth(
    writer: anytype,
    ctx: *const router.RouteContext,
    session_token: ?[]const u8,
) ?[]const u8 {
    const guardian = ctx.guardian orelse {
        response.internalError(writer) catch {};
        return null;
    };

    const tok = session_token orelse {
        response.jsonError(writer, 401, "Unauthorized", "session token required") catch {};
        return null;
    };

    const identity = handler_auth.validateSessionToken(tok, guardian.server_secret) orelse {
        response.jsonError(writer, 401, "Unauthorized", "invalid session token") catch {};
        return null;
    };

    return identity;
}

const OwnershipKind = enum { put, get, delete, list };

/// Session tokens are HMAC-echo and prove nothing about key possession.
/// V2 secrets therefore also require the same Ed25519 ownership V1 already
/// enforces. Missing or invalid proofs are 401, same as a bad session.
fn requireOwnership(
    writer: anytype,
    ctx: *const router.RouteContext,
    identity: []const u8,
    req: router.Request,
    kind: OwnershipKind,
    name: []const u8,
    version: u64,
) bool {
    if (ctx.guardian == null) return true;

    const pubkey = req.vault_pubkey orelse {
        response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature") catch {};
        return false;
    };
    const signature = req.vault_signature orelse {
        response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature") catch {};
        return false;
    };

    const ok = switch (kind) {
        .put => ownership.verifySecretPut(identity, name, version, pubkey, signature),
        .get, .delete, .list => blk: {
            const ts_s = req.vault_timestamp orelse {
                response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature") catch {};
                break :blk false;
            };
            const ts = std.fmt.parseInt(i64, ts_s, 10) catch {
                response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature") catch {};
                break :blk false;
            };
            const now = std.time.timestamp();
            break :blk switch (kind) {
                .get => ownership.verifySecretGet(identity, name, ts, now, pubkey, signature),
                .delete => ownership.verifySecretDelete(identity, name, ts, now, pubkey, signature),
                .list => ownership.verifySecretList(identity, ts, now, pubkey, signature),
                .put => unreachable,
            };
        },
    };
    if (!ok) {
        response.jsonError(writer, 401, "Unauthorized", "invalid ownership signature") catch {};
        return false;
    }
    return true;
}

/// Write the GET response with base64-encoded share data and metadata.
fn writeGetResponse(
    writer: anytype,
    share_data: []const u8,
    name: []const u8,
    version: u64,
    created_ns: i128,
    updated_ns: i128,
    allocator: std.mem.Allocator,
) !void {
    // Base64 encode
    const encoded_len = std.base64.standard.Encoder.calcSize(share_data.len);
    const encoded = allocator.alloc(u8, encoded_len) catch {
        return response.internalError(writer);
    };
    defer allocator.free(encoded);
    _ = std.base64.standard.Encoder.encode(encoded, share_data);

    // Build response by writing parts (avoid huge stack buffer)
    // {"share":"<b64>","name":"<name>","version":<v>,"created_ns":<ts>,"updated_ns":<ts>}
    var meta_buf: [512]u8 = undefined;
    const meta_part = std.fmt.bufPrint(&meta_buf,
        \\","name":"{s}","version":{d},"created_ns":{d},"updated_ns":{d}}}
    , .{ name, version, created_ns, updated_ns }) catch {
        return response.internalError(writer);
    };

    const prefix = "{\"share\":\"";
    const body_len = prefix.len + encoded.len + meta_part.len;

    try writer.writeAll("HTTP/1.1 200 OK\r\n");
    try writer.writeAll("Content-Type: application/json\r\n");
    try std.fmt.format(writer, "Content-Length: {d}\r\n", .{body_len});
    try writer.writeAll("Connection: close\r\n");
    try writer.writeAll("\r\n");
    try writer.writeAll(prefix);
    try writer.writeAll(encoded);
    try writer.writeAll(meta_part);
}
