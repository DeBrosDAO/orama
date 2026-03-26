/// GET /v1/vault/guardians — List active guardians.
///
/// Uses the real node list from the Guardian to report alive nodes,
/// threshold, and total count.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");

pub fn handle(writer: anytype, ctx: *const router.RouteContext) !void {
    if (ctx.guardian) |guardian| {
        // Build guardians list from real node list
        // First pass: count alive nodes for sizing
        var alive_count: usize = 0;
        for (guardian.nodes.nodes) |node| {
            if (node.state == .alive) alive_count += 1;
        }

        const threshold = guardian.nodes.threshold();
        const total = guardian.nodes.nodes.len;

        // Build JSON manually with a buffer
        var buf: [4096]u8 = undefined;
        var fbs = std.io.fixedBufferStream(&buf);
        const w = fbs.writer();

        w.writeAll("{\"guardians\":[") catch {
            return response.internalError(writer);
        };

        var first = true;
        for (guardian.nodes.nodes) |node| {
            if (node.state != .alive) continue;

            if (!first) {
                w.writeAll(",") catch {
                    return response.internalError(writer);
                };
            }
            first = false;

            std.fmt.format(w, "{{\"address\":\"{s}\",\"port\":{d}}}", .{ node.address, node.port }) catch {
                return response.internalError(writer);
            };
        }

        std.fmt.format(w, "],\"threshold\":{d},\"total\":{d}}}", .{ threshold, total }) catch {
            return response.internalError(writer);
        };

        try response.jsonOk(writer, fbs.getWritten());
    } else {
        // Fallback: no guardian available, report self only
        var buf: [512]u8 = undefined;
        const body = std.fmt.bufPrint(&buf,
            \\{{"guardians":[{{"address":"{s}","port":{d}}}],"threshold":3,"total":1}}
        , .{ ctx.listen_address, ctx.client_port }) catch {
            return response.internalError(writer);
        };
        try response.jsonOk(writer, body);
    }
}
