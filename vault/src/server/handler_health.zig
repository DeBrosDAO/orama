/// GET /v1/vault/health — Liveness and readiness check.
///
/// Checks:
/// - Share count > 0 or data dir writable
/// - Peer connectivity (degraded if no peers)
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");

pub fn handle(writer: anytype, ctx: *const router.RouteContext) !void {
    if (ctx.guardian) |guardian| {
        const share_count = guardian.share_count;

        // Check if data dir is accessible by seeing if we can stat it
        const data_dir_ok = blk: {
            std.fs.cwd().access(guardian.cfg.data_dir, .{}) catch break :blk false;
            break :blk true;
        };

        // Count alive peers (excluding self)
        var peer_count: usize = 0;
        for (guardian.nodes.nodes, 0..) |node, i| {
            if (guardian.nodes.self_index != null and i == guardian.nodes.self_index.?) continue;
            if (node.state == .alive) peer_count += 1;
        }

        // Determine status
        const status: []const u8 = if (!data_dir_ok)
            "unhealthy"
        else if (peer_count == 0)
            "degraded"
        else
            "ok";

        var buf: [512]u8 = undefined;
        const body = std.fmt.bufPrint(&buf,
            \\{{"status":"{s}","version":"0.1.0","shares":{d},"peers":{d},"data_dir_ok":{s}}}
        , .{
            status,
            share_count,
            peer_count,
            if (data_dir_ok) "true" else "false",
        }) catch {
            return response.internalError(writer);
        };
        try response.jsonOk(writer, body);
    } else {
        try response.jsonOk(writer, "{\"status\":\"ok\",\"version\":\"0.1.0\"}");
    }
}
